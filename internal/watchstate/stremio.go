package watchstate

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func ObserveStremio(ctx context.Context, tx pgx.Tx, user int64, account, identity string, metadata []byte, played bool, since, at time.Time) error {
	return observeStremio(ctx, tx, user, account, identity, metadata, played, since, at, false)
}

// Shared-account read-back updates state without inventing a second viewing event.
func ObserveStremioEcho(ctx context.Context, tx pgx.Tx, user int64, account, identity string, metadata []byte, played bool, since, at time.Time) error {
	return observeStremio(ctx, tx, user, account, identity, metadata, played, since, at, true)
}
func observeStremio(ctx context.Context, tx pgx.Tx, user int64, account, identity string, metadata []byte, played bool, since, at time.Time, echo bool) error {
	media, err := importIdentity(ctx, tx, identity, metadata)
	if err != nil {
		return err
	}
	var state State
	var source string
	var review bool
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT state,source,updated_at,needs_review FROM watch_current WHERE user_id=$1 AND media_id=$2 FOR UPDATE`, user, media).Scan(&state, &source, &updated, &review)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return err
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stremio_outgoing WHERE user_id=$1 AND media_id=$2 AND phase IN ('pending','prepared'))`, user, media).Scan(&pending); err != nil {
		return err
	}
	// A newer legitimate TED decision in this polling window wins.
	if review || pending || !missing && source != "StremioImport" && source != "StremioPoll" && !updated.Before(since) {
		return nil
	}
	if !missing && state.Played == played {
		return nil
	}
	state.Played = played
	value, _ := json.Marshal(state)
	_, err = tx.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,$3,'StremioPoll',$4) ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state,source=EXCLUDED.source,observed_at=EXCLUDED.observed_at,updated_at=now(),revision=watch_current.revision+1,needs_review=false`, user, media, value, at)
	if err != nil {
		return err
	}
	if !echo {
		if _, err = tx.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at,stremio_account) VALUES($1,$2,'stremio_observed',$3,$4,$5)`, user, media, value, at, account); err != nil {
			return err
		}
	}
	return nil
}
