package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"entertainmentdistrict/internal/testdb"
)

func TestMappingBatchMigrationPreservesOperationOrder(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00012_" {
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
	if _, err = pool.Exec(ctx, `INSERT INTO mapping_operations(title_id,kind,actor,request,phase,created_at) VALUES
 ('tt1345836','movie','{}','{"files":[]}','complete','2026-01-02'),
 ('tt1345836','movie','{}','{"files":[]}','failed','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var phase string
	if err = pool.QueryRow(ctx, "SELECT phase FROM mapping_operations ORDER BY sequence DESC LIMIT 1").Scan(&phase); err != nil || phase != "complete" {
		t.Fatal("latest operation changed", phase, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO mapping_operations(title_id,kind,actor,request) VALUES('tt1345836','movie','{}','{"files":[]}')`); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT phase FROM mapping_operations ORDER BY sequence DESC LIMIT 1").Scan(&phase); err != nil || phase != "queued" {
		t.Fatal("new operation not latest", phase, err)
	}
}

func TestRetirementMigrationRefusesPendingWorkAndPreservesNativeState(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00024_" {
			continue
		}
		data, e := migrations.ReadFile("migrations/" + entry.Name())
		if e != nil {
			t.Fatal(e)
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
	if _, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('retained','Retained','hash','admin');
 INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES('11111111-1111-4111-8111-111111111111','tt1345836','{"name":"Retained"}');
 INSERT INTO media_mappings(id,movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','123','T1',repeat('a',40),1,'/movie.mkv',1000,'{}',1);
 INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES('11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','ready',true,'{}');
 INSERT INTO mapping_submissions(id,title_id,kind,actor,request,batch_size) VALUES('33333333-3333-4333-8333-333333333333','tt1345836','movie','{}','{"files":[{}]}',200);
 INSERT INTO mapping_operations(title_id,kind,actor,request,phase,submission_id) VALUES('tt1345836','movie','{}','{"files":[{}]}','publishing','33333333-3333-4333-8333-333333333333');
 INSERT INTO library_removals(title_id,kind,phase,snapshot) VALUES('tt1245698','series','snapshot','[{"retained":true}]');
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES(1,'11111111-1111-4111-8111-111111111111','{"played":true,"position_ticks":456}','TED',now())`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err == nil {
		t.Fatal("retired integration with pending explicit work")
	}
	if _, err = pool.Exec(ctx, `UPDATE mapping_operations SET phase='failed'; UPDATE library_removals SET phase='complete'`); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT
 to_regclass('watch_links') IS NULL AND
 to_regclass('watch_writes') IS NULL AND
 EXISTS(SELECT 1 FROM media_mappings WHERE id='22222222-2222-4222-8222-222222222222') AND
 EXISTS(SELECT 1 FROM streaming_items WHERE published AND phase='ready') AND
 EXISTS(SELECT 1 FROM watch_current WHERE state->>'position_ticks'='456')`).Scan(&intact); err != nil || !intact {
		t.Fatal("upgrade changed durable legacy state", intact, err)
	}
}

