package watchstate

import (
	"context"
	"time"

	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
)

// Caller holds the same user/server locks as watched-flag delivery. This is
// provider recency evidence, not a fabricated play count or history entry.
func ObserveStremioEpisodeCompletion(ctx context.Context, tx pgx.Tx, user int64, account, identity string, at time.Time) error {
	if !cinemeta.ValidVideoID(identity) || at.Year() < 1970 || at.Year() > 2100 {
		return ErrInvalid
	}
	_, err := tx.Exec(ctx, `INSERT INTO stremio_episode_recency(user_id,media_id,stremio_account,watched_at)
 SELECT w.user_id,w.media_id,$2,$4 FROM watch_current w JOIN catalog_media c ON c.id=w.media_id
 WHERE w.user_id=$1 AND c.kind='episode' AND c.cinemeta_id=$3 AND NOT w.needs_review
 AND (w.state->>'played')::boolean AND w.source!='TEDForget'
 ON CONFLICT(user_id,media_id) DO UPDATE SET stremio_account=EXCLUDED.stremio_account,watched_at=EXCLUDED.watched_at
 WHERE EXCLUDED.watched_at>stremio_episode_recency.watched_at`, user, account, identity, at)
	return err
}
