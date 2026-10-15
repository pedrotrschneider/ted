package watchstate

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"testing"
)

const mediaID = "33333333-3333-3333-3333-333333333333"

func fixture(t *testing.T) (*Store, accounts.Identity) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	p, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	s := New(p)
	var id int64
	err = p.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('admin','Admin','','admin') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(ctx, `INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES($1,'tt1234567','{"name":"Example"}')`, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	who := accounts.Identity{ID: id, Version: 1}
	return s, who
}

func TestPartialStateRejected(t *testing.T) {
	for _, raw := range []string{`{}`, `{"played":false,"position_ticks":0,"play_count":0}`, `{"played":null,"position_ticks":0,"play_count":0,"last_played":null}`, `{"played":false,"position_ticks":0,"play_count":0,"last_played":null,"extra":0}`} {
		var s State
		if json.Unmarshal([]byte(raw), &s) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var s State
	if err := json.Unmarshal([]byte(`{"played":false,"position_ticks":0,"play_count":0,"last_played":null}`), &s); err != nil {
		t.Fatal(err)
	}
}
