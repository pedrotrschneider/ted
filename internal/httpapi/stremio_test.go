package httpapi

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/stremio"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"strings"
	"testing"
)

type stremioRemote struct{}

func (stremioRemote) Login(context.Context, string, string) (stremio.Session, error) {
	return stremio.Session{ID: "test-remote", Email: "test@example.com", Key: "private-stremio-key"}, nil
}
func (stremioRemote) Snapshot(context.Context, string) ([]stremio.Record, error) {
	return []stremio.Record{}, nil
}
func TestStremioHTTPAuthenticationAndCredentialBoundary(t *testing.T) {
	f := setup(t)
	var schema string
	if err := f.pool.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(f.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(context.Background(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	f.api.stremio = stremio.NewStore(f.pool, stremioRemote{}, cinemeta.New())
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/stremio", nil, nil), 401)
	requireStatus(t, call(t, h, "POST", "/api/stremio/link", map[string]string{"email": "test@example.com", "password": "private-password"}, cookie), 200)
	r := call(t, h, "GET", "/api/stremio", nil, cookie)
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), "test@example.com") || strings.Contains(r.Body.String()+f.logs.String(), "private-") {
		t.Fatal("credentials leaked or status absent")
	}
	requireStatus(t, call(t, h, "POST", "/api/stremio/preview", map[string]any{}, cookie), 404)
	requireStatus(t, call(t, h, "DELETE", "/api/stremio/link", nil, cookie), 200)
	r = call(t, h, "GET", "/api/stremio", nil, cookie)
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"linked":false`) {
		t.Fatal(r.Body.String())
	}
}
