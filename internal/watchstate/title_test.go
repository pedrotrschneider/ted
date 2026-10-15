package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"fmt"
	"testing"
)

func publishTitleFixture(t *testing.T, s *Store, user int64, media string) {
	t.Helper()
	_, err := s.pool.Exec(context.Background(), `WITH m AS (INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,'1','t',repeat('a',40),1,'/file',100,'{}',$2) RETURNING id) INSERT INTO streaming_items(movie_id,mapping_id,phase,probe,published) SELECT $1,id,'ready','{}',true FROM m`, media, user)
	if err != nil {
		t.Fatal(err)
	}
}
func readTitle(t *testing.T, s *Store, who accounts.Identity, id string) TitleState {
	t.Helper()
	v, e := s.Title(context.Background(), who, id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestTitleWatchCompetingRequestsAreSerialized(t *testing.T) {
	s, who := fixture(t)
	publishTitleFixture(t, s, who.ID, mediaID)
	ctx := context.Background()
	v := readTitle(t, s, who, "tt1234567")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accounts.MutationLockID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { started <- struct{}{}; done <- s.SetTitle(ctx, who, "tt1234567", v.Version, "", nil, true, "") }()
	}
	<-started
	<-started
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := <-done, <-done
	if !((a == nil && errors.Is(b, ErrConflict)) || (b == nil && errors.Is(a, ErrConflict))) {
		t.Fatal(a, b)
	}
}

func TestTitleWatchWholeShowUnwatchIncludesSpecials(t *testing.T) {
	s, who := fixture(t)
	ctx := context.Background()
	for i, id := range []string{"tt7654321:0:1", "tt7654321:1:1", "tt7654321:2:1"} {
		media := fmt.Sprintf("66666666-6666-6666-6666-%012d", i)
		if _, err := s.pool.Exec(ctx, `INSERT INTO catalog_media(id,cinemeta_id,kind,metadata) VALUES($1,$2,'episode','{}')`, media, id); err != nil {
			t.Fatal(err)
		}
		publishTitleFixture(t, s, who.ID, media)
		if _, err := s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"position_ticks":400,"play_count":2,"last_played":null}','TEDLocal',now())`, who.ID, media); err != nil {
			t.Fatal(err)
		}
	}
	v := readTitle(t, s, who, "tt7654321")
	if err := s.SetTitle(ctx, who, "tt7654321", v.Version, "", nil, false, ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_current WHERE state='{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'::jsonb`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	for _, item := range readTitle(t, s, who, "tt7654321").Items {
		if item.Played || item.Pending {
			t.Fatal("local unwatch was not applied", item)
		}
	}
}
