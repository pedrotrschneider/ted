package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/streaming"
)

func TestNativeSourceLookupExtendsOrdinaryWriteDeadline(t *testing.T) {
	f, _, tokens := nativePlaybackFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "https://cdn.example/episode.mkv"}}})
	}))
	defer upstream.Close()
	h, _ := installPreparationAddon(t, f, upstream, upstream.Client(), tokens.AccessToken)
	server := httptest.NewUnstartedServer(h)
	server.Listener = expiredPreparationListener{server.Listener}
	server.Config.WriteTimeout = 20 * time.Second
	server.Start()
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}, "provider": "stremio"})
	req, _ := http.NewRequest("POST", server.URL+"/api/client/v1/playback/sources", bytes.NewReader(body))
	req.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal("lookup lost its response to ordinary write deadline", err)
	}
	defer res.Body.Close()
	var page struct {
		Sources []addons.Choice `json:"sources"`
	}
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&page) != nil || len(page.Sources) != 1 {
		t.Fatal("source lookup response lost", res.StatusCode)
	}
}

func TestNativeSlowSelectedMediaPreparesPinnedDownloadOnRealConnection(t *testing.T) {
	f, _, tokens := nativePlaybackFixture(t)
	var fileReads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stream/") {
			json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "http://" + r.Host + "/file", "name": "Chosen release"}}})
			return
		}
		if fileReads.Add(1) <= 2 {
			// Two valid probes exceed the old six-second preparation cap while
			// each remains below the old four-second header deadline.
			timer := time.NewTimer(3400 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"chosen-release"`)
		http.ServeContent(w, r, "file.mp4", time.Time{}, strings.NewReader(strings.Repeat("file", 4000)))
	}))
	defer upstream.Close()
	h, sourceID := installPreparationAddon(t, f, upstream, upstream.Client(), tokens.AccessToken)
	server := httptest.NewUnstartedServer(h)
	// Expire the ordinary JSON write deadline deterministically. Session
	// preparation must extend it before waiting on the provider's two probes.
	server.Listener = expiredPreparationListener{server.Listener}
	server.Config.WriteTimeout = 20 * time.Second
	server.Start()
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}, "source_id": sourceID})
	req, _ := http.NewRequest("POST", server.URL+"/api/client/v1/playback/sessions", bytes.NewReader(body))
	req.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal("slow selected file lost its HTTP response", err)
	}
	defer res.Body.Close()
	var session streaming.NativeSession
	if json.NewDecoder(res.Body).Decode(&session) != nil || res.StatusCode != 201 {
		t.Fatal("valid selected file failed preparation", res.StatusCode)
	}
	r := nativeCall(t, h, "POST", "/api/client/v1/downloads/resolve", tokens.AccessToken, map[string]any{"session_id": session.ID}, nil)
	requireStatus(t, r, 200)
	var descriptor streaming.NativeDownload
	if json.Unmarshal(r.Body.Bytes(), &descriptor) != nil || descriptor.Bytes != 16000 || descriptor.ETag == "" {
		t.Fatal("missing validated download descriptor")
	}
	rangeRequest := httptest.NewRequest("GET", descriptor.MediaURL, nil)
	rangeRequest.Header.Set("X-Teddy-Compatibility-Version", "0.2")
	rangeRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	rangeRequest.Header.Set("If-Match", descriptor.ETag)
	rangeRequest.Header.Set("Range", "bytes=100-199")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rangeRequest)
	if w.Code != 206 || w.Body.Len() != 100 || w.Header().Get("ETag") != descriptor.ETag {
		t.Fatal("prepared file could not start a pinned download", w.Code)
	}
}

func TestNativeMediaPreparationFailureIsDistinctFromAddonDiscovery(t *testing.T) {
	f, _, tokens := nativePlaybackFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "http://" + r.Host + "/file", "name": "Selected release"}}})
	}))
	defer upstream.Close()
	client := &http.Client{Transport: preparationUnavailableTransport{upstream.Client().Transport}}
	h, sourceID := installPreparationAddon(t, f, upstream, client, tokens.AccessToken)
	r := nativeCall(t, h, "POST", "/api/client/v1/playback/sessions", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}, "source_id": sourceID}, nil)
	requireStatus(t, r, 503)
	var result map[string]string
	if json.Unmarshal(r.Body.Bytes(), &result) != nil || result["code"] != "source_preparation_unavailable" || strings.Contains(r.Body.String(), "private") {
		t.Fatal("selected file failure lost its safe preparation error", r.Body.String())
	}
	var effects int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM native_addon_playback)+(SELECT count(*) FROM watch_history)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal("failed preparation created playback or viewing state", effects, err)
	}
}

func installPreparationAddon(t *testing.T, f *fixture, upstream *httptest.Server, client *http.Client, token string) (http.Handler, string) {
	t.Helper()
	descriptor, _ := json.Marshal(map[string]any{"transportUrl": upstream.URL + "/private-addon/manifest.json", "manifest": map[string]any{"id": "fixture", "name": "Fixture", "types": []string{"movie"}, "resources": []string{"stream"}}})
	f.api.addons = addons.New(f.pool, addonAccountFixture{[]json.RawMessage{descriptor}}, client, nil)
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES('addon','fixture@example.com','private-auth-key')`); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	r := nativeCall(t, h, "POST", "/api/client/v1/playback/sources", token, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}}, nil)
	requireStatus(t, r, 200)
	var page struct{ Sources []addons.Choice }
	if json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Sources) != 1 {
		t.Fatal("selected source unavailable in fixture")
	}
	return h, page.Sources[0].ID
}

type preparationUnavailableTransport struct{ base http.RoundTripper }

func (p preparationUnavailableTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/file" {
		return nil, errors.New("private selected link failed")
	}
	return p.base.RoundTrip(r)
}

type expiredPreparationListener struct{ net.Listener }

func (l expiredPreparationListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &expiredPreparationConn{Conn: c}, nil
}

type expiredPreparationConn struct {
	net.Conn
	mu      sync.Mutex
	initial bool
}

func (c *expiredPreparationConn) SetWriteDeadline(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initial && !at.IsZero() {
		c.initial = true
		return c.Conn.SetWriteDeadline(time.Now().Add(-time.Second))
	}
	return c.Conn.SetWriteDeadline(at)
}

func TestNativeBackgroundPreparationAuthenticatesAndAvoidsDirectResolver(t *testing.T) {
	f, _, tokens := nativePlaybackFixture(t)
	var reads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "https://cdn.example/movie.mkv"}}})
	}))
	defer upstream.Close()
	h, id := installPreparationAddon(t, f, upstream, upstream.Client(), tokens.AccessToken)
	body := map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}, "source_id": id}
	before := reads.Load()
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/preparations", "", body, nil), 401)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/preparations", tokens.AccessToken, body, nil), 422)
	requireStatus(t, nativeCall(t, h, "DELETE", "/api/client/v1/playback/preparations/"+id, tokens.AccessToken, nil, nil), 200)
	if reads.Load() != before {
		t.Fatal("background route invoked direct-link resolver")
	}
}
