package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSetupLifecycle(t *testing.T) {
	f := setup(t)
	if _, err := f.pool.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	r := call(t, h, "GET", "/api/auth/setup", nil, nil)
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"setup_required":true`) {
		t.Fatal(r.Body.String())
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/setup", map[string]string{"username": "!invalid", "password": "test"}, nil), 400)
	request := httptest.NewRequest("POST", "https://ted.example/api/auth/setup", strings.NewReader(`{"username":"attacker","password":"test"}`))
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	requireStatus(t, response, 403)
	requireStatus(t, call(t, h, "POST", "/api/auth/setup", map[string]string{"username": "owner", "password": testPassword}, nil), 201)
	r = call(t, h, "GET", "/api/auth/setup", nil, nil)
	if !strings.Contains(r.Body.String(), `"setup_required":false`) {
		t.Fatal(r.Body.String())
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/setup", map[string]string{"username": "second", "password": testPassword}, nil), 409)
	cookie := login(t, h, "owner")
	r = call(t, h, "GET", "/api/auth/me", nil, cookie)
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), `"role":"admin"`) {
		t.Fatal(r.Body.String())
	}
}
