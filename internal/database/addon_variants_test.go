package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"entertainmentdistrict/internal/testdb"
	"testing"
	"testing/fstest"
)

func TestAddonVariantMigrationPreservesSwitchesAndSharedPreferences(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	old := fstest.MapFS{}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "00034_" {
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
	hash := func(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
	provider := hash("torrentio")
	if _, err = pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('fixture','private@example.com','private');INSERT INTO stremio_addon_collections(account_id,account_updated_at,payload,observed_at) SELECT id,updated_at,'[{"manifest":{"id":"torrentio","name":"Torrentio"},"transportUrl":"private-config"},{"manifest":{"id":"torrentio","name":"Torrentio RD"},"transportUrl":"other-private-config"}]',now() FROM stremio_accounts;INSERT INTO native_favorite_sources(scope,favorite_key) VALUES('series:tt1234567',repeat('f',64))`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO stremio_addon_policies(id,name,enabled,revision) VALUES($1,'Torrentio',false,4),($2,'Not currently installed',true,2)`, provider, hash("missing")); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var revision, count int
	for _, name := range []string{"Torrentio", "Torrentio RD"} {
		if err = pool.QueryRow(ctx, `SELECT enabled,revision FROM stremio_addon_policies WHERE id=$1`, hash(provider+"\x00"+name)).Scan(&enabled, &revision); err != nil || enabled || revision != 4 {
			t.Fatal("existing switch/revision lost for variant", name, err)
		}
	}
	if err = pool.QueryRow(ctx, `SELECT enabled,revision FROM stremio_addon_policies WHERE id=$1`, hash(hash("missing")+"\x00Not currently installed")).Scan(&enabled, &revision); err != nil || !enabled || revision != 2 {
		t.Fatal("uninstalled saved policy lost", err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM native_favorite_sources WHERE favorite_key=repeat('f',64)`).Scan(&count); err != nil || count != 1 {
		t.Fatal("shared favorites changed", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE stremio_addon_policies SET enabled=true WHERE id=$1`, hash(provider+"\x00Torrentio RD")); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT enabled FROM stremio_addon_policies WHERE id=$1`, hash(provider+"\x00Torrentio")).Scan(&enabled); err != nil || enabled {
		t.Fatal("variants still coupled", err)
	}
}
