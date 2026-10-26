package database

import (
	"context"
	"testing"
	"testing/fstest"

	"entertainmentdistrict/internal/testdb"
)

func TestSeriesProviderMigrationRetainsCanonicalSchedulesAndStableViewing(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00048_" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		old[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	if err = migrate(ctx, dsn, old); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `
 INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'viewer','Viewer','retained','admin');
 INSERT INTO catalog_episode_schedules(title_id,episodes,observed_at,fetched_at) VALUES('tt1234567','[{"id":"tt1234567:21:1","season":21,"number":1,"name":"Retained"}]','2026-10-01','2026-10-01');
 INSERT INTO catalog_media(id,cinemeta_id,kind,metadata) VALUES('11111111-1111-4111-8111-111111111111','tt1234567:21:1','episode','{}');
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES(1,'11111111-1111-4111-8111-111111111111','{"played":true,"play_count":2}','TEDLocal','2026-10-02');
 INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT user_id,media_id,'ted_watched',state,observed_at FROM watch_current;
 `); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal("repeated startup", err)
	}
	var preserved bool
	if err = pool.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM catalog_episode_schedules WHERE canonical_episodes=episodes AND identity_version=0 AND fetched_at='2026-10-01' AND episodes->0->>'id'='tt1234567:21:1')
 AND ted_episode_season('tt1234567:21:1')=21 AND ted_episode_number('tt1234567:21:1')=1
 AND EXISTS(SELECT 1 FROM watch_current WHERE state->>'play_count'='2')
 AND EXISTS(SELECT 1 FROM watch_history WHERE kind='ted_watched' AND observed_at='2026-10-02')
 AND NOT EXISTS(SELECT 1 FROM metadata_series_providers)`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("upgrade changed schedules or viewing", preserved, err)
	}
}