func TestMigrationConcurrentAndRepeated(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- Migrate(ctx, url) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, "INSERT INTO users(username,password_hash,role,display_name) VALUES ('retained','hash','admin','Retained')"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username='retained'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("retained user count=%d err=%v", count, err)
	}
}
func TestMigrationLockCancellation(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", MigrationLockID); err != nil {
		t.Fatal(err)
	}
	op, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	err = Migrate(op, url)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration must stop at held lock: %v", err)
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", MigrationLockID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
}
func TestFailedMigrationRollsBackAndPreservesExistingData(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, "CREATE TABLE retained(value text); INSERT INTO retained VALUES ('keep')"); err != nil {
		t.Fatal(err)
	}
	bad := fstest.MapFS{"00001_bad.sql": {Data: []byte("-- +goose Up\nCREATE TABLE unfinished(id integer);\nSELECT 1/0;\n")}}
	if err = migrate(ctx, url, bad); err == nil {
		t.Fatal("broken migration passed")
	}
	var value string
	var absent bool
	if err = pool.QueryRow(ctx, "SELECT value FROM retained").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("existing data lost: %v", err)
	}
	if err = pool.QueryRow(ctx, "SELECT to_regclass('unfinished') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("partial migration leaked: %v", err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayNameMigrationPreservesAccountsAndSessions(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old, err := migrations.ReadFile("migrations/00001_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, url, fstest.MapFS{"00001_accounts.sql": {Data: old}}); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `INSERT INTO users(username,password_hash,role,session_version) VALUES ('existing','preserved-hash','admin',9);
 INSERT INTO sessions(token,data,expiry) VALUES ('token','data',now()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var name, hash string
	var version, count int
	if err = pool.QueryRow(ctx, "SELECT display_name,password_hash,session_version FROM users WHERE username='existing'").Scan(&name, &hash, &version); err != nil {
		t.Fatal(err)
	}
	if name != "existing" || hash != "preserved-hash" || version != 9 {
		t.Fatal("migration changed identity or credentials")
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE token='token'").Scan(&count); err != nil || count != 1 {
		t.Fatal("session lost", err)
	}
}

func TestSeriesMigrationPreservesPublishedMovieAndCheckpoints(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	for _, name := range []string{"00001_accounts.sql", "00002_display_names.sql", "00003_movie_mappings.sql", "00004_streaming.sql", "00005_local_files.sql", "00006_download_metrics.sql"} {
		data, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: data}
	}
	if err := migrate(ctx, url, old); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `INSERT INTO users(username,password_hash,role,display_name) VALUES('retained','hash','admin','Retained');
 INSERT INTO catalog_movies(id,cinemeta_id,metadata) VALUES('11111111-1111-4111-8111-111111111111','tt1345836','{"name":"Retained"}');
 INSERT INTO movie_mappings(id,movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','123','T1',repeat('a',40),1,'/movie.mkv',1000,'{}',1);
 INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES('11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','ready',true,'{}');
 INSERT INTO local_files(movie_id,generation,job_id,phase,completed_bytes,total_bytes) VALUES('11111111-1111-4111-8111-111111111111',7,8,'downloading',500,1000);
 INSERT INTO streaming_links VALUES('22222222-2222-4222-8222-222222222222','scope','http://example.invalid/media',now()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var id, kind string
	var published bool
	var completed, generation, job int64
	if err = pool.QueryRow(ctx, `SELECT c.id::text,c.kind,s.published,l.completed_bytes,l.generation,l.job_id FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id JOIN streaming_items s ON s.movie_id=c.id JOIN local_files l ON l.movie_id=c.id JOIN streaming_links k ON k.mapping_id=m.id WHERE c.cinemeta_id='tt1345836'`).Scan(&id, &kind, &published, &completed, &generation, &job); err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" || kind != "movie" || !published || completed != 500 || generation != 7 || job != 8 {
		t.Fatal("migration changed movie identity or interrupted work")
	}
}

func TestFullStremioAccountMigrationRetainsHistoryAndReenrollsExclusions(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00020_" {
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
	_, err = pool.Exec(ctx, `
 INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'owner','Owner','','admin');
 INSERT INTO stremio_accounts(id,email,auth_key) VALUES('remote','test@example.com','private');
 INSERT INTO stremio_links(id,user_id,account_id) VALUES('00000000-0000-0000-0000-000000000001',1,'remote');
 INSERT INTO stremio_previews(id,link_id,phase,captured_at) VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','ready',now());
 INSERT INTO stremio_imports(preview_id,link_id,phase) VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','complete');
 INSERT INTO stremio_import_exclusions(user_id,account_id,entry_id,preview_id,excluded) VALUES(1,'remote','tt1234567','00000000-0000-0000-0000-000000000002',true);
 INSERT INTO stremio_sync_links(link_id) VALUES('00000000-0000-0000-0000-000000000001');
 INSERT INTO stremio_sync_baselines(link_id,title_id,entries,observed_at) VALUES('00000000-0000-0000-0000-000000000001','tt1234567','[{"id":"tt1234567"}]',now()),('00000000-0000-0000-0000-000000000001','tt1234568','[{"id":"tt1234568"}]',now());
 INSERT INTO catalog_media(cinemeta_id,metadata) VALUES('tt1234568','{}');
 INSERT INTO watch_history(user_id,media_id,kind,state,observed_at,stremio_account,stremio_preview) SELECT 1,id,'stremio_import','{"played":true}',now(),'remote','00000000-0000-0000-0000-000000000002' FROM catalog_media;
 `)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var excluded, retained, history int
	if err = pool.QueryRow(ctx, `SELECT jsonb_array_length(entries) FROM stremio_sync_baselines WHERE title_id='tt1234567'`).Scan(&excluded); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT jsonb_array_length(entries) FROM stremio_sync_baselines WHERE title_id='tt1234568'`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE kind='stremio_import' AND state->>'played'='true'`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if excluded != 0 || retained != 1 || history != 1 {
		t.Fatal(excluded, retained, history)
	}
}
