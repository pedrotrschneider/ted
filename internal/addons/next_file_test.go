package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type nextRDProtocol struct {
	mu                      sync.Mutex
	adds, selects, requests int
	selected                bool
	entered, release        chan struct{}
}

func (f *nextRDProtocol) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/rest/1.0/")
	f.mu.Lock()
	f.requests++
	selected := f.selected
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	torrent := func(id string) map[string]any {
		return map[string]any{"id": id, "filename": "series", "bytes": 2000, "status": "downloaded", "progress": 100, "added": "2026-11-20T00:00:00Z", "hash": strings.Repeat("a", 40)}
	}
	switch {
	case path == "user":
		w.Write([]byte(`{"id":1}`))
	case path == "torrents":
		w.Header().Set("X-Total-Count", "1")
		json.NewEncoder(w).Encode([]any{torrent("ORIGINAL")})
	case path == "torrents/addMagnet":
		r.ParseForm()
		if r.Form.Get("magnet") != "magnet:?xt=urn:btih:"+strings.Repeat("a", 40) {
			http.Error(w, "wrong magnet", 400)
			return
		}
		f.mu.Lock()
		f.adds++
		f.mu.Unlock()
		if f.entered != nil {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"NEXT"}`))
	case path == "torrents/selectFiles/NEXT":
		r.ParseForm()
		if r.Form.Get("files") != "2" {
			http.Error(w, "wrong selection", 400)
			return
		}
		f.mu.Lock()
		f.selected = true
		f.selects++
		f.mu.Unlock()
		w.WriteHeader(204)
	case path == "torrents/info/NEXT" || path == "torrents/info/ORIGINAL":
		id := strings.TrimPrefix(path, "torrents/info/")
		value := torrent(id)
		first, second := 0, 0
		links := []string{}
		if id == "ORIGINAL" {
			first = 1
			links = append(links, "https://real-debrid.com/d/ORIGINAL-1")
		} else if selected {
			second = 1
			links = append(links, "https://real-debrid.com/d/NEXT-2")
		} else {
			value["status"] = "waiting_files_selection"
			value["progress"] = 0
		}
		value["files"] = []any{map[string]any{"id": 1, "path": "/episode1.mkv", "bytes": 1000, "selected": first}, map[string]any{"id": 2, "path": "/episode2.mkv", "bytes": 1000, "selected": second}}
		value["links"] = links
		json.NewEncoder(w).Encode(value)
	case path == "unrestrict/link":
		r.ParseForm()
		if r.Form.Get("link") != "https://real-debrid.com/d/NEXT-2" {
			http.Error(w, "wrong link", 400)
			return
		}
		w.Write([]byte(`{"download":"https://fixture.download.real-debrid.com/media","filename":"episode2.mkv","filesize":1000}`))
	default:
		http.Error(w, "unexpected provider request", 400)
	}
}

type nextTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t nextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	copy.URL = &u
	copy.Host = t.target.Host
	return t.base.RoundTrip(copy)
}

func nextFileFixture(t *testing.T) (*Service, *accounts.NativeAuth, accounts.NativeGrant, selected, string, *nextRDProtocol) {
	t.Helper()
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	id := choose(t, s, auth, g, fixtureMedia)
	v, err := s.selectedForPlayback(ctx, g, fixtureMedia, id)
	if err != nil {
		t.Fatal(err)
	}
	index := int64(1)
	v.source.InfoHash = strings.Repeat("a", 40)
	v.source.FileIndex = &index
	v.source.Filename = "episode2.mkv"
	v.source.VideoSize = 1000
	raw, _ := json.Marshal(v.source)
	if _, err = s.pool.Exec(ctx, `UPDATE native_addon_sources SET source=$2 WHERE id=$1`, id, raw); err != nil {
		t.Fatal(err)
	}
	store := settings.New(s.pool)
	if err = store.Initialize(ctx, settings.Credentials{RDKey: "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	migrations, err := rivermigrate.New(riverpgxv5.New(s.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrations.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	protocol := &nextRDProtocol{}
	server := httptest.NewServer(protocol)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client := realdebrid.NewWithHTTPClient("fixture-key", &http.Client{Transport: nextTransport{http.DefaultTransport, target}})
	s.RD, err = realdebrid.NewInventory(s.pool, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.nextSchema = schema
	s.next, err = river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	return s, auth, g, v, id, protocol
}
func queuedNextFile(t *testing.T, s *Service, operation string) *river.Job[nextFileArgs] {
	t.Helper()
	var raw []byte
	var id int64
	if err := s.pool.QueryRow(context.Background(), `SELECT id,args FROM `+pgx.Identifier{s.nextSchema, "river_job"}.Sanitize()+` WHERE kind='next_episode_rd_file' AND args->>'operation_id'=$1`, operation).Scan(&id, &raw); err != nil {
		t.Fatal(err)
	}
	var args nextFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	return &river.Job[nextFileArgs]{Args: args}
}
func TestNextFileJobPersistsDeduplicatesAndSelectsOnlyRequestedFile(t *testing.T) {
	s, auth, g, v, id, rd := nextFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	operation := "11111111-1111-4111-8111-111111111111"
	for range 2 {
		if err := s.requestNextFile(ctx, auth, g, v, id, operation); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{s.nextSchema, "river_job"}.Sanitize()+` WHERE kind='next_episode_rd_file'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	job := queuedNextFile(t, s, operation)
	worker := &nextFileWorker{s: s}
	var leaseSeconds float64
	if err := s.pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-clock_timestamp()) FROM native_next_preparations WHERE operation_id=$1`, operation).Scan(&leaseSeconds); err != nil || leaseSeconds > 150 || leaseSeconds < 140 {
		t.Fatal("device intent lacks bounded lease", leaseSeconds, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE native_next_preparations SET expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	if err := worker.Work(ctx, job); err == nil {
		t.Fatal("abandoned device intent reached RD")
	}
	rd.mu.Lock()
	calls := rd.requests
	rd.mu.Unlock()
	if calls != 0 {
		t.Fatal("expired lease reached provider", calls)
	}
	if err := s.requestNextFile(ctx, auth, g, v, id, operation); err != nil {
		t.Fatal(err)
	}
	rd.entered = make(chan struct{}, 1)
	rd.release = make(chan struct{})
	done := make(chan error, 2)
	go func() { done <- worker.Work(ctx, job) }()
	select {
	case <-rd.entered:
	case <-ctx.Done():
		t.Fatal("RD add was not reached")
	}
	go func() { done <- worker.Work(ctx, job) }()
	close(rd.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	rd.mu.Lock()
	adds, selects := rd.adds, rd.selects
	rd.mu.Unlock()
	if adds != 1 || selects != 1 {
		t.Fatal("duplicated RD effect", adds, selects)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&count); err != nil || count != 0 {
		t.Fatal("preparation created viewing", count, err)
	}
}
func TestNextFileCancellationIsOperationScopedAndRestartRechecksFamily(t *testing.T) {
	s, auth, g, v, id, rd := nextFileFixture(t)
	ctx := context.Background()
	operation := "11111111-1111-4111-8111-111111111111"
	if err := s.requestNextFile(ctx, auth, g, v, id, operation); err != nil {
		t.Fatal(err)
	}
	s.DiscardPreparationOperation(g, id, "22222222-2222-4222-8222-222222222222")
	var state string
	table := pgx.Identifier{s.nextSchema, "river_job"}.Sanitize()
	if err := s.pool.QueryRow(ctx, `SELECT state::text FROM `+table+` WHERE kind='next_episode_rd_file'`).Scan(&state); err != nil || state != "available" {
		t.Fatal("old cancellation affected successor", state, err)
	}
	job := queuedNextFile(t, s, operation)
	if err := auth.Logout(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.pool, s.remote, s.http, nil)
	restarted.RD = s.RD
	if err := (&nextFileWorker{s: restarted}).Work(ctx, job); err == nil {
		t.Fatal("revoked durable work ran after restart")
	}
	rd.mu.Lock()
	calls := rd.requests
	rd.mu.Unlock()
	if calls != 0 {
		t.Fatal("revoked job reached provider", calls)
	}
	s.DiscardPreparationOperation(g, id, operation)
	if err := s.pool.QueryRow(ctx, `SELECT state::text FROM `+table+` WHERE kind='next_episode_rd_file'`).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("intent cancellation was not durable", state, err)
	}
}

func TestNextFileRunnerExecutesPersistedIntentAndStopsCleanly(t *testing.T) {
	s, auth, g, v, id, rd := nextFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.StartNextPreparations(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.StopNextPreparations(stop); err != nil {
			t.Error(err)
		}
	}()
	completed, unsubscribe := s.next.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	if err := s.requestNextFile(ctx, auth, g, v, id, "33333333-3333-4333-8333-333333333333"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("persisted next-file intent was not executed")
	}
	rd.mu.Lock()
	adds, selects := rd.adds, rd.selects
	rd.mu.Unlock()
	if adds != 1 || selects != 1 {
		t.Fatal("runner did not execute the exact-file effects", adds, selects)
	}
	file, link, err := s.RD.ResolveAvailableSelection(ctx, torrentSelection(v.source))
	if err != nil || file.Path != "/episode2.mkv" || link.Bytes != 1000 {
		t.Fatal("completed job did not publish a ready exact file", file, link.Bytes, err)
	}
}

func TestNextFilePreparationQueuesOnlyForPlaybackOptIn(t *testing.T) {
	s, auth, g, _, id, rd := nextFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	operation := "44444444-4444-4444-8444-444444444444"
	table := pgx.Identifier{s.nextSchema, "river_job"}.Sanitize()
	for _, allow := range []bool{false, true} {
		err := s.PrepareSourceOperation(ctx, auth, g, fixtureMedia, id, operation, allow)
		if !errors.Is(err, realdebrid.ErrPending) {
			t.Fatal("missing exact file lost its readiness signal", err)
		}
		var jobs int
		if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE kind='next_episode_rd_file'`).Scan(&jobs); err != nil {
			t.Fatal(err)
		}
		want := 0
		if allow {
			want = 1
		}
		if jobs != want {
			t.Fatal("RD effects were not restricted to playback opt-in", allow, jobs)
		}
	}
	rd.mu.Lock()
	adds, selects := rd.adds, rd.selects
	rd.mu.Unlock()
	if adds != 0 || selects != 0 {
		t.Fatal("request performed effects outside the durable worker", adds, selects)
	}
}
