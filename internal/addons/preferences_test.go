package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/sourceprefs"
	"entertainmentdistrict/internal/watchstate"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func secondViewer(t *testing.T, s *Service, a *accounts.NativeAuth) accounts.NativeGrant {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(username,display_name,role,password_hash) SELECT 'viewer','Viewer','user',password_hash FROM users WHERE username='admin'`); err != nil {
		t.Fatal(err)
	}
	tokens, err := a.Login(ctx, "viewer", "", "127.0.0.1", "device-2", "Fixture")
	if err != nil {
		t.Fatal(err)
	}
	g, err := a.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func TestFavoritesMatchGroupsAcrossEpisodesAccountsAndConfigurations(t *testing.T) {
	s, a, g, remote, p := fixture(t)
	ctx := context.Background()
	episode := mediaIdentity("episode", "tt1234567:1:1")
	page, err := s.SelectedSources(ctx, a, g, episode, "")
	if err != nil {
		t.Fatal(err)
	}
	c := page.Sources[0]
	fav, err := s.SetFavorite(ctx, a, g, c.ID, true)
	if err != nil || !fav.Favorite {
		t.Fatal(fav, err)
	}
	other := secondViewer(t, s, a)
	if _, err = s.SetFavorite(ctx, a, other, c.ID, false); !errors.Is(err, ErrSelection) {
		t.Fatal("another account adopted a source handle", err)
	}
	remote.mu.Lock()
	remote.collection = []json.RawMessage{descriptorRaw(p.url + "/other-private-config")}
	remote.mu.Unlock()
	if _, err = s.Collection(ctx, other.User.ID, true); err != nil {
		t.Fatal(err)
	}
	page, err = s.SelectedSources(ctx, a, other, mediaIdentity("episode", "tt1234567:1:2"), "")
	if err != nil {
		t.Fatal(err)
	}
	fresh := page.Sources[0]
	if !fresh.Favorite || fresh.FavoriteKey != c.FavoriteKey || fresh.AddonID == c.AddonID || page.Preferred != fresh.ID || fresh.ID == c.ID {
		t.Fatal("shared group did not select a new account-owned handle", page)
	}
	unrelated, err := s.SelectedSources(ctx, a, other, mediaIdentity("episode", "tt7654321:1:2"), "")
	if err != nil || unrelated.Sources[0].Favorite {
		t.Fatal("group escaped its show", err)
	}
	if _, err = s.SetFavorite(ctx, a, other, fresh.ID, false); err != nil {
		t.Fatal(err)
	}
	page, err = s.SelectedSources(ctx, a, other, page.Media, "")
	if err != nil || page.Sources[0].Favorite {
		t.Fatal("shared removal was lost", err)
	}
	raw, _ := json.Marshal(page)
	for _, secret := range []string{p.url, "private-config", "fixture-720p"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private source data escaped")
		}
	}
}
func TestRecentGroupsOnlyRecordViewingKeepThreeAndHonorReportTimes(t *testing.T) {
	s, a, g, _, p := fixture(t)
	ctx := context.Background()
	store := watchstate.New(s.pool)
	episode := mediaIdentity("episode", "tt1234567:1:1")
	var first watchstate.NativeEvent
	for i := 1; i <= 5; i++ {
		p.mu.Lock()
		p.variant = i
		p.mu.Unlock()
		page, err := s.SelectedSources(ctx, a, g, episode, "")
		if err != nil {
			t.Fatal(err)
		}
		session, err := s.CreateSession(ctx, a, g, episode, page.Sources[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM native_recent_sources`).Scan(&count); err != nil || count != min(i-1, 3) {
			t.Fatal("preparation counted as viewing", count, err)
		}
		at := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		event := watchstate.NativeEvent{ID: fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", i), SessionID: session.ID, Sequence: 1, Media: episode, Kind: "start", PositionMS: 15000, DurationMS: 100000, ObservedAt: at, Origin: "live"}
		if i == 1 {
			first = event
		}
		if _, err = store.IngestNative(ctx, a, g, []watchstate.NativeEvent{event}); err != nil {
			t.Fatal(err)
		}
	}
	// Replay must not resurrect an evicted old source or move it to the front.
	if _, err := store.IngestNative(ctx, a, g, []watchstate.NativeEvent{first}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM native_recent_sources`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	other := secondViewer(t, s, a)
	next := mediaIdentity("episode", "tt1234567:1:2")
	page, err := s.SelectedSources(ctx, a, other, next, "")
	if err != nil || page.Sources[0].RecentRank != 1 || page.Preferred != page.Sources[0].ID {
		t.Fatal("recent group did not span episodes/accounts", page, err)
	}
	// Favorites take priority over a newer recent, without losing recent rank.
	if _, err = s.SetFavorite(ctx, a, other, page.Sources[0].ID, true); err != nil {
		t.Fatal(err)
	}
	page, err = s.SelectedSources(ctx, a, other, next, "")
	if err != nil || !page.Sources[0].Favorite || page.Sources[0].RecentRank != 1 {
		t.Fatal(page, err)
	}
	// Offline viewing can cite an owned prepared/download session; it must use
	// its original timestamp and never reuse the expired playback permission.
	originalSession, err := s.CreateSession(ctx, a, other, next, page.Sources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	registration := watchstate.NativeOfflineSession{ID: "bbbbbbbb-0000-4000-8000-000000000001", Media: next, StartedAt: first.ObservedAt.Add(-time.Hour), SourceSession: originalSession.ID}
	report := watchstate.NativeEvent{ID: "bbbbbbbb-0000-4000-8000-000000000002", SessionID: registration.ID, Sequence: 1, Media: next, Kind: "start", PositionMS: 15000, DurationMS: 100000, ObservedAt: registration.StartedAt, Origin: "offline"}
	wrong := registration
	wrong.SourceSession = first.SessionID
	if _, e := store.IngestNativeOffline(ctx, a, other, []watchstate.NativeEvent{report}, []watchstate.NativeOfflineSession{wrong}); e == nil {
		t.Fatal("foreign source provenance accepted")
	}
	if _, err = store.IngestNativeOffline(ctx, a, other, []watchstate.NativeEvent{report}, []watchstate.NativeOfflineSession{registration}); err != nil {
		t.Fatal(err)
	}
	// Failed batch commit must not leave either reports or recents.
	before := count
	bad := report
	bad.ID = "bbbbbbbb-0000-4000-8000-000000000003"
	bad.Sequence = 2
	bad.Kind = "invalid"
	if _, err = store.IngestNative(ctx, a, other, []watchstate.NativeEvent{bad}); err == nil {
		t.Fatal("invalid report accepted")
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM native_recent_sources`).Scan(&count); err != nil || count != before {
		t.Fatal("failed intake changed preferences", count, err)
	}
}
func TestSharedKeysFallbackIsTitleScopedAndIndependentOfPrivateURLs(t *testing.T) {
	var d Descriptor
	d.Manifest.ID = "addon"
	d.Transport = "private-url"
	media := catalog.MediaIdentity{Kind: "episode", ID: "tt1234567:1:1"}
	source := Source{Name: "720p", Description: "Release", URL: "private-playback"}
	key, fav, scope := sharedKeys(d, source, media)
	source.URL = "new-signed-link"
	d.Transport = "other-private-config"
	k, f, sc := sharedKeys(d, source, media)
	if key == k || fav != f || scope != sc || scope != "episode:tt1234567:1:1" {
		t.Fatal("private URL affected preferences or distinct unknown files were deduplicated")
	}
	source.Headers = map[string]string{"Authorization": "other-private"}
	newKey, newFavorite, newScope := sharedKeys(d, source, media)
	if newKey == k || newFavorite != fav || newScope != scope {
		t.Fatal("private headers changed preferences or collapsed different access requirements")
	}
	source.BingeGroup = "pack"
	_, f, sc = sharedKeys(d, source, media)
	if sc != "series:tt1234567" || f == fav {
		t.Fatal("group not show scoped")
	}
	d.Manifest.ID = "different"
	_, other, _ := sharedKeys(d, source, media)
	if other == f {
		t.Fatal("group not addon scoped")
	}
}
func TestAddonPoliciesFenceSelectionsPersistAndKeepExistingPlayback(t *testing.T) {
	s, a, g, _, p := fixture(t)
	ctx := context.Background()
	source := choose(t, s, a, g, fixtureMedia)
	session, err := s.CreateSession(ctx, a, g, fixtureMedia, source)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := s.Policies(ctx)
	if err != nil || len(policies) != 1 {
		t.Fatal(policies, err)
	}
	policy := policies[0]
	actor := accounts.Identity{ID: g.User.ID, Version: g.User.Version}
	if _, err = s.UpdatePolicy(ctx, actor, policy.ID, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdatePolicy(ctx, actor, policy.ID, true, 0); !errors.Is(err, ErrPolicyConflict) {
		t.Fatal("stale save won", err)
	}
	p.mu.Lock()
	before := p.requests
	p.mu.Unlock()
	page, err := s.SelectedSources(ctx, a, g, fixtureMedia, "")
	if err != nil || len(page.Sources) != 0 || page.Addons[0].Enabled {
		t.Fatal(page, err)
	}
	if _, err = s.CreateSession(ctx, a, g, fixtureMedia, source); !errors.Is(err, ErrSelection) {
		t.Fatal("old selection bypassed disabling", err)
	}
	if _, err = s.SetFavorite(ctx, a, g, source, true); !errors.Is(err, ErrSelection) {
		t.Fatal("disabled source changed favorite", err)
	}
	p.mu.Lock()
	after := p.requests
	p.mu.Unlock()
	if after != before {
		t.Fatal("disabled addon was queried")
	}
	if _, _, err = s.playback(ctx, g, session.ID); err != nil {
		t.Fatal("valid pinned playback interrupted", err)
	}
	other := secondViewer(t, s, a)
	if _, err = s.UpdatePolicy(ctx, accounts.Identity{ID: other.User.ID, Version: other.User.Version}, policy.ID, true, 1); !errors.Is(err, accounts.ErrForbidden) {
		t.Fatal("member changed global policy", err)
	}
	restarted := New(s.pool, s.remote, s.http, nil)
	values, err := restarted.Policies(ctx)
	if err != nil || values[0].Enabled || values[0].Revision != 1 {
		t.Fatal("policy lost on restart", err)
	}
	// Hold a new request's media preparation, disable during it, and verify the
	// session publication fence rather than trusting the earlier policy read.
	if _, err = s.UpdatePolicy(ctx, actor, policy.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	source = choose(t, s, a, g, fixtureMedia)
	p.mu.Lock()
	p.entered = make(chan struct{}, 1)
	p.release = make(chan struct{})
	entered, release := p.entered, p.release
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, e := s.CreateSession(ctx, a, g, fixtureMedia, source); done <- e }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation not entered")
	}
	if _, err = s.UpdatePolicy(ctx, actor, policy.ID, false, 2); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; !errors.Is(err, ErrSelection) {
		t.Fatal("late session published disabled choice", err)
	}
	// No abandoned transaction may retain a policy lock.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	if err = policyLock(ctx, tx, true); err != nil {
		t.Fatal(err)
	}
}

