package database

import (
	"context"
	"entertainmentdistrict/internal/testdb"
	"testing"
	"testing/fstest"
)

func TestServerAddonMigrationPreservesApprovedHistoryAndHandles(t *testing.T) {
	for _, name := range []string{"native_tester", "another_user"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			url := testdb.New(t)
			old := fstest.MapFS{}
			entries, err := migrations.ReadDir("migrations")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() >= "00038_" {
					continue
				}
				raw, e := migrations.ReadFile("migrations/" + entry.Name())
				if e != nil {
					t.Fatal(e)
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
			if _, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES($1,'Native','fixture','admin')`, name); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('fixture','private@example.com','private');INSERT INTO stremio_links(user_id,account_id) SELECT id,'fixture' FROM users;INSERT INTO stremio_addon_collections SELECT id,updated_at,'[]',now(),'' FROM stremio_accounts;
 INSERT INTO native_addon_sources(user_id,link_id,account_id,account_updated_at,media_kind,media_identity,addon_key,descriptor,source) SELECT l.user_id,l.id,l.account_id,a.updated_at,'movie','tt1234567','key','{}','{}' FROM stremio_links l JOIN stremio_accounts a ON a.id=l.account_id`); err != nil {
				t.Fatal(err)
			}
			if err = Migrate(ctx, url); err != nil {
				t.Fatal(err)
			}
			var n int
			want := 0
			if name == "native_tester" {
				want = 1
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM server_addon_accounts WHERE active`).Scan(&n); err != nil || n != want {
				t.Fatal("migration guessed provider", n, err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM native_addon_sources WHERE provider_id IS NOT NULL`).Scan(&n); err != nil || n != want {
				t.Fatal("source handles lost", n, err)
			}
			if _, err = pool.Exec(ctx, `UPDATE stremio_accounts SET auth_key=''`); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM server_addon_accounts WHERE auth_key='private'`).Scan(&n); err != nil || n != want {
				t.Fatal("credential roles coupled", n, err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM stremio_links WHERE retired_at IS NULL`).Scan(&n); err != nil || n != 1 {
				t.Fatal("history link modified", n, err)
			}
		})
	}
}
