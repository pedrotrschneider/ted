package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type fixtureReader struct {
	fetch   func(context.Context, string, Request) (Document, error)
	resolve func(context.Context, string, string, string) (string, error)
	artwork func(context.Context, string, Request) ([]ArtworkChoice, error)
}

func (*fixtureReader) Enabled(string) bool { return true }
func (r *fixtureReader) Artwork(ctx context.Context, p string, v Request) ([]ArtworkChoice, error) {
	if r.artwork == nil {
		return nil, ErrProvider
	}
	return r.artwork(ctx, p, v)
}
func (r *fixtureReader) Fetch(ctx context.Context, p string, v Request) (Document, error) {
	if r.fetch == nil {
		return Document{}, ErrProvider
	}
	return r.fetch(ctx, p, v)
}
func (r *fixtureReader) ResolveCredit(ctx context.Context, p, k, id string) (string, error) {
	if r.resolve == nil {
		return "", ErrProvider
	}
	return r.resolve(ctx, p, k, id)
}
func metadataFixture(t *testing.T, r Reader) *Service {
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
	var schema string
	if err = pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	s, err := New(pool, r, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func runEntity(t *testing.T, s *Service, id string) {
	t.Helper()
	v, err := s.readEntity(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&worker{s: s}).Work(context.Background(), &river.Job[Args]{Args: Args{ID: id, Generation: v.Generation}}); err != nil {
		t.Fatal(err)
	}
}
func movieDocument(name string) Document {
	return Document{ProviderID: "49026", Item: Item{Name: name, Overview: "Overview", ProviderIDs: map[string]string{"Imdb": "tt1345836", "Tmdb": "49026"}}, People: []PersonSummary{{Provider: "Tmdb", ID: "3894", Name: "Christian Bale", Role: "Bruce Wayne", Type: "Actor", Image: "https://image.tmdb.org/t/p/original/actor.jpg"}}, Images: []Image{{Type: "Primary", URL: "https://image.tmdb.org/t/p/original/poster.jpg"}}}
}

func TestMetadataLazyCacheConcurrentRequestsAndPartialRefresh(t *testing.T) {
	ctx := context.Background()
	doc := movieDocument("Enriched")
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return doc, nil }}
	s := metadataFixture(t, r)
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie", Name: "Cinemeta"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 6)
	for range 6 {
		go func() {
			_, e := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie", Name: "Cinemeta"})
			done <- e
		}()
	}
	for range 6 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	var jobs int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='native_metadata_enrichment'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("duplicate durable detail jobs", jobs, err)
	}
	runEntity(t, s, v.Item.ID)
	ready, err := s.Get(ctx, v.Item.ID)
	if err != nil || ready.Status != "ready" || len(ready.Item.People) != 1 {
		t.Fatal(ready, err)
	}
	person := ready.Item.People[0].ID
	p, err := s.Get(ctx, person)
	if err != nil || p.Status != "new" || p.Generation != 0 || p.Item.Overview != "" {
		t.Fatal("cast eagerly fetched biography", p, err)
	}
	if _, err = s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{}); err != nil {
		t.Fatal(err)
	}
	doc = Document{ProviderID: "49026", Item: Item{Name: "Refreshed"}}
	r.fetch = func(context.Context, string, Request) (Document, error) { return doc, ErrPartial }
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	e := (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{ID: v.Item.ID, Generation: 2}})
	if !errors.Is(e, ErrPartial) {
		t.Fatal(e)
	}
	partial, err := s.Get(ctx, v.Item.ID)
	if err != nil || partial.Status != "partial" || partial.Item.Name != "Refreshed" || len(partial.Item.People) != 1 || len(partial.Images) != 1 {
		t.Fatal("partial refresh discarded retained data", partial, err)
	}
	var effects int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM catalog_media)+(SELECT count(*) FROM media_mappings)+(SELECT count(*) FROM watch_current)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal("metadata created library or history", effects, err)
	}
}

