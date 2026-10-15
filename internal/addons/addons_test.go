package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/stremio"
	"entertainmentdistrict/internal/testdb"
	"entertainmentdistrict/internal/watchstate"
)

type accountFake struct {
	mu               sync.Mutex
	collection       []json.RawMessage
	err              error
	entered, release chan struct{}
	calls            int
}

func (f *accountFake) AddonCollection(ctx context.Context, key string) ([]json.RawMessage, error) {
	if key != "private-stremio-key-fixture" {
		return nil, ErrUnavailable
	}
	f.mu.Lock()
	wire, err, entered, release := f.collection, f.err, f.entered, f.release
	f.calls++
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return wire, err
}

type providerFake struct {
	mu                                       sync.Mutex
	url                                      string
	expired, ambiguous, unsupported, changed bool
	variant                                  int
	requests                                 int
	entered, release                         chan struct{}
}

func (p *providerFake) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	expired, ambiguous, unsupported, changed, entered, release := p.expired, p.ambiguous, p.unsupported, p.changed, p.entered, p.release
	p.requests++
	base := p.url
	variant := p.variant
	p.mu.Unlock()
	if strings.Contains(r.URL.Path, "/stream/") {
		target := base + "/media/old"
		if expired {
			target = base + "/media/new"
		}
		source := map[string]any{"url": target, "name": "Fixture 720p", "description": "Direct file", "subtitles": []map[string]any{{"id": "english", "url": base + "/subtitle", "lang": "eng"}}, "behaviorHints": map[string]any{"filename": "fixture.mp4", "videoHash": "1234567890abcdef", "videoSize": 16000, "bingeGroup": "fixture-720p", "proxyHeaders": map[string]any{"request": map[string]string{"Authorization": "provider-only", "User-Agent": "Fixture"}}}}
		if variant > 0 {
			source["name"] = fmt.Sprintf("Release %d", variant)
			source["behaviorHints"].(map[string]any)["bingeGroup"] = fmt.Sprintf("group-%d", variant)
			source["behaviorHints"].(map[string]any)["filename"] = fmt.Sprintf("release-%d.mp4", variant)
		}
		streams := []any{source}
		if ambiguous {
			streams = append(streams, source)
		}
		if unsupported {
			streams = append(streams, map[string]any{"nzbUrl": "https://example.com/archive.nzb"}, map[string]any{"externalUrl": "https://example.com/page"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"streams": streams})
		return
	}
	if r.URL.Path == "/subtitle" {
		if r.Header.Get("Authorization") != "provider-only" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nFixture subtitle\n"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/media/") {
		if r.Header.Get("Authorization") != "provider-only" || r.Header.Get("Accept-Encoding") != "identity" {
			w.WriteHeader(401)
			return
		}
		if expired && r.URL.Path == "/media/old" {
			w.WriteHeader(403)
			return
		}
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"same-release"`)
		body := bytes.Repeat([]byte("fixture-file----"), 1000)
		if changed {
			body[len(body)-1] = 'x'
		}
		http.ServeContent(w, r, "fixture.mp4", time.Time{}, bytes.NewReader(body))
		return
	}
	w.WriteHeader(503)
}
func descriptorRaw(base string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"transportUrl": base + "/configured-secret/manifest.json", "manifest": map[string]any{"id": "fixture", "name": "Fixture add-on", "types": []string{"movie", "series"}, "resources": []string{"stream"}, "idPrefixes": []string{"tt"}}})
	return data
}
func fixture(t *testing.T) (*Service, *accounts.NativeAuth, accounts.NativeGrant, *accountFake, *providerFake) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.New(t)
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	passwords, _ := accounts.NewPasswords(0)
	users := accounts.New(pool, passwords)
	user, err := users.Bootstrap(ctx, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := accounts.NewNativeAuth(pool, users, accounts.DefaultNativePolicy())
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.Login(ctx, "admin", "", "127.0.0.1", "device-1", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := auth.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('fixture','fixture@example.com','private-stremio-key-fixture')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO stremio_links(user_id,account_id) VALUES($1,'fixture')`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES('fixture','fixture@example.com','private-stremio-key-fixture')`); err != nil {
		t.Fatal(err)
	}
	provider := &providerFake{}
	server := httptest.NewServer(http.HandlerFunc(provider.handler))
	t.Cleanup(server.Close)
	provider.url = server.URL
	remote := &accountFake{collection: []json.RawMessage{descriptorRaw(server.URL)}}
	return New(pool, remote, server.Client(), nil), auth, grant, remote, provider
}

var fixtureMedia = catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}

func choose(t *testing.T, s *Service, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity) string {
	t.Helper()
	page, err := s.Sources(context.Background(), auth, g, media, "")
	if err != nil || len(page.Sources) != 1 {
		t.Fatal("source discovery", err, len(page.Sources))
	}
	return page.Sources[0].ID
}
func TestCollectionCacheFailureEmptyAndLinkIsolation(t *testing.T) {
	s, _, g, remote, _ := fixture(t)
	ctx := context.Background()
	view, err := s.Collection(ctx, g.User.ID, false)
	if err != nil || len(view.Addons) != 1 || view.Cached {
		t.Fatal(view, err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "127.0.0.1") {
		t.Fatal("collection leaked configuration")
	}
	remote.mu.Lock()
	remote.err = ErrUnavailable
	remote.mu.Unlock()
	view, err = s.Collection(ctx, g.User.ID, true)
	if err != nil || !view.Cached || len(view.Addons) != 1 || len(view.Issues) != 1 {
		t.Fatal("lost saved collection", view, err)
	}
	remote.mu.Lock()
	remote.err = nil
	remote.collection = []json.RawMessage{}
	remote.mu.Unlock()
	view, err = s.Collection(ctx, g.User.ID, true)
	if err != nil || len(view.Addons) != 0 || view.Cached {
		t.Fatal("empty is distinct from failed", view, err)
	}
	remote.mu.Lock()
	remote.err = stremio.ErrCredentials
	remote.mu.Unlock()
	if _, err = s.Collection(ctx, g.User.ID, true); !errors.Is(err, ErrCredentials) {
		t.Fatal("rejected credentials reused cache", err)
	}
	if _, err = s.Collection(ctx, g.User.ID, false); !errors.Is(err, ErrCredentials) {
		t.Fatal("normal read bypassed credential rejection", err)
	}
	remote.mu.Lock()
	remote.err = ErrUnavailable
	remote.mu.Unlock()
	if _, err = s.Collection(ctx, g.User.ID, false); !errors.Is(err, ErrCredentials) {
		t.Fatal("later outage resurrected rejected collection", err)
	}
	if _, err = s.Collection(ctx, g.User.ID+1, false); !errors.Is(err, ErrNotLinked) {
		t.Fatal("collection crossed accounts", err)
	}
}
func TestSourcesSelectedRangesSubtitlesAndUnknownDurationHistory(t *testing.T) {
	s, auth, g, _, provider := fixture(t)
	ctx := context.Background()
	provider.mu.Lock()
	provider.unsupported = true
	provider.mu.Unlock()
	page, err := s.Sources(ctx, auth, g, fixtureMedia, "")
	if err != nil || len(page.Sources) != 1 || len(page.Issues) != 1 || page.Issues[0].Count != 2 {
		t.Fatal(page, err)
	}
	var identities int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM catalog_media`).Scan(&identities); err != nil || identities != 0 {
		t.Fatal("discovery created identities", identities, err)
	}
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, page.Sources[0].ID)
	if err != nil || session.DurationMS != 0 || session.Source != "addon" || len(session.Subtitles) != 1 {
		t.Fatal(session, err)
	}
	raw, _ := json.Marshal(session)
	if strings.Contains(string(raw), "provider-only") || strings.Contains(string(raw), "configured-secret") || strings.Contains(string(raw), "127.0.0.1") {
		t.Fatal("session leaked provider data")
	}
	request := httptest.NewRequest("GET", session.MediaURL, nil)
	request.Header.Set("Range", "bytes=100-199")
	w := httptest.NewRecorder()
	if err = s.Serve(w, request, g, session.ID); err != nil || w.Code != 206 || w.Body.Len() != 100 || w.Header().Get("Content-Range") != "bytes 100-199/16000" {
		t.Fatal("range serving", w.Code, w.Body.Len(), err)
	}
	head := httptest.NewRecorder()
	if err = s.Serve(head, httptest.NewRequest("HEAD", session.MediaURL, nil), g, session.ID); err != nil || head.Code != 200 || head.Header().Get("Content-Length") != "16000" || head.Body.Len() != 0 {
		t.Fatal("HEAD", head.Code, err)
	}
	data, _, err := s.Subtitle(ctx, g, session.ID, 0)
	if err != nil || !bytes.Contains(data, []byte("Fixture subtitle")) {
		t.Fatal("subtitle", err)
	}
	g2 := g
	g2.DeviceID = "other-device"
	if err = s.Serve(httptest.NewRecorder(), request, g2, session.ID); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatal("session crossed devices", err)
	}
	store := watchstate.New(s.pool)
	now := time.Now().UTC()
	events := []watchstate.NativeEvent{
		{ID: "11111111-1111-4111-8111-111111111111", SessionID: session.ID, Sequence: 1, Media: fixtureMedia, Kind: "start", ObservedAt: now, Origin: "live"},
		{ID: "22222222-2222-4222-8222-222222222222", SessionID: session.ID, Sequence: 2, Media: fixtureMedia, Kind: "progress", PositionMS: 8000, DurationMS: 10000, ObservedAt: now.Add(time.Second), Origin: "live"},
		{ID: "33333333-3333-4333-8333-333333333333", SessionID: session.ID, Sequence: 3, Media: fixtureMedia, Kind: "stop", PositionMS: 9500, DurationMS: 10000, ObservedAt: now.Add(2 * time.Second), Origin: "live"},
	}
	if _, err = store.IngestNative(ctx, auth, g, events); err != nil {
		t.Fatal(err)
	}
	state, err := store.NativeState(ctx, g.User.ID, fixtureMedia)
	if err != nil || !state.Played || state.DurationMS == nil || *state.DurationMS != 10000 || state.PlayCount != 1 {
		t.Fatal(state, err)
	}
	if _, err = store.IngestNative(ctx, auth, g, events); err != nil {
		t.Fatal("replay", err)
	}
	var mappings, local, history int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_mappings),(SELECT count(*) FROM local_files),(SELECT count(*) FROM watch_history)`).Scan(&mappings, &local, &history); err != nil || mappings != 0 || local != 0 || history != 2 {
		t.Fatal("unmapped viewing created shared files or duplicate history", mappings, local, history, err)
	}
}
func TestAddonSessionResumesUnfinishedRewatchWithoutClearingWatchedHistory(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	store := watchstate.New(s.pool)
	prior, err := store.EditNative(ctx, auth, g, watchstate.NativeEdit{Media: fixtureMedia, Action: "watched"})
	if err != nil {
		t.Fatal(err)
	}
	source := choose(t, s, auth, g, fixtureMedia)
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, source)
	if err != nil || session.ResumeMS != 0 {
		t.Fatal("completed item did not start a new viewing", session, err)
	}
	at := prior.LastPlayed.Add(time.Second)
	events := []watchstate.NativeEvent{
		{ID: "aaaaaaaa-1111-4111-8111-111111111111", SessionID: session.ID, Sequence: 1, Media: fixtureMedia, Kind: "start", PositionMS: 15000, DurationMS: 100000, ObservedAt: at, Origin: "live"},
		{ID: "aaaaaaaa-2222-4222-8222-222222222222", SessionID: session.ID, Sequence: 2, Media: fixtureMedia, Kind: "stop", PositionMS: 40000, DurationMS: 100000, ObservedAt: at, Origin: "live"},
	}
	if _, err = store.IngestNative(ctx, auth, g, events); err != nil {
		t.Fatal(err)
	}
	resume, err := s.CreateSession(ctx, auth, g, fixtureMedia, source)
	if err != nil || resume.ResumeMS != 40000 || resume.WatchRevision != 3 {
		t.Fatal("watched flag suppressed unfinished add-on resume", resume, err)
	}
	state, err := store.NativeState(ctx, g.User.ID, fixtureMedia)
	if err != nil || !state.Played || state.PlayCount != 1 || state.PositionMS != 40000 {
		t.Fatal("resuming changed the watched decision", state, err)
	}
	history, err := store.NativeHistory(ctx, g.User.ID, 0)
	if err != nil || len(history.Entries) != 3 || history.Entries[0].Kind != "playback_stopped" {
		t.Fatal("rewatch history was lost", history, err)
	}
}

func TestRenewalPinsReleaseAndSerializesConcurrentRanges(t *testing.T) {
	s, auth, g, _, p := fixture(t)
	ctx := context.Background()
	id := choose(t, s, auth, g, fixtureMedia)
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, id)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.expired = true
	p.mu.Unlock()
	// Hold the production renewal row while both requests reach it. They must
	// reuse one replacement after the lock is released, including after restart.
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(lock)
	if _, err = lock.Exec(ctx, `SELECT session_id FROM native_addon_playback WHERE session_id=$1 FOR UPDATE`, session.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", session.MediaURL, nil)
			r.Header.Set("Range", "bytes=10-19")
			e := s.Serve(w, r, g, session.ID)
			if e == nil && (w.Code != 206 || w.Body.Len() != 10) {
				e = fmt.Errorf("unexpected range response")
			}
			results <- e
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		err = s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%native_addon_playback%' AND pid!=pg_backend_pid()`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if blocked < 2 {
		t.Fatal("workers did not reach held renewal lock")
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	restarted := New(s.pool, s.remote, s.http, nil)
	w := httptest.NewRecorder()
	if err = restarted.Serve(w, httptest.NewRequest("GET", session.MediaURL, nil), g, session.ID); err != nil || w.Body.Len() != 16000 {
		t.Fatal("restart lost source", err)
	}
	p.mu.Lock()
	p.expired = false
	p.mu.Unlock()
	_, err = s.pool.Exec(ctx, `UPDATE native_addon_playback SET source=jsonb_set(source,'{url}',to_jsonb($2::text)) WHERE session_id=$1`, session.ID, p.url+"/media/old")
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.expired = true
	p.ambiguous = true
	p.mu.Unlock()
	if err = s.Serve(httptest.NewRecorder(), httptest.NewRequest("GET", session.MediaURL, nil), g, session.ID); !errors.Is(err, ErrSelection) {
		t.Fatal("ambiguous renewal changed release", err)
	}
}
func TestLinkChangesDuringReadAndPreparationAreFenced(t *testing.T) {
	s, auth, g, remote, p := fixture(t)
	ctx := context.Background()
	id := choose(t, s, auth, g, fixtureMedia)
	p.mu.Lock()
	p.entered = make(chan struct{}, 1)
	p.release = make(chan struct{})
	entered, release := p.entered, p.release
	p.mu.Unlock()
	result := make(chan error, 1)
	go func() { _, err := s.CreateSession(ctx, auth, g, fixtureMedia, id); result <- err }()
	<-entered
	if err := auth.Logout(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal("revocation during preparation committed session", err)
	}
	var sessions int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM native_playback_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal(sessions, err)
	}
	remote.mu.Lock()
	remote.entered = make(chan struct{}, 1)
	remote.release = make(chan struct{})
	entered, release = remote.entered, remote.release
	remote.mu.Unlock()
	go func() { _, err := s.Collection(ctx, g.User.ID, true); result <- err }()
	<-entered
	if _, err := s.pool.Exec(ctx, `UPDATE server_addon_accounts SET active=false,auth_key='' WHERE active`); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrChanged) {
		t.Fatal("retired link published collection", err)
	}
}
func TestPublicNetworkAndProtocolRejectUnsupportedForms(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.2", "169.254.169.254", "100.100.100.200", "fc00::1", "0.0.0.0", "240.1.2.3", "64:ff9b::7f00:1", "2002:7f00:1::1", "2001:db8::1"} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Fatal("private address accepted", address)
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("private dial occurred") }))
	defer server.Close()
	if _, err := PublicClient().Get(server.URL); err == nil {
		t.Fatal("public client reached a private server")
	}
	for _, raw := range []string{`{"url":"https://example.com/file.m3u8"}`, `{"url":"https://example.com/movie.mkv","infoHash":"a"}`, `{"externalUrl":"https://example.com/page"}`, `{"url":"file:///tmp/movie.mkv"}`, `{"url":"https://example.com/movie.mp4","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer ted_a_private"}}}}`} {
		if _, err := parseSource(json.RawMessage(raw)); err == nil {
			t.Fatal("unsupported source accepted")
		}
	}
	full, _ := parseDescriptor([]byte(`{"transportUrl":"https://example.com/key/manifest.json","manifest":{"id":"full","name":"Full","types":["movie"],"resources":[{"name":"stream","types":["series"],"idPrefixes":["tt"]}]}}`))
	if !full.Supports(mediaIdentity("episode", "tt1234567:1:2")) || full.Supports(fixtureMedia) {
		t.Fatal("resource capability precedence")
	}
}
