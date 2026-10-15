package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/stremio"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type copyHTTPRemote struct{ logins atomic.Int32 }

func (r *copyHTTPRemote) Login(_ context.Context, email, _ string) (stremio.Session, error) {
	r.logins.Add(1)
	id := "source"
	if email == "destination@example.com" {
		id = "destination"
	}
	return stremio.Session{ID: id, Email: email, Key: "private-copy-key-" + id}, nil
}
func (*copyHTTPRemote) Snapshot(context.Context, string) ([]stremio.Record, error) {
	return []stremio.Record{}, nil
}

func TestStremioCopyHTTPAdminCredentialsAndCancellation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var schema string
	if err := f.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(f.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	remote := &copyHTTPRemote{}
	f.api.stremio = stremio.NewStore(f.pool, remote, cinemeta.New())
	h := f.api.Handler()
	input := map[string]any{"source": map[string]string{"email": "source@example.com", "password": "private-source-password"}, "destination": map[string]string{"email": "destination@example.com", "password": "private-destination-password"}}
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy", input, nil), 401)
	if _, err = f.service.Create(ctx, accounts.Identity{ID: f.admin.ID, Version: 1}, "member", "Member", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	member := login(t, h, "member")
	for _, method := range []string{"GET", "POST"} {
		requireStatus(t, call(t, h, method, "/api/admin/stremio-copy", input, member), 403)
	}
	if remote.logins.Load() != 0 {
		t.Fatal("unauthorized credentials reached Stremio")
	}
	cookie := login(t, h, "admin")
	started := call(t, h, "POST", "/api/admin/stremio-copy", input, cookie)
	requireStatus(t, started, 202)
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(started.Body.Bytes(), &result) != nil || result.ID == "" {
		t.Fatal("no durable copy id")
	}
	status := call(t, h, "GET", "/api/admin/stremio-copy", nil, cookie)
	requireStatus(t, status, 200)
	if strings.Contains(status.Body.String()+f.logs.String(), "private-") || !strings.Contains(status.Body.String(), "source@example.com") {
		t.Fatal("unsafe or absent copy status")
	}
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy/"+result.ID+"/cancel", nil, member), 403)
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy/"+result.ID+"/cancel", nil, cookie), 202)
	var keys string
	if err = f.pool.QueryRow(ctx, `SELECT source_key||destination_key FROM temporary_stremio_copies WHERE id=$1`, result.ID).Scan(&keys); err != nil || keys != "" {
		t.Fatal("cancel retained credentials", err)
	}
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy/"+result.ID+"/retry", nil, cookie), 409)
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy/not-an-id/retry", nil, cookie), 400)
	input["destination"] = map[string]string{"email": "same@example.com", "password": "private-password"}
	requireStatus(t, call(t, h, "POST", "/api/admin/stremio-copy", input, cookie), 400)
}
