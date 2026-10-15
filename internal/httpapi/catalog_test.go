package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/realdebrid"
)

type movieStub struct {
	err    error
	during func()
}

func (s *movieStub) Movie(context.Context, string) (cinemeta.Movie, error) {
	if s.during != nil {
		s.during()
	}
	return cinemeta.Movie{ID: "tt1345836", Type: "movie", Name: "The Dark Knight Rises", Year: "2012", Genres: []string{}}, s.err
}
func (s *movieStub) Browse(ctx context.Context, _ string, skip int, _ cinemeta.Filters) (cinemeta.Page, error) {
	m, err := s.Movie(ctx, "")
	return cinemeta.Page{Movies: []cinemeta.Movie{m}, Skip: skip}, err
}

type mappingRD struct {
	rdStub
	detail  realdebrid.Detail
	account string
	before  func()
}

func (s *mappingRD) AccountID(context.Context) (string, error) { return s.account, nil }
func (s *mappingRD) Torrent(context.Context, string) (realdebrid.Detail, error) {
	if s.before != nil {
		s.before()
	}
	return s.detail, s.fail
}
func mappingSetup(t *testing.T) (*fixture, *mappingRD, catalog.Selection) {
	f := setup(t)
	f.api.movies = &movieStub{}
	file := realdebrid.File{ID: 3, Path: "/movie.mkv", Bytes: 25275015542, Selected: true, Video: true}
	rd := &mappingRD{account: "1234", detail: realdebrid.Detail{Torrent: realdebrid.Torrent{ID: "T1", Name: "Original.Release", Added: time.Now(), Status: "downloaded", Progress: 100}, Hash: strings.Repeat("a", 40), Files: []realdebrid.File{file}}}
	f.api.rd = rd
	return f, rd, catalog.Selection{TorrentID: "T1", Hash: rd.detail.Hash, FileID: file.ID, Path: file.Path, Bytes: file.Bytes}
}

const mappingPath = "/api/admin/movies/tt1345836/mapping"

func TestCatalogErrorsAreBoundedAndDistinct(t *testing.T) {
	f, _, _ := mappingSetup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{cinemeta.ErrIncomplete, 502, "catalog_incomplete"}, {cinemeta.ErrNotFound, 404, "catalog_not_found"}, {errors.New("secret-provider-url"), 503, ""}} {
		f.api.movies = &movieStub{err: tc.err}
		r := call(t, h, "GET", "/api/movies", nil, admin)
		requireStatus(t, r, tc.status)
		if !strings.Contains(r.Body.String(), tc.code) || strings.Contains(r.Body.String()+f.logs.String(), "secret-provider-url") {
			t.Fatal("unsafe or incorrect error")
		}
	}
}

func TestConcurrentMappingsSerializeOnPersistentMovieLock(t *testing.T) {
	f, rd, first := mappingSetup(t)
	ctx := context.Background()
	other, err := f.service.Create(ctx, identity(f.admin), "otheradmin", "Other", testPassword, "admin")
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.FileID = 4
	second.Path = "/other.mkv"
	rd.detail.Files = append(rd.detail.Files, realdebrid.File{ID: 4, Path: second.Path, Bytes: second.Bytes, Video: true})
	metadata, _ := json.Marshal(cinemeta.Movie{ID: "tt1345836", Type: "movie", Name: "The Dark Knight Rises", Genres: []string{}})
	if _, err = f.pool.Exec(ctx, "INSERT INTO catalog_media(cinemeta_id,metadata) VALUES('tt1345836',$1)", metadata); err != nil {
		t.Fatal(err)
	}
	held, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err = held.Exec(ctx, "SELECT id FROM catalog_media WHERE cinemeta_id='tt1345836' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		m   catalog.Mapping
		err error
	}
	results := make(chan outcome, 2)
	for _, tc := range []struct {
		who       accounts.Identity
		selection catalog.Selection
	}{{identity(f.admin), first}, {identity(other), second}} {
		go func() {
			m, err := f.api.catalog.Save(ctx, tc.who, "tt1345836", tc.selection, f.api.movies, rd)
			results <- outcome{m, err}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id::text FROM catalog_media%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("workers did not reach held PostgreSQL lock")
	}
	if err = held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || !errors.Is(b.err, catalog.ErrConflict) {
		t.Fatal("concurrent writes were not serialized", a.err, b.err)
	}
	stored, err := f.api.catalog.Get(ctx, "tt1345836")
	if err != nil || stored.ID != a.m.ID || stored.Selection != a.m.Selection {
		t.Fatal("winning mapping was replaced", err)
	}
}
