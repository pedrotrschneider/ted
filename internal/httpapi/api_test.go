package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testPassword = "test-password-for-TED"

type fixture struct {
	pool    *pgxpool.Pool
	service *accounts.Service
	api     *API
	admin   accounts.User
	logs    bytes.Buffer
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	passwords, err := accounts.NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: pool, service: accounts.New(pool, passwords)}
	f.admin, err = f.service.Bootstrap(ctx, "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	f.api = New(pool, f.service, true, slog.New(slog.NewJSONHandler(&f.logs, nil)), nil)
	return f
}
func call(t *testing.T, h http.Handler, method, path string, input any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "https://ted.example"+path, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func requireStatus(t *testing.T, r *httptest.ResponseRecorder, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("status %d want %d: %s", r.Code, want, r.Body.String())
	}
}
func login(t *testing.T, h http.Handler, name string) *http.Cookie {
	t.Helper()
	r := call(t, h, "POST", "/api/auth/login", map[string]string{"username": name, "password": testPassword}, nil)
	requireStatus(t, r, 200)
	for _, c := range r.Result().Cookies() {
		if c.Name == "ted_session" {
			return c
		}
	}
	t.Fatal("missing session cookie")
	return nil
}
func TestSessionsSurviveServerRecreationAndLogoutRevokesStaleWrites(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe cookie attributes: %+v", cookie)
	}
	var raw int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions WHERE token=$1", cookie.Value).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("raw bearer token stored: %v", err)
	}
	// A second server instance reads the persisted session without sharing in-memory state.
	restarted := New(f.pool, f.service, true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	requireStatus(t, call(t, restarted.Handler(), "GET", "/api/auth/me", nil, cookie), 200)
	// Hold an already-loaded request's session, revoke it, then let its delayed save complete.
	stale, err := f.api.sessions.Load(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/logout", nil, cookie), 200)
	if _, _, err = f.api.sessions.Commit(stale); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, restarted.Handler(), "GET", "/api/auth/me", nil, cookie), 401)
}
func TestAuthorizationDisableAndPasswordChange(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	input := map[string]string{"username": "viewer", "password": testPassword}
	requireStatus(t, call(t, h, "POST", "/api/admin/users", input, nil), 401)
	r := call(t, h, "POST", "/api/admin/users", input, admin)
	requireStatus(t, r, 201)
	var viewer accounts.User
	if err := json.Unmarshal(r.Body.Bytes(), &viewer); err != nil {
		t.Fatal(err)
	}
	userCookie := login(t, h, "viewer")
	requireStatus(t, call(t, h, "POST", "/api/admin/users", map[string]string{"username": "intruder", "password": testPassword}, userCookie), 403)
	if err := f.service.SetDisabled(context.Background(), identity(f.admin), viewer.ID, true); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, userCookie), 401)
	requireStatus(t, call(t, h, "POST", "/api/auth/login", input, nil), 401)
	if err := f.service.SetDisabled(context.Background(), identity(f.admin), viewer.ID, false); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, userCookie), 401)
	userCookie = login(t, h, "viewer")
	requireStatus(t, call(t, h, "POST", "/api/auth/password", map[string]string{"current_password": testPassword, "new_password": "replacement-password"}, userCookie), 200)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, userCookie), 401)
	requireStatus(t, call(t, h, "POST", "/api/auth/login", input, nil), 401)
	requireStatus(t, call(t, h, "POST", "/api/auth/login", map[string]string{"username": "viewer", "password": "replacement-password"}, nil), 200)
	requireStatus(t, call(t, h, "PUT", "/api/admin/users/1/disabled", map[string]bool{"disabled": true}, admin), 409)
}
func TestSessionSaveFailureDiscardsSuccessAndRedactsError(t *testing.T) {
	f := setup(t)
	_, err := f.pool.Exec(context.Background(), `CREATE FUNCTION reject_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret-diagnostic-do-not-expose'; END $$;
 CREATE TRIGGER reject_session BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION reject_session()`)
	if err != nil {
		t.Fatal(err)
	}
	r := call(t, f.api.Handler(), "POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil)
	requireStatus(t, r, 503)
	if len(r.Result().Cookies()) != 0 || strings.Contains(r.Body.String(), "username") || strings.Contains(r.Body.String(), "secret-diagnostic") || strings.Contains(f.logs.String(), "secret-diagnostic") || strings.Contains(f.logs.String(), testPassword) {
		t.Fatal("session failure published success, credentials, or database detail")
	}
	if !strings.Contains(f.logs.String(), "P0001") {
		t.Fatal("failure lacks sanitized SQLSTATE diagnostics")
	}
}
func TestOriginChecksLoginThrottleAndMalformedHash(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	req := httptest.NewRequest("POST", "https://ted.example/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-for-TED"}`))
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	requireStatus(t, rec, 403)
	for range 10 {
		requireStatus(t, call(t, h, "POST", "/api/auth/login", map[string]string{"username": "absent", "password": "wrong"}, nil), 401)
	}
	// Recreating the API does not reset durable rate limits.
	next := New(f.pool, f.service, true, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	requireStatus(t, call(t, next.Handler(), "POST", "/api/auth/login", map[string]string{"username": "absent", "password": "wrong"}, nil), 429)
	if _, err := f.pool.Exec(context.Background(), "UPDATE users SET password_hash='malformed' WHERE username='admin'"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil), 503)
	var hash string
	if err := f.pool.QueryRow(context.Background(), "SELECT password_hash FROM users WHERE username='admin'").Scan(&hash); err != nil || hash != "malformed" {
		t.Fatalf("malformed persistent state changed: %v", err)
	}
}
func TestDatabaseFailureIsNotUnauthenticatedOrHealthy(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	f.pool.Close()
	requireStatus(t, call(t, h, "GET", "/health/live", nil, nil), 200)
	requireStatus(t, call(t, h, "GET", "/health/ready", nil, nil), 503)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, cookie), 503)
}

func TestRemovedAdminJobRoutes(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/jobs", nil, cookie), 404)
	requireStatus(t, call(t, h, "POST", "/api/admin/jobs/orphan_cleanup/run", map[string]string{}, cookie), 404)
	requireStatus(t, call(t, h, "POST", "/api/admin/jobs/TEDMetadataRestore/run", map[string]string{}, cookie), 404)
}
