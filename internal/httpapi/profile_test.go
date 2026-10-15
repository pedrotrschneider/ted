package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"entertainmentdistrict/internal/accounts"
)

func TestDisplayNamesAndProfileAuthorization(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	r := call(t, h, "POST", "/api/admin/users", map[string]string{"username": "viewer", "display_name": "  María 猫  ", "password": ""}, admin)
	requireStatus(t, r, 201)
	var created accounts.User
	if err := json.Unmarshal(r.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.DisplayName != "María 猫" || created.Username != "viewer" {
		t.Fatalf("unexpected account: %+v", created)
	}
	signed := call(t, h, "POST", "/api/auth/login", map[string]string{"username": "viewer", "password": ""}, nil)
	requireStatus(t, signed, 200)
	cookie := signed.Result().Cookies()[0]
	requireStatus(t, call(t, h, "PUT", "/api/auth/profile", map[string]string{"display_name": "New name"}, nil), 401)
	requireStatus(t, call(t, h, "PUT", "/api/auth/profile", map[string]any{"display_name": "New name", "id": f.admin.ID}, cookie), 400)
	requireStatus(t, call(t, h, "PUT", "/api/auth/profile", map[string]string{"display_name": "New name"}, cookie), 200)
	me := call(t, h, "GET", "/api/auth/me", nil, cookie)
	requireStatus(t, me, 200)
	var updated accounts.User
	if err := json.Unmarshal(me.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Username != created.Username || updated.DisplayName != "New name" {
		t.Fatal("profile changed identity")
	}
	for _, name := range []string{"  ", "line\nbreak", strings.Repeat("x", 101)} {
		requireStatus(t, call(t, h, "PUT", "/api/auth/profile", map[string]string{"display_name": name}, cookie), 400)
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/password", map[string]string{"current_password": "", "new_password": "x"}, cookie), 200)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, cookie), 401)
}
func TestConfiguredMinimumAppliesToNewPasswordsOnly(t *testing.T) {
	f := setup(t)
	p, err := accounts.NewPasswords(24)
	if err != nil {
		t.Fatal(err)
	}
	strict := New(f.pool, accounts.New(f.pool, p), true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	h := strict.Handler()
	cookie := login(t, h, "admin")
	policy := call(t, h, "GET", "/api/auth/policy", nil, nil)
	requireStatus(t, policy, 200)
	if !strings.Contains(policy.Body.String(), `"min_password_length":24`) {
		t.Fatal("policy not exposed")
	}
	requireStatus(t, call(t, h, "POST", "/api/admin/users", map[string]string{"username": "short", "password": "x"}, cookie), 400)
	requireStatus(t, call(t, h, "POST", "/api/auth/password", map[string]string{"current_password": testPassword, "new_password": ""}, cookie), 400)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, cookie), 200)
}
