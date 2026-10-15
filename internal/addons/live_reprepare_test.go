package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"os"
	"testing"
	"time"
)

// Re-prepare the exact retained file without creating sessions, viewing events,
// torrents or file selections. Opt-in and credential-redacted.
func TestLiveSelectedRepreparation(t *testing.T) {
	id := os.Getenv("TED_LIVE_REPREPARE_SESSION")
	if id == "" {
		t.Skip("no selected session")
	}
	if !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, e := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	var raw []byte
	var expected string
	if e = pool.QueryRow(ctx, `SELECT ns.source,p.fingerprint FROM native_playback_sessions ps JOIN native_addon_sources ns ON ns.id=ps.addon_source_id JOIN native_addon_playback p ON p.session_id=ps.id WHERE ps.id=$1`, id).Scan(&raw, &expected); e != nil {
		t.Fatal("retained source unavailable")
	}
	var source Source
	if json.Unmarshal(raw, &source) != nil || source.InfoHash == "" {
		t.Fatal("invalid torrent source")
	}
	s := New(pool, nil, nil, nil)
	s.RD, e = realdebrid.NewInventory(pool, realdebrid.NewStored(settings.New(pool)), nil)
	if e != nil {
		t.Fatal("inventory unavailable")
	}
	defer s.media.CloseIdleConnections()
	s.media.Transport = preparationProbeTransport{base: s.media.Transport, t: t}
	for trial := 0; trial < 3; trial++ {
		started := time.Now()
		scope, e := s.RD.Snapshot(ctx)
		if e != nil {
			t.Fatal("credentials unavailable")
		}
		want, resolved, e := s.RD.ResolveAvailableSelection(ctx, torrentSelection(source))
		t.Logf("trial=%d RD resolve=%v result=%s", trial, time.Since(started), probeResult(e))
		if e != nil {
			continue
		}
		current := source
		current.URL = resolved.URL
		current.Headers = map[string]string{}
		current.RDFile = &want
		current.RDScope = scope.CredentialScope()
		current.VideoSize = resolved.Bytes
		u, _ := httpURL(current.URL)
		t.Logf("trial=%d public CDN host=%s", trial, u.Hostname())
		prepared, e := s.preparePlayback(ctx, current)
		prepared.startup.close()
		t.Logf("trial=%d total=%v result=%s identity_matches=%t", trial, time.Since(started), probeResult(e), e == nil && prepared.Fingerprint == expected)
	}
}
