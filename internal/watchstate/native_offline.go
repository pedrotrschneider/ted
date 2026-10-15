package watchstate

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"github.com/jackc/pgx/v5"
)

// Offline sessions identify viewing, not a credential or a source permission.
// A client may register its own historical viewing after a fresh same-device
// login without keeping a streaming session alive while disconnected.
type NativeOfflineSession struct {
	ID              string                `json:"id"`
	Media           catalog.MediaIdentity `json:"media"`
	WatchRevision   int64                 `json:"watch_revision"`
	StartedAt       time.Time             `json:"started_at"`
	SourceSession   string                `json:"source_session,omitempty"`
	PreviousSession string                `json:"previous_session,omitempty"`
}

func (s *Store) registerOffline(ctx context.Context, tx pgx.Tx, g accounts.NativeGrant, sessions []NativeOfflineSession) error {
	if len(sessions) > 100 {
		return ErrInvalid
	}
	for _, session := range sessions {
		if !uuid.MatchString(session.ID) || !session.Media.Valid() || session.WatchRevision < 0 || session.StartedAt.Year() < 1970 || session.StartedAt.Year() > 2100 || (session.SourceSession != "" && !uuid.MatchString(session.SourceSession)) || session.PreviousSession == session.ID || (session.PreviousSession != "" && !uuid.MatchString(session.PreviousSession)) {
			return ErrInvalid
		}
		session.StartedAt = session.StartedAt.UTC()
		raw, err := json.Marshal(session)
		if err != nil {
			return err
		}
		// Advisory locking also protects the absent-row registration race. UUIDs
		// are global; a different account/device must never adopt an existing id.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "offline-session:"+session.ID); err != nil {
			return err
		}
		var same bool
		err = tx.QueryRow(ctx, `SELECT user_id=$2 AND device_id=$3 AND origin='offline' AND offline_registration=$4::jsonb FROM native_playback_sessions WHERE id=$1`, session.ID, g.User.ID, g.DeviceID, raw).Scan(&same)
		if err == nil {
			if !same {
				return ErrConflict
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if session.StartedAt.After(now.Add(5 * time.Minute)) {
			return ErrInvalid
		}
		media, err := EnsureNativeIdentity(ctx, tx, session.Media)
		if err != nil {
			return err
		}
		if session.SourceSession != "" {
			var owned bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 AND media_id=$4 AND origin='live')`, session.SourceSession, g.User.ID, g.DeviceID, media).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return ErrInvalid
			}
		}
		if session.PreviousSession != "" {
			var owned bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM native_playback_sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 AND media_id=$4)`, session.PreviousSession, g.User.ID, g.DeviceID, media).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return ErrInvalid
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO native_playback_sessions(id,user_id,family_id,device_id,media_id,duration_ms,applied_revision,origin,offline_registration,previous_offline_session)
   VALUES($1,$2,$3,$4,$5,0,$6,'offline',$7,NULLIF($8,'')::uuid)`, session.ID, g.User.ID, g.FamilyID, g.DeviceID, media, session.WatchRevision, raw, session.PreviousSession)
		if err != nil {
			return err
		}
	}
	return nil
}
