package httpapi

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/realdebrid"
	"errors"
	"fmt"
	"testing"
	"time"
)

type seriesStub struct{ count int }

func (stub seriesStub) Series(context.Context, string) (cinemeta.Series, error) {
	if stub.count > 0 {
		show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1245698", Type: "series", Name: "Birdy", Genres: []string{}}}
		for n := 1; n <= stub.count; n++ {
			show.Episodes = append(show.Episodes, cinemeta.Episode{ID: fmt.Sprintf("tt1245698:1:%d", n), Name: fmt.Sprint(n), Season: 1, Number: n, ReleaseDate: "2008-07-01"})
		}
		return show, nil
	}
	return cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1245698", Type: "series", Name: "Birdy", Genres: []string{}}, Episodes: []cinemeta.Episode{{ID: "tt1245698:1:1", Name: "One", Season: 1, Number: 1, ReleaseDate: "2008-07-01"}, {ID: "tt1245698:1:2", Name: "Two", Season: 1, Number: 2, ReleaseDate: "2008-07-08"}}}, nil
}
func (s seriesStub) BrowseSeries(ctx context.Context, _ string, skip int, _ cinemeta.Filters) (cinemeta.SeriesPage, error) {
	v, _ := s.Series(ctx, "")
	return cinemeta.SeriesPage{Series: []cinemeta.Movie{v.Movie}, Skip: skip}, nil
}

const seriesMappingPath = "/api/admin/series/tt1245698/mappings"

func TestEpisodeMappingsSerializeAcrossWriters(t *testing.T) {
	f, rd, selection := mappingSetup(t)
	reader := seriesStub{}
	ctx := context.Background()
	other, err := f.service.Create(ctx, identity(f.admin), "otheradmin", "Other", testPassword, "admin")
	if err != nil {
		t.Fatal(err)
	}
	second := selection
	second.FileID = 4
	second.Path = "/alternative.mkv"
	rd.detail.Files = append(rd.detail.Files, realdebrid.File{ID: 4, Path: second.Path, Bytes: second.Bytes, Video: true})
	if _, err = f.pool.Exec(ctx, `INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{}')`); err != nil {
		t.Fatal(err)
	}
	held, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err = held.Exec(ctx, `SELECT id FROM catalog_series WHERE cinemeta_id='tt1245698' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i, sel := range []catalog.Selection{selection, second} {
		who := identity(f.admin)
		if i == 1 {
			who = identity(other)
		}
		go func() {
			results <- f.api.catalog.SaveEpisodes(ctx, who, "tt1245698", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: "tt1245698:1:1", Selection: sel}}}, reader, rd)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id::text,category FROM catalog_series%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("writers did not reach the held series lock")
	}
	if err = held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, catalog.ErrConflict)) || (b == nil && errors.Is(a, catalog.ErrConflict))) {
		t.Fatalf("concurrent outcomes %v %v", a, b)
	}
	mapped, err := f.api.catalog.EpisodeMappings(ctx, "tt1245698")
	if err != nil || len(mapped.Mappings) != 1 {
		t.Fatal(mapped, err)
	}
}