func TestRecentRotationSerializesAcrossTransactionsAndRollback(t *testing.T) {
	s, a, g, _, p := fixture(t)
	ctx := context.Background()
	scope := "movie:" + fixtureMedia.ID
	type entry struct {
		session, event string
		at             time.Time
		key            string
	}
	entries := []entry{}
	for i := 1; i <= 5; i++ {
		p.mu.Lock()
		p.variant = i
		p.mu.Unlock()
		page, err := s.SelectedSources(ctx, a, g, fixtureMedia, "")
		if err != nil {
			t.Fatal(err)
		}
		session, err := s.CreateSession(ctx, a, g, fixtureMedia, page.Sources[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		event := fmt.Sprintf("cccccccc-0000-4000-8000-%012d", i)
		if _, err = s.pool.Exec(ctx, `INSERT INTO native_watch_events(id,user_id,session_id,sequence,envelope,outcome) VALUES($1,$2,$3,1,'{}','applied')`, event, g.User.ID, session.ID); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry{session.ID, event, time.Now().UTC().Add(time.Duration(i) * time.Second), page.Sources[0].FavoriteKey})
	}
	record := func(e entry, commit bool) error {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer database.Rollback(tx)
		if err = sourceprefs.RecordStarted(ctx, tx, e.session, e.event, e.at); err != nil {
			return err
		}
		if commit {
			return tx.Commit(ctx)
		}
		return tx.Rollback(ctx)
	}
	if err := record(entries[3], true); err != nil {
		t.Fatal(err)
	}
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(lock)
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "recent-sources:"+scope); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 3)
	for _, e := range entries[:3] {
		go func(e entry) { done <- record(e, true) }(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked int
		err = s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock(hashtextextended($1,0))%' AND pid!=pg_backend_pid()`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers never reached the held production recent lock", blocked)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err = record(entries[4], false); err != nil {
		t.Fatal(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT source_key FROM native_recent_sources WHERE scope=$1 ORDER BY last_used_at DESC,source_key DESC`, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys[0] != entries[3].key || keys[1] != entries[2].key || keys[2] != entries[1].key {
		t.Fatal("commit order, old timestamps or rollback changed rotation", keys)
	}
}
