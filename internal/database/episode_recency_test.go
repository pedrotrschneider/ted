package database

import (
	"context"
	"testing"
	"testing/fstest"

	"entertainmentdistrict/internal/testdb"
)

func TestEpisodeRecencyUpgradePreservesSnapshotsBaselinesAndWatchState(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00032_" {
			continue
		}
		data, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		old[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if err = migrate(ctx, url, old); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('retained','Retained','hash','admin');
 INSERT INTO catalog_media(id,kind,cinemeta_id,metadata) VALUES('11111111-1111-4111-8111-111111111111','episode','tt9253284:2:9','{}');
 INSERT INTO stremio_accounts(id,email,auth_key) VALUES('remote','fixture@example.com','fixture-only');
 INSERT INTO stremio_links(id,user_id,account_id) VALUES('22222222-2222-4222-8222-222222222222',1,'remote');
 INSERT INTO stremio_poll_snapshots(account_id,titles,captured_at) VALUES('remote','{"tt9253284":{"entries":[{"id":"tt9253284:2:9"}],"issue":"","record":{"retained":true}}}','2026-10-10');
 INSERT INTO stremio_poll_deliveries(snapshot_id,link_id,after_title) VALUES(1,'22222222-2222-4222-8222-222222222222','tt1234567');
 INSERT INTO stremio_sync_baselines(link_id,title_id,entries,observed_at) VALUES('22222222-2222-4222-8222-222222222222','tt9253284','[{"id":"tt9253284:2:9"}]','2026-10-09');
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES(1,'11111111-1111-4111-8111-111111111111','{"played":true,"position_ticks":123456,"play_count":3,"last_played":null}','StremioImport','2026-10-09');
 INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES(1,'11111111-1111-4111-8111-111111111111','stremio_import','{"played":true}','2026-10-09')`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	const fingerprint = `SELECT md5(concat((SELECT jsonb_agg(to_jsonb(s))::text FROM stremio_poll_snapshots s),(SELECT jsonb_agg(to_jsonb(d))::text FROM stremio_poll_deliveries d),(SELECT jsonb_agg(to_jsonb(w)-'link_id'-'external_sequence')::text FROM watch_current w),(SELECT jsonb_agg(to_jsonb(h)-'link_id'-'event_id'-'write_id')::text FROM watch_history h)))`
	if err = pool.QueryRow(ctx, fingerprint).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = pool.QueryRow(ctx, fingerprint).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed retained state", before, after, err)
	}
	var preserved bool
	if err = pool.QueryRow(ctx, `SELECT entries='[{"id":"tt9253284:2:9"}]' AND observed_at='2026-10-09'::timestamptz AND completion='null'::jsonb AND NOT EXISTS(SELECT 1 FROM stremio_episode_recency) FROM stremio_sync_baselines`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("upgrade changed baseline or fabricated recency", preserved, err)
	}
}
