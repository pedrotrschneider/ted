package database

import (
	"context"
	"testing"
	"testing/fstest"

	"entertainmentdistrict/internal/testdb"

	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func TestPlaybackCacheRetirementPreservesDurableStateAndUnrelatedJobs(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00047_" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		old[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	if err = migrate(ctx, url, old); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var schema string
	if err = pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	river, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = river.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
 INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'viewer','Viewer','retained','admin');
 INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES('11111111-1111-4111-8111-111111111111','tt1234567','{"name":"Retained"}');
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at,duration_ms) VALUES(1,'11111111-1111-4111-8111-111111111111','{"played":false,"position_ticks":600000000}','TEDNativePlayback',now(),100000);
 INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT user_id,media_id,'playback_stopped',state,observed_at FROM watch_current;
 INSERT INTO account_title_preferences(user_id,kind,title_id,watchlist,rating,metadata) VALUES(1,'movie','tt1234567',true,7,'{}');
 INSERT INTO playback_cache_entries(cache_key,file_bytes) VALUES(repeat('a',64),1000);
 INSERT INTO playback_cache_chunks(cache_key,chunk_index,bytes,ready) VALUES(repeat('a',64),0,1000,true);
 INSERT INTO river_job(kind,args,queue,state,max_attempts,priority,finalized_at) VALUES
 ('playback_cache_fill','{}','default','available',25,1,NULL),
 ('playback_cache_index','{}','default','running',25,1,NULL),
 ('playback_cache_cleanup','{}','default','retryable',25,1,NULL),
 ('playback_cache_fill','{}','default','completed',25,1,now()),
 ('metadata_fetch','{}','default','available',25,1,NULL)`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal("repeated startup", err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT
 to_regclass('playback_cache_entries') IS NULL AND
 to_regclass('playback_cache_chunks') IS NULL AND
 to_regprocedure('playback_cache_bytes_changed()') IS NULL AND
 EXISTS(SELECT 1 FROM users WHERE password_hash='retained') AND
 EXISTS(SELECT 1 FROM watch_current WHERE state->>'position_ticks'='600000000' AND duration_ms=100000) AND
 EXISTS(SELECT 1 FROM watch_history WHERE kind='playback_stopped') AND
 EXISTS(SELECT 1 FROM account_title_preferences WHERE watchlist AND rating=7) AND
 (SELECT count(*) FROM river_job WHERE state='cancelled' AND finalized_at IS NOT NULL)=3 AND
 (SELECT count(*) FROM river_job WHERE state='completed')=1 AND
 EXISTS(SELECT 1 FROM river_job WHERE kind='metadata_fetch' AND state='available')`).Scan(&intact); err != nil || !intact {
		t.Fatal("cache retirement lost state or unrelated jobs", intact, err)
	}
}
