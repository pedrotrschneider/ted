package httpapi

import (
	"context"
	"encoding/json"
	tedversion "entertainmentdistrict"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entertainmentdistrict/internal/accounts"
)

func nativeCall(t *testing.T, h http.Handler, method, path, token string, input any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body string
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = string(b)
	}
	r := httptest.NewRequest(method, "https://ted.example"+path, strings.NewReader(body))
	r.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	r.Header.Set("X-Teddy-Compatibility-Version", tedversion.Compatibility())
	r.RemoteAddr = "127.0.0.1:54321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
		t.Fatal("native response cached or modified browser session")
	}
	return w
}

func nativeHTTPLogin(t *testing.T, h http.Handler) accounts.NativeTokens {
	t.Helper()
	r := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "admin", "password": testPassword, "device_id": "native-device", "device_name": "Test player"}, nil)
	requireStatus(t, r, 200)
	var pair accounts.NativeTokens
	if err := json.Unmarshal(r.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if pair.TokenType != "Bearer" || pair.ExpiresIn != 600 || pair.User.ID < 1 || pair.FamilyID == "" {
		t.Fatal("incomplete native login response")
	}
	return pair
}

func TestNativeHTTPRotationIsolationAndLogoutScopes(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	browser := login(t, h, "admin")
	a, b := nativeHTTPLogin(t, h), nativeHTTPLogin(t, h)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", a.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", "", nil, browser), 401)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", a.AccessToken, nil, browser), 401)
	// Browser APIs refuse bearer credentials rather than ignore a competing user.
	r := httptest.NewRequest("GET", "https://ted.example/api/auth/me", nil)
	r.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	r.AddCookie(browser)
	r.Header.Set("Authorization", "Bearer "+a.AccessToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireStatus(t, w, 401)
	rotated := nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": a.RefreshToken}, nil)
	requireStatus(t, rotated, 200)
	var next accounts.NativeTokens
	if err := json.Unmarshal(rotated.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.FamilyID != a.FamilyID {
		t.Fatal("HTTP refresh changed family")
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/logout", next.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", next.AccessToken, nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", b.AccessToken, nil, nil), 200)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, browser), 200)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/logout-all", b.AccessToken, nil, nil), 200)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, browser), 401)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", b.AccessToken, nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": b.RefreshToken}, nil), 401)
}

func TestNativeHTTPPurposeHeaderOriginAndSharedLoginLimits(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	for _, request := range []struct {
		method, path, token string
		input               any
	}{
		{"GET", "/api/client/v1/auth/me?access_token=" + pair.AccessToken, "", nil},
		{"GET", "/api/client/v1/auth/me", pair.RefreshToken, nil},
		{"POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.AccessToken}},
		{"POST", "/api/client/v1/auth/refresh", pair.AccessToken, map[string]string{"refresh_token": pair.RefreshToken}},
	} {
		r := nativeCall(t, h, request.method, request.path, request.token, request.input, nil)
		requireStatus(t, r, 401)
		if r.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("missing bearer challenge")
		}
	}
	r := httptest.NewRequest("GET", "https://ted.example/api/client/v1/auth/me", nil)
	r.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	r.Header.Add("Authorization", "Bearer "+pair.AccessToken)
	r.Header.Add("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireStatus(t, w, 401)
	r = httptest.NewRequest("POST", "https://ted.example/api/client/v1/auth/refresh", strings.NewReader(`{"refresh_token":"`+pair.RefreshToken+`"}`))
	r.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	r.Header.Set("Origin", "https://attacker.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireStatus(t, w, 403)
	// Refused malformed/origin/header attempts have not consumed the refresh token.
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken, "family_id": pair.FamilyID}, nil), 400)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil), 200)
	for range 10 {
		requireStatus(t, call(t, h, "POST", "/api/auth/login", map[string]string{"username": "absent", "password": "wrong"}, nil), 401)
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "absent", "password": "wrong", "device_id": "device", "device_name": "Player"}, nil), 429)
	if strings.Contains(f.logs.String(), pair.RefreshToken) || strings.Contains(f.logs.String(), pair.AccessToken) || strings.Contains(f.logs.String(), testPassword) {
		t.Fatal("credential logged")
	}
}

func TestNativeHTTPCommitFailureRedactionAndUnavailableDatabase(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	_, err := f.pool.Exec(context.Background(), `CREATE FUNCTION fail_native_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'secret-native-diagnostic'; END $$;
 CREATE CONSTRAINT TRIGGER fail_native_commit AFTER INSERT ON native_access_tokens DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_native_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	r := nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil)
	requireStatus(t, r, 503)
	if strings.Contains(r.Body.String(), "access_token") || strings.Contains(r.Body.String(), "secret-native") || strings.Contains(f.logs.String(), "secret-native") || strings.Contains(f.logs.String(), pair.RefreshToken) {
		t.Fatal("failed commit exposed success or secrets")
	}
	if !strings.Contains(f.logs.String(), "P0001") {
		t.Fatal("missing sanitized database diagnostics")
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil), 200)
	f.pool.Close()
	r = nativeCall(t, h, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil)
	requireStatus(t, r, 503)
	if r.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("database outage treated as invalid credentials")
	}
}

// Browser authentication must never replace a native refresh family, including
// when a browser session is already signed into another local account.
func TestBrowserLoginPreservesNativeSessionAndRefresh(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	if _, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	browser := login(t, h, "viewer")
	response := call(t, h, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, browser)
	requireStatus(t, response, 200)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil), 200)
	rotated := nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil)
	requireStatus(t, rotated, 200)
	var next accounts.NativeTokens
	if err := json.Unmarshal(rotated.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.FamilyID != pair.FamilyID {
		t.Fatal("browser login replaced native family")
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/auth/me", next.AccessToken, nil, nil), 200)
}

func TestBrowserOnlyLogoutPreservesOtherLoginsAndFencesDelayedSessionSave(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	browser, other := login(t, h, "admin"), login(t, h, "admin")
	stale, err := f.api.sessions.Load(context.Background(), browser.Value)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "POST", "/api/auth/logout-browser", nil, browser), 200)
	// Recreate the old session deliberately, simulating a delayed in-flight save.
	f.api.sessions.Put(stale, "delayed", true)
	if _, _, err := f.api.sessions.Commit(stale); err != nil {
		t.Fatal(err)
	}
	restarted := New(f.pool, f.service, true, f.api.logger, nil).Handler()
	requireStatus(t, call(t, restarted, "GET", "/api/auth/me", nil, browser), 401)
	requireStatus(t, call(t, restarted, "GET", "/api/auth/me", nil, other), 200)
	requireStatus(t, nativeCall(t, restarted, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, restarted, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil), 200)
	// An old revoked browser cookie must not block a new explicit login.
	response := call(t, restarted, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, browser)
	requireStatus(t, response, 200)
	var fresh *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "ted_session" {
			fresh = cookie
		}
	}
	if fresh == nil {
		t.Fatal("new browser session absent")
	}
	requireStatus(t, call(t, restarted, "GET", "/api/auth/me", nil, fresh), 200)
	// The deliberately separate all-device action still revokes native access.
	requireStatus(t, call(t, restarted, "POST", "/api/auth/logout", nil, fresh), 200)
	requireStatus(t, nativeCall(t, restarted, "GET", "/api/client/v1/auth/me", pair.AccessToken, nil, nil), 401)
	requireStatus(t, call(t, restarted, "GET", "/api/auth/me", nil, other), 401)
}
