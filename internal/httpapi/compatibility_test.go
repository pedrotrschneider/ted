package httpapi

import (
	tedversion "entertainmentdistrict"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeCompatibilityBoundary(t *testing.T) {
	for _, major := range []string{tedversion.Major(), "999999", "invalid"} {
		called := false
		h := compatibilityHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
		r := httptest.NewRequest("POST", "/api/client/v1/auth/login", nil)
		r.Header.Set("X-Teddy-Compatibility-Major", major)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		match := major == tedversion.Major()
		if called != match {
			t.Fatalf("major %q reached login=%v", major, called)
		}
		if !match && w.Code != 426 {
			t.Fatalf("expected mismatch, got %d", w.Code)
		}
		if w.Header().Get("X-TED-Compatibility-Major") != tedversion.Major() {
			t.Fatal("missing version header")
		}
	}
}

func TestNativePublicVersion(t *testing.T) {
	h := compatibilityHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("version required authentication") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/client/v1/version", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
}
