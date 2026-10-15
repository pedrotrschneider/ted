package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"time"

	"github.com/jackc/pgx/v5"
)

type Entry struct {
	ID    int64     `json:"id"`
	Media string    `json:"media_id"`
	Title string    `json:"title"`
	Kind  string    `json:"kind"`
	State State     `json:"state"`
	At    time.Time `json:"observed_at"`
}
type Current struct {
	Revision int64 `json:"revision"`

	Media  string    `json:"media_id"`
	Title  string    `json:"title"`
	State  State     `json:"state"`
	Source string    `json:"source"`
	Review bool      `json:"needs_review"`
	At     time.Time `json:"observed_at"`
}
type History struct {
	Entries []Entry   `json:"entries"`
	Next    *int64    `json:"next_before,omitempty"`
	Current []Current `json:"current"`
}

func (s *Store) History(ctx context.Context, who accounts.Identity, before int64) (History, error) {
	out := History{Entries: []Entry{}, Current: []Current{}}
	if before < 0 {
		return out, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer database.Rollback(tx)
	if err = authorize(ctx, tx, who, false); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT h.id,h.media_id::text,COALESCE(c.metadata->>'name',c.cinemeta_id),h.kind,h.state,h.observed_at FROM watch_history h JOIN catalog_media c ON c.id=h.media_id WHERE h.user_id=$1 AND ($2::bigint=0 OR h.id<$2) ORDER BY h.id DESC LIMIT 51`, who.ID, before)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.Media, &e.Title, &e.Kind, &e.State, &e.At); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Entries) == 50 {
			n := out.Entries[49].ID
			out.Next = &n
		} else {
			out.Entries = append(out.Entries, e)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// The current panel is deliberately bounded, independent of the paged history.
	rows, err = tx.Query(ctx, `SELECT w.media_id::text,COALESCE(c.metadata->>'name',c.cinemeta_id),w.state,w.source,w.needs_review,w.observed_at,w.revision FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=$1 ORDER BY w.updated_at DESC,w.media_id LIMIT 50`, who.ID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e Current
		if err = rows.Scan(&e.Media, &e.Title, &e.State, &e.Source, &e.Review, &e.At, &e.Revision); err != nil {
			rows.Close()
			return out, err
		}
		out.Current = append(out.Current, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
