package addons

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
)

// Explicit diagnostic only: a short-lived capability exposes one existing
// session to a local player over an SSH forward. No normal app route changes.
func TestLivePlaybackPath(t *testing.T) {
	id := os.Getenv("TED_LIVE_PATH_SESSION")
	if id == "" {
		t.Skip("no path comparison requested")
	}
	if !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	pool, e := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	var g accounts.NativeGrant
	var raw []byte
	e = pool.QueryRow(ctx, `SELECT ps.user_id,ps.device_id,ps.family_id::text,p.source FROM native_playback_sessions ps JOIN native_addon_playback p ON p.session_id=ps.id WHERE ps.id=$1`, id).Scan(&g.User.ID, &g.DeviceID, &g.FamilyID, &raw)
	if e != nil {
		t.Fatal("session unavailable")
	}
	var source Source
	if json.Unmarshal(raw, &source) != nil || !realdebrid.ValidDownloadURL(source.URL) || len(source.Headers) != 0 {
		t.Fatal("unsupported diagnostic source")
	}
	s := New(pool, nil, nil, nil)
	s.RD, e = realdebrid.NewInventory(pool, realdebrid.NewStored(settings.New(pool)), nil)
	if e != nil {
		t.Fatal("inventory unavailable")
	}
	defer s.media.CloseIdleConnections()
	defer s.http.CloseIdleConnections()
	s.media.Transport = preparationProbeTransport{base: s.media.Transport, t: t}
	var addonRaw []byte
	var addon Source
	var expected string
	if e = pool.QueryRow(ctx, `SELECT ns.source,p.fingerprint FROM native_playback_sessions ps JOIN native_addon_sources ns ON ns.id=ps.addon_source_id JOIN native_addon_playback p ON p.session_id=ps.id WHERE ps.id=$1`, id).Scan(&addonRaw, &expected); e != nil || json.Unmarshal(addonRaw, &addon) != nil {
		t.Fatal("original add-on source unavailable")
	}
	addonReady := false
	if os.Getenv("TED_LIVE_PATH_ADDON") == "1" && addon.URL == "" {
		// Identified torrent sources deliberately discard their resolver URL.
		// Fetch it only for this diagnostic and match the stored release key.
		v, _, e := s.playback(ctx, g, id)
		if e != nil {
			t.Fatal("linked source unavailable")
		}
		target, e := v.descriptor.StreamURL(mediaIdentity(v.media.Kind, v.media.ID))
		if e != nil {
			t.Fatal("add-on endpoint unavailable")
		}
		bounded, stop := context.WithTimeout(ctx, 25*time.Second)
		req, e := sourceRequest(bounded, Source{URL: target}, "")
		if e != nil {
			stop()
			t.Fatal("invalid add-on endpoint")
		}
		res, e := s.http.Do(req)
		if e != nil {
			stop()
			t.Fatal("add-on lookup failed")
		}
		body, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
		res.Body.Close()
		stop()
		var wire struct {
			Streams []json.RawMessage `json:"streams"`
		}
		if e != nil || res.StatusCode != 200 || len(body) > 2<<20 || json.Unmarshal(body, &wire) != nil {
			t.Fatal("invalid add-on response")
		}
		matches := 0
		for _, raw := range wire.Streams {
			candidate, e := parseSource(raw)
			if e != nil || candidate.ReleaseKey() != addon.ReleaseKey() {
				continue
			}
			var original struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(raw, &original) != nil || original.URL == "" {
				continue
			}
			matches++
			addon.URL = original.URL
		}
		if matches != 1 {
			t.Fatal("original direct URL not uniquely matched")
		}
	}
	if os.Getenv("TED_LIVE_PATH_ADDON") == "1" && addon.URL != "" && len(addon.Headers) == 0 {
		bounded, stop := context.WithTimeout(ctx, 30*time.Second)
		started := time.Now()
		p, e := s.prepare(bounded, addon)
		stop()
		addonReady = e == nil && p.Fingerprint == expected
		t.Logf("ADDON validation elapsed=%s result=%s same_file=%t", time.Since(started), probeResult(e), addonReady)
	}
	secret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		t.Fatal("random unavailable")
	}
	token := hex.EncodeToString(secret)
	listener, e := net.Listen("tcp", "0.0.0.0:37965")
	if e != nil {
		t.Fatal("listen unavailable")
	}
	done := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/prepare-baseline", "/prepare-optimized":
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			s.startup.mu.Lock()
			delete(s.startup.sessions, id)
			s.startup.mu.Unlock()
			candidate := source
			if r.URL.Path == "/prepare-baseline" {
				candidate.RDFile = nil
			}
			started := time.Now()
			bounded, stop := context.WithTimeout(r.Context(), 30*time.Second)
			p, err := s.preparePlayback(bounded, candidate)
			stop()
			if err != nil || p.Fingerprint != expected {
				t.Logf("PREP mode=%s result=%s elapsed=%s same_file=%t", r.URL.Path, probeResult(err), time.Since(started), p.Fingerprint == expected)
				http.Error(w, "preparation failed", 502)
				return
			}
			p.Source = source
			s.rememberStartup(id, p)
			t.Logf("PREP mode=%s elapsed=%s same_file=true", r.URL.Path, time.Since(started))
			w.WriteHeader(204)
		case "/direct":
			http.Redirect(w, r, source.URL, http.StatusTemporaryRedirect)
		case "/addon":
			if !addonReady {
				http.Error(w, "unavailable", 502)
				return
			}
			http.Redirect(w, r, addon.URL, http.StatusTemporaryRedirect)
		case "/ted":
			started := time.Now()
			if e := s.Serve(w, r, g, id); e != nil {
				t.Logf("media failure=%s", probeResult(e))
				http.Error(w, "unavailable", 502)
			}
			t.Logf("CLIENT range=%s elapsed=%s", r.Header.Get("Range"), time.Since(started))
		case "/done":
			select {
			case done <- struct{}{}:
			default:
			}
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	path := "/metadata/playback-path-capability.json"
	if e = os.WriteFile(path, []byte(`{"token":"`+token+`"}`), 0600); e != nil {
		t.Fatal("capability write failed")
	}
	defer os.Remove(path)
	t.Log("comparison ready")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("comparison expired")
	}
}
