package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"entertainmentdistrict/internal/testdb"
	"fmt"
	"testing"
	"testing/fstest"
)

func TestSharedSourceUpgradeRecoversRealStartsAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= "00033_" {
			continue
		}
		raw, e2 := migrations.ReadFile("migrations/" + e.Name())
		if e2 != nil {
			t.Fatal(e2)
		}
		old[e.Name()] = &fstest.MapFile{Data: raw}
	}
	if err = migrate(ctx, url, old); err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'viewer','Viewer','','admin');INSERT INTO stremio_accounts(id,email,auth_key) VALUES('fixture','private@example.com','private');INSERT INTO stremio_links(id,user_id,account_id) VALUES('11111111-0000-4000-8000-000000000001',1,'fixture');INSERT INTO native_token_families(id,user_id,account_version,device_id,device_name,created_at,absolute_expires_at,idle_expires_at) VALUES('11111111-0000-4000-8000-000000000002',1,1,'device','Fixture',now(),now()+interval '90 days',now()+interval '30 days');INSERT INTO catalog_media(id,kind,cinemeta_id,metadata) VALUES('11111111-0000-4000-8000-000000000003','episode','tt1234567:1:1','{}')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		source := fmt.Sprintf("22222222-0000-4000-8000-%012d", i)
		session := fmt.Sprintf("33333333-0000-4000-8000-%012d", i)
		event := fmt.Sprintf("44444444-0000-4000-8000-%012d", i)
		// Fifth session prepared a download only. It must not displace group four.
		if _, err = pool.Exec(ctx, `INSERT INTO native_addon_sources(id,user_id,link_id,account_id,account_updated_at,media_kind,media_identity,addon_key,descriptor,source) SELECT $1,1,'11111111-0000-4000-8000-000000000001','fixture',updated_at,'episode','tt1234567:1:1','private-config-key','{"manifest":{"id":"addon"}}',jsonb_build_object('name','720p','binge_group',$2::text,'url','private-url') FROM stremio_accounts WHERE id='fixture'`, source, fmt.Sprintf("group-%d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO native_playback_sessions(id,user_id,family_id,device_id,media_id,addon_source_id,duration_ms,applied_revision,started) VALUES($1,1,'11111111-0000-4000-8000-000000000002','device','11111111-0000-4000-8000-000000000003',$2,0,0,$3)`, session, source, i < 5); err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			if _, err = pool.Exec(ctx, `INSERT INTO native_watch_events(id,user_id,session_id,sequence,envelope,outcome) VALUES($1,1,$2,1,jsonb_build_object('observed_at',$3::text),'applied')`, event, session, fmt.Sprintf("2026-10-12T10:00:0%dZ", i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	var before string
	if err = pool.QueryRow(ctx, `SELECT md5(string_agg(envelope::text,',' ORDER BY id)) FROM native_watch_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = pool.QueryRow(ctx, `SELECT md5(string_agg(envelope::text,',' ORDER BY id)) FROM native_watch_events`).Scan(&after); err != nil || before != after {
		t.Fatal("source upgrade changed accepted reports", err)
	}
	h := sha256.Sum256([]byte("addon"))
	provider := hex.EncodeToString(h[:])
	h = sha256.Sum256([]byte(provider + "\x00group:group-4"))
	expected := hex.EncodeToString(h[:])
	var count int
	var key, scope string
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM native_recent_sources`).Scan(&count); err != nil || count != 3 {
		t.Fatal("recents not bounded or prepared sessions counted", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT source_key,scope FROM native_recent_sources ORDER BY last_used_at DESC LIMIT 1`).Scan(&key, &scope); err != nil || key != expected || scope != "series:tt1234567" {
		t.Fatal("migration encoding disagrees with shared group contract", key, scope, err)
	}
}
