package watchstate

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Only accepted explicit decisions and completed playback call this helper.
// Snapshot/import/position changes and incoming Stremio acknowledgments do not.
func queueStremio(ctx context.Context, tx pgx.Tx, user int64, media string, played bool) error {
	_, err := tx.Exec(ctx, `INSERT INTO stremio_outgoing(link_id,account_id,user_id,media_id,identity,title_id,played)
 SELECT l.id,l.account_id,l.user_id,c.id,c.cinemeta_id,split_part(c.cinemeta_id,':',1),$3
 FROM stremio_links l
 JOIN catalog_media c ON c.id=$2 WHERE l.user_id=$1 AND l.retired_at IS NULL`, user, media, played)
	return err
}
