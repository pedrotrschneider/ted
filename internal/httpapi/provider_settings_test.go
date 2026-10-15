package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"strings"
	"testing"
)

func TestProviderSettingsAuthorizationSecretRetentionAndRestart(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.api.Providers = settings.New(f.pool)
	if err := f.api.Providers.Initialize(ctx, settings.Credentials{RDKey: "legacy-private-key"}); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	admin := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/providers", nil, nil), 401)
	initial := call(t, h, "GET", "/api/admin/providers", nil, admin)
	requireStatus(t, initial, 200)
	if !strings.Contains(initial.Body.String(), `"rd_configured":true`) || strings.Contains(initial.Body.String(), "private-key") {
		t.Fatal("credential absent or disclosed", initial.Body.String())
	}
	requireStatus(t, call(t, h, "PUT", "/api/admin/providers", map[string]any{"revision": 1, "tmdb_token": "private-tmdb", "tvdb_key": "private-tvdb", "tvdb_pin": "private-pin"}, admin), 200)
	requireStatus(t, call(t, h, "PUT", "/api/admin/providers", map[string]any{"revision": 1, "rd_key": "stale-key"}, admin), 409)
	current := call(t, h, "GET", "/api/admin/providers", nil, admin)
	requireStatus(t, current, 200)
	var status settings.Status
	if err := json.Unmarshal(current.Body.Bytes(), &status); err != nil || !status.RD || !status.TMDB || !status.TVDB || status.Revision != 2 {
		t.Fatal(status, err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	member := login(t, h, "member")
	for _, method := range []string{"GET", "PUT"} {
		requireStatus(t, call(t, h, method, "/api/admin/providers", map[string]any{"revision": 2, "rd_key": "unauthorized"}, member), 403)
	}
	requireStatus(t, call(t, h, "PUT", "/api/admin/providers", map[string]any{"revision": 2, "rd_key": ""}, admin), 200)
	restarted := settings.New(f.pool)
	if err := restarted.Initialize(ctx, settings.Credentials{RDKey: "legacy-private-key"}); err != nil {
		t.Fatal(err)
	}
	values, err := restarted.Load(ctx)
	if err != nil || values.RDKey != "" || values.TMDBToken != "private-tmdb" {
		t.Fatal("restart resurrected cleared key or dropped unrelated credentials", err)
	}
	public := call(t, h, "GET", "/api/admin/providers", nil, admin)
	requireStatus(t, public, 200)
	if strings.Contains(public.Body.String()+f.logs.String(), "private-") || strings.Contains(public.Body.String()+f.logs.String(), "stale-key") {
		t.Fatal("saved secret entered response/logs")
	}
}

func TestProviderSettingsInvalidInputDoesNotReplaceSavedKeys(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.api.Providers = settings.New(f.pool)
	if err := f.api.Providers.Initialize(ctx, settings.Credentials{RDKey: "retained-key"}); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	for _, input := range []map[string]any{{"revision": 1, "rd_key": "sensitive\ninvalid"}, {"revision": 1, "unexpected_secret": "sensitive"}, {"revision": 0, "rd_key": ""}} {
		requireStatus(t, call(t, h, "PUT", "/api/admin/providers", input, cookie), 400)
	}
	c, err := f.api.Providers.Load(ctx)
	if err != nil || c.RDKey != "retained-key" || c.Revision != 1 {
		t.Fatal("invalid input changed saved credential", err)
	}
	if strings.Contains(f.logs.String(), "sensitive") {
		t.Fatal("invalid input leaked")
	}
}

func TestMissingProviderSettingsAreFailureNotUnconfigured(t *testing.T) {
	f := setup(t)
	f.api.Providers = settings.New(f.pool)
	f.api.rd = realdebrid.NewStored(f.api.Providers)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/providers", nil, cookie), 503)
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/status", nil, cookie), 503)
}
