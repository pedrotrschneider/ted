package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuildDelivery(t *testing.T) {
	icon := []byte("\x89PNG\r\n\x1a\nTED icon")
	files := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>TED</title>")}, "icon.png": {Data: icon}, "assets/index-test1234.js": {Data: []byte("export {};")}, "private.txt": {Data: []byte("secret")}, "private.png": {Data: []byte("secret")}, "assets/private.png": {Data: []byte("secret")}, "assets/index.js.map": {Data: []byte("source")}}
	ui, err := New(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
		mime, cache  string
	}{
		{"GET", "/", 200, "text/html", "no-cache"}, {"HEAD", "/", 200, "text/html", "no-cache"},
		{"GET", "/assets/index-test1234.js", 200, "javascript", "immutable"},
		{"GET", "/icon.png", 200, "image/png", "no-cache"}, {"HEAD", "/icon.png", 200, "image/png", "no-cache"},
		{"GET", "/icon.png?v=ted", 200, "image/png", "no-cache"}, {"POST", "/icon.png", 405, "text/plain", ""},
		{"POST", "/", 405, "text/plain", ""}, {"GET", "/private.txt", 404, "text/plain", ""},
		{"GET", "/private.png", 404, "text/plain", ""}, {"GET", "/assets/private.png", 404, "text/plain", ""},
		{"GET", "/assets/../private.txt", 404, "text/plain", ""}, {"GET", "/assets/index.js.map", 404, "text/plain", ""}, {"GET", "/assets/", 404, "text/plain", ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRecorder()
			ui.ServeHTTP(r, httptest.NewRequest(tc.method, tc.path, nil))
			if r.Code != tc.status || !strings.Contains(r.Header().Get("Content-Type"), tc.mime) || !strings.Contains(r.Header().Get("Cache-Control"), tc.cache) {
				t.Fatalf("response: %d %v", r.Code, r.Header())
			}
			if tc.method == "HEAD" && r.Body.Len() != 0 {
				t.Fatal("HEAD returned body")
			}
			if tc.method == "GET" && strings.HasPrefix(tc.path, "/icon.png") && !bytes.Equal(r.Body.Bytes(), icon) {
				t.Fatal("favicon bytes were not served intact")
			}
			if strings.Contains(r.Body.String(), "secret") {
				t.Fatal("exposed unrelated file")
			}
		})
	}
	if _, err := New(fstest.MapFS{}); err == nil {
		t.Fatal("missing build accepted")
	}
}
func TestAPIBoundary(t *testing.T) {
	ui, _ := New(fstest.MapFS{"index.html": {Data: []byte("TED")}})
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	h := WithAPI(api, ui)
	for _, p := range []string{"/api", "/api/unknown", "/health/ready"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", p, nil))
		if r.Code != 401 {
			t.Fatalf("%s: %d", p, r.Code)
		}
	}
}

func TestPosterRedirectHostAllowedOnlyForImages(t *testing.T) {
	ui, err := New(fstest.MapFS{"index.html": {Data: []byte("TED")}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	ui.ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
	directives := map[string][]string{}
	for _, part := range strings.Split(r.Header().Get("Content-Security-Policy"), ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			directives[fields[0]] = fields[1:]
		}
	}
	// The observed 307 destination must load without broadening other resource access.
	images := map[string]bool{}
	for _, source := range directives["img-src"] {
		images[source] = true
	}
	for _, host := range []string{"https://images.metahub.space", "https://live.metahub.space", "https://debridmediamanager.com/apple-touch-icon.png"} {
		if !images[host] {
			t.Fatalf("poster host %s blocked", host)
		}
	}
	for _, source := range []string{"*", "https:", "https://*.metahub.space"} {
		if images[source] {
			t.Fatalf("overbroad image policy: %s", source)
		}
	}
	for _, name := range []string{"script-src", "connect-src", "default-src"} {
		if strings.Join(directives[name], " ") != "'self'" {
			t.Fatalf("%s was broadened", name)
		}
	}
}
