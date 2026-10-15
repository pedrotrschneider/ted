// Package sourceprefs persists shared source preferences independently of
// account-owned playback URLs and per-user watch state.
package sourceprefs

import (
	"context"
	"github.com/jackc/pgx/v5"
	"time"
)

// RecordStarted is part of watch intake's transaction, after event deduplication.
// Only actual viewing counts; preparing a session or download does not. Original
// report timestamps keep delayed/offline reports from displacing newer viewing.
func RecordStarted(ctx context.Context, tx pgx.Tx, session, event string, at time.Time) error {
	var key, scope string
	err := tx.QueryRow(ctx, `SELECT COALESCE(s.favorite_key,''),COALESCE(s.favorite_scope,'') FROM native_playback_sessions p LEFT JOIN native_playback_sessions original ON original.id=NULLIF(p.offline_registration->>'source_session','')::uuid LEFT JOIN native_addon_sources s ON s.id=COALESCE(p.addon_source_id,original.addon_source_id) WHERE p.id=$1`, session).Scan(&key, &scope)
	if err != nil || key == "" {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recent-sources:"+scope); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO native_recent_sources(scope,source_key,last_used_at,event_id) VALUES($1,$2,$3,$4) ON CONFLICT(scope,source_key) DO UPDATE SET last_used_at=EXCLUDED.last_used_at,event_id=EXCLUDED.event_id WHERE native_recent_sources.last_used_at<EXCLUDED.last_used_at`, scope, key, at, event); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM native_recent_sources WHERE scope=$1 AND source_key NOT IN (SELECT source_key FROM native_recent_sources WHERE scope=$1 ORDER BY last_used_at DESC,source_key DESC LIMIT 3)`, scope)
	return err
}
