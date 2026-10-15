package catalog

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"testing"
)

func metadataStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.New(t)
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	p, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return New(p)
}
func metadataExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

type metadataFake struct {
	title cinemeta.Movie
	err   error
}

func (f metadataFake) Movie(context.Context, string) (cinemeta.Movie, error) { return f.title, f.err }
func (f metadataFake) Series(context.Context, string) (cinemeta.Series, error) {
	return cinemeta.Series{Movie: f.title}, f.err
}

func TestMetadataEnrichmentPreservesExistingFields(t *testing.T) {
	ctx := context.Background()
	s := metadataStore(t)
	metadataExec(t, s, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt0289043','movie','{"id":"tt0289043","type":"movie","name":"Keep this name","year":"","poster":"keep.jpg","genres":[],"custom":"preserved"}')`)
	title := cinemeta.Movie{ID: "tt0289043", Type: "movie", Name: "28 Days Later", Year: "2002", Description: "Description", Poster: "new.jpg", Genres: []string{"Horror"}}
	w := metadataWorker{store: s, reader: metadataFake{title: title}}
	if err := w.Work(ctx, &river.Job[metadataArgs]{Args: metadataArgs{ID: title.ID, Type: title.Type}}); err != nil {
		t.Fatal(err)
	}
	var year, name, poster, custom string
	if err := s.pool.QueryRow(ctx, `SELECT metadata->>'year',metadata->>'name',metadata->>'poster',metadata->>'custom' FROM catalog_media`).Scan(&year, &name, &poster, &custom); err != nil {
		t.Fatal(err)
	}
	if year != "2002" || name != "Keep this name" || poster != "keep.jpg" || custom != "preserved" {
		t.Fatal(year, name, poster, custom)
	}
	title.Year = "2099"
	if err := s.EnrichTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT metadata->>'year' FROM catalog_media`).Scan(&year); err != nil || year != "2002" {
		t.Fatal(year, err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM watch_current)+(SELECT count(*) FROM watch_history)+(SELECT count(*) FROM media_mappings)`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestMetadataSeriesAndFailures(t *testing.T) {
	ctx := context.Background()
	s := metadataStore(t)
	metadataExec(t, s, `INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"id":"tt1245698","type":"series","name":"Birdy","year":""}')`)
	metadataExec(t, s, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt1245698:1:1','episode','{"name":"Episode one","series":{"id":"tt1245698","type":"series","name":"Birdy","year":""}}')`)
	a := metadataArgs{ID: "tt1245698", Type: "series"}
	w := metadataWorker{store: s, reader: metadataFake{err: errors.New("offline")}}
	if err := w.Work(ctx, &river.Job[metadataArgs]{Args: a}); err == nil {
		t.Fatal("failure lost")
	}
	title := cinemeta.Movie{ID: a.ID, Type: a.Type, Year: "2008"}
	w.reader = metadataFake{title: title}
	if err := w.Work(ctx, &river.Job[metadataArgs]{Args: a}); err != nil {
		t.Fatal(err)
	}
	var top, nested, name string
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT metadata->>'year' FROM catalog_series),metadata->'series'->>'year',metadata->>'name' FROM catalog_media`).Scan(&top, &nested, &name); err != nil {
		t.Fatal(err)
	}
	if top != "2008" || nested != "2008" || name != "Episode one" {
		t.Fatal(top, nested, name)
	}
	w.reader = metadataFake{title: cinemeta.Movie{ID: "tt9999999", Type: "series", Year: "2099"}}
	if err := w.Work(ctx, &river.Job[metadataArgs]{Args: a}); !errors.Is(err, cinemeta.ErrIncomplete) {
		t.Fatal(err)
	}
}
func TestMetadataDispatchDurableDeduplication(t *testing.T) {
	ctx := context.Background()
	s := metadataStore(t)
	var schema string
	if err := s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(s.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	metadataExec(t, s, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt0289043','movie','{"year":""}'),('tt1345836','movie','{"year":"2012"}'),('tt1245698:1:1','episode','{"series":{"year":""}}'),('tt1245698:1:2','episode','{"series":{"year":""}}')`)
	w := metadataDispatchWorker{pool: s.pool}
	for i := 0; i < 2; i++ {
		if err := w.Work(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='catalog_metadata'`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestRatingsCacheUnratedAndFailedRefresh(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	score := 7.5
	title := cinemeta.Movie{ID: "tt0289043", Type: "movie", IMDbRating: &score}
	if err := s.EnrichTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	title.ID = "tt1345836"
	title.IMDbRating = nil
	if err := s.EnrichTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	values, err := s.Ratings(ctx, "movie", []string{"tt0289043", "tt1345836", "tt9999999"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[0].Value == nil || *values[0].Value != 7.5 || values[0].Pending || values[1].Value != nil || values[1].Pending || !values[2].Pending {
		t.Fatal(values)
	}
	metadataExec(t, s, `UPDATE catalog_ratings SET checked_at=now()-interval '8 days' WHERE title_id='tt0289043'`)
	w := ratingWorker{metadata: &metadataWorker{store: s, reader: metadataFake{err: errors.New("offline")}}}
	if err = w.Work(ctx, &river.Job[ratingArgs]{Args: ratingArgs{ID: "tt0289043", Type: "movie"}}); err == nil {
		t.Fatal("lost failure")
	}
	values, err = s.Ratings(ctx, "movie", []string{"tt0289043"}, false)
	if err != nil || !values[0].Pending || values[0].Value == nil || *values[0].Value != 7.5 {
		t.Fatal(values, err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM catalog_media`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rating created library identity", count, err)
	}
}
func TestRatingJobsDeduplicateAndFillCache(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	var schema string
	if err := s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(s.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.Ratings(ctx, "series", []string{"tt0409591"}, true); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='catalog_rating'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	score := 8.4
	w := ratingWorker{metadata: &metadataWorker{store: s, reader: metadataFake{title: cinemeta.Movie{ID: "tt0409591", Type: "series", IMDbRating: &score}}}}
	if err = w.Work(ctx, &river.Job[ratingArgs]{Args: ratingArgs{ID: "tt0409591", Type: "series"}}); err != nil {
		t.Fatal(err)
	}
	values, err := s.Ratings(ctx, "series", []string{"tt0409591"}, true)
	if err != nil || values[0].Value == nil || *values[0].Value != 8.4 || values[0].Pending {
		t.Fatal(values, err)
	}
}
