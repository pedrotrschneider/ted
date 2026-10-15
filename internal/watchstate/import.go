package watchstate

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// importStremio adds historical evidence without replacing an existing current
// decision, resume position or catalog metadata. The import time is receipt time,
// never an invented last-played date. Caller owns the durable transaction.
func importStremio(ctx context.Context, tx pgx.Tx, user int64, account, identity string, metadata []byte, at time.Time) error {
	media, err := importIdentity(ctx, tx, identity, metadata)
	if err != nil {
		return err
	}
	var forgotten bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM watch_current WHERE user_id=$1 AND media_id=$2 AND source='TEDForget')`, user, media).Scan(&forgotten); err != nil {
		return err
	}
	if forgotten {
		return nil
	}
	state, _ := json.Marshal(State{Played: true})
	if at.IsZero() {
		if err = tx.QueryRow(ctx, "SELECT now()").Scan(&at); err != nil {
			return err
		}
	}
	result, err := tx.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at,stremio_account) VALUES($1,$2,'stremio_import',$3,$5,$4) ON CONFLICT(user_id,media_id,stremio_account) WHERE kind='stremio_import' DO NOTHING`, user, media, state, account, at)
	if err != nil || result.RowsAffected() == 0 {
		return err
	}
	// Concurrent live viewing wins through the unique key; no read-then-write race.
	_, err = tx.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,$3,'StremioImport',$4) ON CONFLICT(user_id,media_id) DO NOTHING`, user, media, state, at)
	return err
}

func importIdentity(ctx context.Context, tx pgx.Tx, identity string, metadata []byte) (string, error) {
	kind := "movie"
	if strings.Contains(identity, ":") {
		kind = "episode"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,$3) ON CONFLICT(cinemeta_id) DO NOTHING`, identity, kind, metadata); err != nil {
		return "", err
	}
	var media string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1 AND kind=$2`, identity, kind).Scan(&media); err != nil {
		return "", err
	}
	return media, nil
}

// EnrollStremio captures pre-existing remote watched evidence without replacing a
// saved TED decision.
func EnrollStremio(ctx context.Context, tx pgx.Tx, user int64, account, identity string, metadata []byte, at time.Time) error {
	if err := importStremio(ctx, tx, user, account, identity, metadata, at); err != nil {
		return err
	}
	// A retained explicit unwatched decision also needs delivery when a title
	// first appears, or becomes resolvable after the initial account sync.
	if _, err := tx.Exec(ctx, `INSERT INTO stremio_outgoing(link_id,account_id,user_id,media_id,identity,title_id,played)
      SELECT l.id,l.account_id,w.user_id,c.id,c.cinemeta_id,split_part(c.cinemeta_id,':',1),false
      FROM watch_current w JOIN catalog_media c ON c.id=w.media_id JOIN stremio_links l ON l.user_id=w.user_id
      WHERE w.user_id=$1 AND c.cinemeta_id=$2 AND l.account_id=$3 AND l.retired_at IS NULL
      AND NOT w.needs_review AND (w.state->>'played')::boolean=false
      AND NOT EXISTS(SELECT 1 FROM stremio_outgoing o WHERE o.link_id=l.id AND o.media_id=c.id AND o.phase IN('pending','prepared'))`, user, identity, account); err != nil {
		return err
	}
	return nil
}