func TestMetadataRefreshFencesHeldOlderWorkerAndProtectsArtwork(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	r := &fixtureReader{fetch: func(ctx context.Context, p string, req Request) (Document, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return movieDocument("Older"), nil
		}
		return movieDocument("Newer"), nil
	}}
	s := metadataFixture(t, r)
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie", Name: "Cinemeta"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{v.Item.ID, 1}}) }()
	<-entered
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	latest, err := s.Get(ctx, v.Item.ID)
	if err != nil || latest.Item.Name != "Newer" || latest.Generation != 2 {
		t.Fatal("stale enrichment won", latest, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_artwork SET user_modified=true,source_url='https://image.tmdb.org/t/p/original/user.jpg' WHERE entity_id=$1`, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	var source string
	if err = s.pool.QueryRow(ctx, `SELECT source_url FROM metadata_artwork WHERE entity_id=$1`, v.Item.ID).Scan(&source); err != nil || source != "https://image.tmdb.org/t/p/original/user.jpg" {
		t.Fatal("refresh replaced user artwork", err)
	}
}

func TestMetadataDurableQueueRestartAndSeasonIdentity(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	r := &fixtureReader{fetch: func(_ context.Context, _ string, req Request) (Document, error) {
		calls.Add(1)
		if req.Kind == "movie" {
			return movieDocument("Queued before restart"), nil
		}
		return Document{ProviderID: "100", Item: Item{Name: "Season 1"}, Children: []Child{
			{Request: Request{Kind: "episode", TitleID: req.TitleID, Season: 1, Number: 1, ProviderID: "10", EpisodeName: "Pilot", EpisodeDate: "2005-03-24"}, Image: "https://artworks.thetvdb.com/pilot.jpg"},
			{Request: Request{Kind: "episode", TitleID: req.TitleID, Season: 1, Number: 2, ProviderID: "11", EpisodeName: "Provider title", EpisodeDate: "2005-03-28"}, Image: "https://artworks.thetvdb.com/second.jpg"},
			{Request: Request{Kind: "episode", TitleID: req.TitleID, Season: 1, Number: 9, ProviderID: "19", EpisodeName: "Unlisted episode", EpisodeDate: "2005-04-01"}, Image: "https://artworks.thetvdb.com/unlisted.jpg"},
		}}, nil
	}}
	s := metadataFixture(t, r)
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie", Name: "Cinemeta"})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(s.pool, r, "", s.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop(ctx)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err = restarted.Get(ctx, v.Item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == "ready" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if v.Status != "ready" || calls.Load() != 1 {
		t.Fatal("durable queue did not survive service reconstruction", v, calls.Load())
	}
	if err = restarted.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt0386676", Type: "series", Name: "The Office", Year: "2005–2013"}, Episodes: []cinemeta.Episode{{ID: "tt0386676:1:1", Season: 1, Number: 1, Name: "Pilot", ReleaseDate: "2001-01-31"}, {ID: "tt0386676:1:2", Season: 1, Number: 2, Name: "Diversity Day", ReleaseDate: "2005-03-29"}}}
	if err = catalog.New(s.pool).SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	season, err := s.Ensure(ctx, "Tvdb", Request{Kind: "season", TitleID: show.ID, Season: 1}, Item{Type: "Season", Name: "Season 1"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, season.Item.ID)
	season, err = s.Get(ctx, season.Item.ID)
	if err != nil || season.Status != "partial" || season.Error != "identity_ambiguous" {
		t.Fatal("skipped episode association was hidden as a successful season", season, err)
	}
	ep, err := s.Lookup(ctx, Request{Kind: "episode", TitleID: show.ID, Season: 1, Number: 1})
	if err != nil || ep.Generation != 0 || len(ep.Images) != 1 || ep.Item.Name != "Pilot" {
		t.Fatal("season eagerly enriched episode or lost canonical summary", ep, err)
	}
	second, err := s.Lookup(ctx, Request{Kind: "episode", TitleID: show.ID, Season: 1, Number: 2})
	if err != nil || second.Item.Name != "Diversity Day" || len(second.Images) != 1 || second.Generation != 0 {
		t.Fatal("descriptive differences blocked lazy episode artwork", second, err)
	}
	linked, err := s.readEntity(ctx, second.Item.ID)
	if err != nil || linked.ProviderID != "11" {
		t.Fatal("season episode lost its provider identity", linked, err)
	}
	if _, err = s.Lookup(ctx, Request{Kind: "episode", TitleID: show.ID, Season: 1, Number: 9}); err == nil {
		t.Fatal("provider invented an episode outside TED's canonical identity")
	}
	retained, err := catalog.New(s.pool).NativeSeries(ctx, show.ID)
	if err != nil || retained.Episodes[0].ReleaseDate != "2001-01-31" {
		t.Fatal("enrichment changed the canonical schedule", retained, err)
	}
}

func TestMetadataEpisodeEnrichmentPreservesCatalogAndUsesProviderDescriptions(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(_ context.Context, _ string, req Request) (Document, error) {
		if req.EpisodeDate != "2001-01-31" || req.EpisodeName != "Pilot" || req.Season != 1 || req.Number != 1 {
			t.Fatalf("catalog identity/seed changed: %+v", req)
		}
		return Document{ProviderID: "110413", Item: Item{Name: "Provider title", Overview: "Provider episode details", PremiereDate: date("2005-03-24")}}, nil
	}}
	s := metadataFixture(t, r)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt0386676", Type: "series", Name: "The Office", Year: "2005–2013"}, Episodes: []cinemeta.Episode{{ID: "tt0386676:1:1", Season: 1, Number: 1, Name: "Pilot", ReleaseDate: "2001-01-31"}}}
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	v, err := s.Ensure(ctx, "Tvdb", Request{Kind: "episode", TitleID: show.ID, Season: 1, Number: 1, EpisodeName: "Pilot", EpisodeDate: "2001-01-31"}, Item{Type: "Episode", Name: "Pilot"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	v, err = s.Get(ctx, v.Item.ID)
	if err != nil || v.Status != "ready" || v.Item.Name != "Provider title" || v.Item.Overview != "Provider episode details" || v.Item.PremiereDate == nil || v.Item.PremiereDate.Format(time.DateOnly) != "2005-03-24" {
		t.Fatal("episode details not enriched", v, err)
	}
	retained, err := catalog.New(s.pool).NativeSeries(ctx, show.ID)
	if err != nil || retained.Episodes[0].Name != "Pilot" || retained.Episodes[0].ReleaseDate != "2001-01-31" {
		t.Fatal("provider descriptions changed catalog identity/schedule", retained, err)
	}
}

type rewriteTransport struct {
	base      string
	transport http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	v := req.Clone(req.Context())
	v.URL.Scheme = "http"
	v.URL.Host = r.base
	return r.transport.RoundTrip(v)
}
func TestArtworkAtomicCacheFailedRefreshAndModifiedInFlight(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return movieDocument("Movie"), nil }})
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie", Name: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	v, err = s.Get(ctx, v.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := v.Images[0].ID
	var data bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err = png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.Write([]byte("broken image"))
		case 2:
			close(entered)
			<-release
			w.Write(data.Bytes())
		default:
			w.Write(data.Bytes())
		}
	}))
	defer server.Close()
	s.imageHTTP = &http.Client{Transport: rewriteTransport{base: server.Listener.Addr().String(), transport: http.DefaultTransport}, Timeout: 5 * time.Second}
	asset, err := s.RequestImage(ctx, id, false)
	if err != nil || asset.Status != "queued" {
		t.Fatal(asset, err)
	}
	if err = s.fetchImage(ctx, Args{id, 1}); err != nil {
		t.Fatal(err)
	}
	f, mime, tag, err := s.ImageFile(ctx, id)
	if err != nil || mime != "image/png" {
		t.Fatal(mime, err)
	}
	f.Close()
	mode.Store(1)
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	if err = (&imageWorker{s: s}).Work(ctx, &river.Job[assetArgs]{Args: assetArgs{ID: id, Generation: 2}}); !errors.Is(err, ErrImage) {
		t.Fatal("invalid refresh published", err)
	}
	f, _, retained, err := s.ImageFile(ctx, id)
	if err != nil || retained != tag {
		t.Fatal("failed refresh discarded valid artwork", retained, err)
	}
	f.Close()
	mode.Store(2)
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	done := make(chan error, 1)
	go func() { done <- s.fetchImage(ctx, Args{id, 3}) }()
	<-entered
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_artwork SET user_modified=true,status='ready' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var modified bool
	if err = s.pool.QueryRow(ctx, `SELECT user_modified FROM metadata_artwork WHERE id=$1`, id).Scan(&modified); err != nil || !modified {
		t.Fatal("in-flight worker changed artwork ownership", err)
	}
}

func TestFilmographyWatchedSplitAndLazyIdentityFencing(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	person, err := s.Ensure(ctx, "Tmdb", Request{Kind: "person", ProviderID: "3894"}, Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.readEntity(ctx, person.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := Document{ProviderID: "3894", Item: Item{Name: "Actor"}, Credits: []Credit{{Provider: "Tmdb", Kind: "movie", ID: "1", TitleID: "tt1234567", Item: Item{Type: "Movie", Name: "Watched movie"}}, {Provider: "Tmdb", Kind: "series", ID: "2", TitleID: "tt0386676", Item: Item{Type: "Series", Name: "Partially watched show"}}, {Provider: "Tmdb", Kind: "movie", ID: "3", Item: Item{Type: "Movie", Name: "Unresolved"}}}}
	if err = s.apply(ctx, v, d, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'admin','Admin','unused','admin'),(2,'other','Other','unused','user')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tt1234567", "tt0386676:1:1"} {
		kind := "movie"
		if id != "tt1234567" {
			kind = "episode"
		}
		if _, err = s.pool.Exec(ctx, `WITH c AS(INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,'{}') RETURNING id) INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT 1,id,'{"played":true,"position_ticks":0,"play_count":1,"last_played":null}','TED',now() FROM c`, id, kind); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || len(page.Items) != 3 {
		t.Fatal(page, err)
	}
	played, unknown := 0, 0
	var unresolved string
	for _, entry := range page.Items {
		if entry.Watched == nil {
			unknown++
			unresolved = entry.Item.ID
		} else if *entry.Watched {
			played++
		}
	}
	if played != 2 || unknown != 1 {
		t.Fatal("series required full completion or unresolved became unwatched", page)
	}
	other, err := s.Filmography(ctx, 2, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range other.Items {
		if entry.Watched != nil && *entry.Watched {
			t.Fatal("another user's watched state leaked")
		}
	}
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.readEntity(ctx, person.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.apply(ctx, v, d, nil); err != nil {
		t.Fatal(err)
	}
	s.reader = &fixtureReader{resolve: func(context.Context, string, string, string) (string, error) { return "tt9999999", nil }}
	if err = (&creditWorker{s: s}).Work(ctx, &river.Job[creditArgs]{Args: creditArgs{ID: unresolved, Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	var imdb string
	if err = s.pool.QueryRow(ctx, `SELECT cinemeta_id FROM metadata_credits WHERE reference_id=$1`, unresolved).Scan(&imdb); err != nil || imdb != "" {
		t.Fatal("stale identity resolution crossed refresh", imdb, err)
	}
}
