package realdebrid

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/settings"
	"entertainmentdistrict/internal/testdb"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStoredCredentialSnapshotRotationAndClear(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor accounts.Identity
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('admin','Admin','unused','admin') RETURNING id,session_version`).Scan(&actor.ID, &actor.Version); err != nil {
		t.Fatal(err)
	}
	store := settings.New(pool)
	if err = store.Initialize(ctx, settings.Credentials{RDKey: "old-private-key"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer old-private-key":
			w.Write([]byte(`{"id":1}`))
		case "Bearer new-private-key":
			w.Write([]byte(`{"id":2}`))
		default:
			t.Error("incorrect credential")
			w.WriteHeader(401)
		}
	}))
	defer server.Close()
	c := NewStored(store)
	c.base = server.URL + "/"
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := "new-private-key"
	if _, err = store.Update(ctx, actor, settings.Patch{Revision: 1, RDKey: &key}); err != nil {
		t.Fatal(err)
	}
	newer, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if old.CredentialScope() == newer.CredentialScope() {
		t.Fatal("key rotation reused another credential's cache scope")
	}
	id, err := old.AccountID(ctx)
	if err != nil || id != "1" {
		t.Fatal("in-flight operation switched credential", id, err)
	}
	id, err = c.AccountID(ctx)
	if err != nil || id != "2" {
		t.Fatal("new request did not apply saved key", id, err)
	}
	key = ""
	if _, err = store.Update(ctx, actor, settings.Patch{Revision: 2, RDKey: &key}); err != nil {
		t.Fatal(err)
	}
	if c.Configured() {
		t.Fatal("cleared credential remained active")
	}
	if _, err = c.AccountID(ctx); err != ErrNotConfigured {
		t.Fatal("clear made an unauthenticated provider request", err)
	}
}
