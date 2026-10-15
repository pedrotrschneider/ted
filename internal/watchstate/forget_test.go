package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"testing"
	"time"
)

func TestForgetLocalIncludesSpecialsAndRetiredCatalogEntries(t *testing.T) {
	s, who := fixture(t)
	ctx := context.Background()
	seedOutgoing(t, s, who.ID)
	for _, id := range []string{"tt7654321:0:1", "tt7654321:1:1", "tt7654321:1:2"} {
		if _, err := s.pool.Exec(ctx, `WITH c AS (INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($2,'episode','{}') RETURNING id) INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true,"position_ticks":123,"play_count":1,"last_played":null}','StremioImport',now() FROM c`, who.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_episode_recency(user_id,media_id,stremio_account,watched_at)
 SELECT user_id,media_id,'test-account',now() FROM watch_current WHERE user_id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_title_catalog(title_id,kind,entries,regular_episode_count) VALUES('tt7654321','series','["tt7654321:1:1"]',1)`); err != nil {
		t.Fatal(err)
	}
	v := readTitle(t, s, who, "tt7654321")
	if len(v.Items) != 1 {
		t.Fatal(v)
	}
	if err := s.SetTitle(ctx, who, "tt7654321", "stale", "", nil, false, "", true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.SetTitle(ctx, accounts.Identity{ID: who.ID, Version: 999}, "tt7654321", v.Version, "", nil, false, "", true); err == nil {
		t.Fatal("revoked session accepted")
	}
	if err := s.SetTitle(ctx, who, "tt7654321", v.Version, "", nil, false, "", true); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_current WHERE user_id=$1 AND source='TEDForget' AND state->>'played'='false' AND state->>'position_ticks'='0'`, who.ID).Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_episode_recency WHERE user_id=$1`, who.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("forgotten completion evidence retained", n, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = EnrollStremio(ctx, tx, who.ID, "remote", "tt7654321:1:1", []byte(`{}`), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err = ObserveStremioEpisodeCompletion(ctx, tx, who.ID, "test-account", "tt7654321:1:1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, who.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("old enrollment restored erased history", n, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_episode_recency WHERE user_id=$1`, who.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("old completion restored erased recency", n, err)
	}
}
