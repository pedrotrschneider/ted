package watchstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
)

func TestLocalHistoryEditPreservesDatesAndRejectsStaleRevision(t *testing.T) {
	s, who := fixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":456,"play_count":0,"last_played":null}','TED',now())`, who.ID, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 1, Action: "watched", DateMode: "undated"}); err != nil {
		t.Fatal(err)
	}
	saved := current(t, s, who)
	state, revision, review := saved.State, saved.Revision, saved.Review
	if !state.Played || state.LastPlayed != nil || state.Position != 0 || revision != 2 || review {
		t.Fatal(state, revision, review)
	}
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 1, Action: "unwatched"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 2, Action: "watched", DateMode: "now"}); err != nil {
		t.Fatal(err)
	}
	saved = current(t, s, who)
	state, revision = saved.State, saved.Revision
	if state.LastPlayed == nil || state.LastPlayed.Before(before) || revision != 3 {
		t.Fatal(state, revision)
	}
	var history int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM watch_history WHERE user_id=$1 AND media_id=$2", who.ID, mediaID).Scan(&history); err != nil || history != 2 {
		t.Fatal(history, err)
	}
	who.Version++
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 3, Action: "unwatched"}); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestLocalHistoryEditQueuesOnlyWatchedFlagChanges(t *testing.T) {
	s, who := stremioFixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":456,"play_count":0,"last_played":null}','TED',now())`, who.ID, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 1, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM stremio_outgoing").Scan(&n); err != nil || n != 0 {
		t.Fatal("position-only edit queued external flag", n, err)
	}
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 2, Action: "watched"}); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM stremio_outgoing WHERE user_id=$1 AND played", who.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	saved := current(t, s, who)
	state, revision := saved.State, saved.Revision
	if !state.Played || revision != 3 {
		t.Fatal(state, revision)
	}
}
