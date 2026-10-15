package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/testdb"
)

const probeJSON = `{"format":{"format_name":"matroska,webm","duration":"3600","size":"1000"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080}]}`

type resolver struct {
	calls   atomic.Int32
	url     string
	enter   chan struct{}
	release chan struct{}
}

func (r *resolver) CredentialScope() string { return "test-account" }
func (r *resolver) ResolveFile(ctx context.Context, f realdebrid.FileRequest) (realdebrid.Resolved, error) {
	r.calls.Add(1)
	if r.enter != nil {
		select {
		case r.enter <- struct{}{}:
		default:
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return realdebrid.Resolved{}, ctx.Err()
		}
	}
	return realdebrid.Resolved{URL: r.url, Bytes: f.Bytes, Filename: "movie.mkv"}, nil
}
func fixture(t *testing.T, r Resolver) (*Service, Media, accounts.Identity) {
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
	s, err := New(ctx, pool, r, slog.New(slog.NewTextHandler(io.Discard, nil)), 2)
	if err != nil {
		t.Fatal(err)
	}
	var who accounts.Identity
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('admin','Admin','unused','admin') RETURNING id,session_version`).Scan(&who.ID, &who.Version); err != nil {
		t.Fatal(err)
	}
	m := Media{Name: "Movie", IMDB: "tt1345836", Year: "2012", FileRequest: realdebrid.FileRequest{AccountID: "123", TorrentID: "T1", Hash: strings.Repeat("a", 40), Path: "/movie.mkv", Bytes: 1000}}
	if err = pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata) VALUES($1,'{"name":"Movie","year":"2012"}') RETURNING id::text`, m.IMDB).Scan(&m.ID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,'123','T1',$2,42,'/movie.mkv',1000,'{}',$3) RETURNING id::text`, m.ID, m.Hash, who.ID).Scan(&m.MappingID); err != nil {
		t.Fatal(err)
	}
	return s, m, who
}
func awaitState(t *testing.T, s *Service, phase string) State {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.Status(context.Background(), "tt1345836")
		if e != nil {
			t.Fatal(e)
		}
		if v.Phase == phase {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := s.Status(context.Background(), "tt1345836")
	t.Fatalf("state did not become %s: %+v", phase, v)
	return State{}
}
func start(t *testing.T, s *Service) {
	t.Helper()
	if e := s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if e := s.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
}

func TestDurablePreparationPublicationAndAuthorization(t *testing.T) {
	rd := &resolver{url: "https://test.download.real-debrid.com/private"}
	s, m, who := fixture(t, rd)
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return json.RawMessage(probeJSON), nil }
	if e := s.Enqueue(context.Background(), accounts.Identity{ID: who.ID, Version: who.Version + 1}, m.IMDB); e == nil {
		t.Fatal("stale session enqueued")
	}
	if e := s.Enqueue(context.Background(), who, m.IMDB); e != nil {
		t.Fatal(e)
	}
	if e := s.Enqueue(context.Background(), who, m.IMDB); e != nil {
		t.Fatal(e)
	}
	var jobs int
	if e := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM river_job").Scan(&jobs); e != nil || jobs != 1 {
		t.Fatalf("duplicate enqueue: %d %v", jobs, e)
	}
	// Queue persists before any worker exists. A newly constructed process executes it.
	replacement, e := New(context.Background(), s.pool, rd, s.logger, 2)
	if e != nil {
		t.Fatal(e)
	}
	replacement.probe = s.probe
	start(t, replacement)
	state := awaitState(t, replacement, "ready")
	if !state.Published {
		t.Fatal("not published")
	}
	var jobState string
	if e = s.pool.QueryRow(context.Background(), "SELECT state FROM river_job WHERE id=$1", state.JobID).Scan(&jobState); e != nil || jobState != "completed" {
		t.Fatalf("job not atomically complete: %s %v", jobState, e)
	}
}

func TestCancellationCannotPublishAfterProbe(t *testing.T) {
	s, m, who := fixture(t, &resolver{url: "https://test.download.real-debrid.com/private"})
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s.probe = func(ctx context.Context, _ Media) (json.RawMessage, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(probeJSON), nil
	}
	if e := s.Enqueue(context.Background(), who, m.IMDB); e != nil {
		t.Fatal(e)
	}
	start(t, s)
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("worker not started")
	}
	if e := s.Cancel(context.Background(), who, m.IMDB); e != nil {
		t.Fatal(e)
	}
	v := awaitState(t, s, "cancelled")
	if v.Published {
		t.Fatal("cancel published")
	}
}
func TestConcurrentLinkRenewalUsesDatabaseLock(t *testing.T) {
	rd := &resolver{url: "https://test.download.real-debrid.com/new", enter: make(chan struct{}, 1), release: make(chan struct{})}
	s, m, _ := fixture(t, rd)
	other, err := New(context.Background(), s.pool, rd, s.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, instance := range []*Service{s, other} {
		wg.Go(func() {
			link, e := instance.link(context.Background(), m, "expired")
			if e == nil && link != rd.url {
				e = fmt.Errorf("wrong link")
			}
			errs <- e
		})
	}
	select {
	case <-rd.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("resolver did not enter")
	}
	close(rd.release)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if rd.calls.Load() != 1 {
		t.Fatal("concurrent renewal duplicated upstream operation")
	}
}
func TestRangesRenewalAndReconnect(t *testing.T) {
	var rejected atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/expired" {
			rejected.Add(1)
			w.WriteHeader(403)
			return
		}
		want, e := parseRange(r.Header.Get("Range"), 1000)
		if e != nil {
			t.Error(e)
			w.WriteHeader(416)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/1000", want.start, want.end))
		w.Header().Set("Content-Length", fmt.Sprint(want.end-want.start+1))
		w.WriteHeader(206)
		for i := want.start; i <= want.end; i++ {
			w.Write([]byte{byte(i % 251)})
		}
	}))
	defer upstream.Close()
	rd := &resolver{url: upstream.URL + "/valid"}
	s, m, _ := fixture(t, rd)
	s.mediaHTTP = upstream.Client()
	_, e := s.pool.Exec(context.Background(), `INSERT INTO streaming_links VALUES($1,$2,$3,now()+interval '1 hour')`, m.MappingID, rd.CredentialScope(), upstream.URL+"/expired")
	if e != nil {
		t.Fatal(e)
	}
	for _, span := range []string{"bytes=0-31", "bytes=400-431", "bytes=-32"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Range", span)
		s.sendMedia(w, req, m)
		if w.Code != 206 || w.Body.Len() != 32 {
			t.Fatalf("range %s: %d %d", span, w.Code, w.Body.Len())
		}
		wanted, _ := parseRange(span, m.Bytes)
		for i, b := range w.Body.Bytes() {
			if b != byte((wanted.start+int64(i))%251) {
				t.Fatal("wrong range bytes")
			}
		}
	}
	if rejected.Load() != 1 || rd.calls.Load() != 1 {
		t.Fatal("link renewal not reused")
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/", nil)
		if method == "GET" {
			req.Header.Set("Range", "bytes=1000-")
		}
		s.sendMedia(w, req, m)
		if method == "GET" && w.Code != 416 {
			t.Fatal(w.Code)
		}
		if method == "HEAD" && (w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "1000") {
			t.Fatal("HEAD contract")
		}
	}
}
func TestProbeValidation(t *testing.T) {
	for _, test := range []struct {
		raw   string
		valid bool
	}{{probeJSON, true}, {strings.Replace(probeJSON, "3600", "NaN", 1), false}, {strings.Replace(probeJSON, "1000", "999", 1), false}, {strings.Replace(probeJSON, "video", "audio", 1), false}} {
		if (validateProbe([]byte(test.raw), 1000) == nil) != test.valid {
			t.Fatal("probe validation contract")
		}
	}
}
