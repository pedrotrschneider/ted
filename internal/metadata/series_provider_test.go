package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/riverqueue/river"
)

func providerCanonical() cinemeta.Series {
	return cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1234567", Type: "series", Name: "Original", Genres: []string{}}, Episodes: []cinemeta.Episode{{ID: "tt1234567:1:1", Season: 1, Number: 1, Name: "One", TVDBID: 101, ReleaseDate: "2020-01-01"}, {ID: "tt1234567:2:1", Season: 2, Number: 1, Name: "Two", TVDBID: 102, ReleaseDate: "2020-01-02"}}}
}
func protectedMetadataState(t *testing.T, s *Service) string {
	t.Helper()
	var v string
	err := s.pool.QueryRow(context.Background(), `SELECT json_build_object('users',(SELECT json_agg(u ORDER BY id) FROM users u),'media',(SELECT json_agg(c ORDER BY id) FROM catalog_media c),'watch',(SELECT json_agg(w ORDER BY user_id,media_id) FROM watch_current w),'history',(SELECT json_agg(h ORDER BY id) FROM watch_history h),'mappings',(SELECT json_agg(m ORDER BY id) FROM media_mappings m))::text`).Scan(&v)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSeriesProviderSwitchFencesHeldWorkersAndPreservesViewingMappingsAndOverrides(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	r := &fixtureReader{fetch: func(ctx context.Context, p string, req Request) (Document, error) {
		if req.Kind == "series" {
			id := "100"
			if p == "Tmdb" {
				id = "200"
			}
			doc := Document{ProviderID: id, Item: Item{Name: p + " series"}, Images: []Image{{Type: "Primary", URL: "https://image.tmdb.org/t/p/original/provider.jpg"}}}
			if p == "Tmdb" {
				doc.Children = []Child{{Request: Request{Kind: "season", TitleID: req.TitleID, Season: 20, ProviderID: "250"}}}
			}
			return doc, nil
		}
		if p == "Tvdb" {
			close(entered)
			<-release
			return Document{ProviderID: "101", Item: Item{Name: "Stale TVDB episode"}}, nil
		}
		if req.Kind == "season" {
			return Document{ProviderID: "250", Item: Item{Name: "Season 20"}, Children: []Child{{Request: Request{Kind: "episode", TitleID: req.TitleID, Season: 20, Number: 432, ProviderID: "301", ParentProviderID: "200"}, Image: "https://image.tmdb.org/t/p/original/episode.jpg"}}}, nil
		}
		return Document{ProviderID: req.ProviderID, ScopedCastChecked: true, Item: Item{Name: "New episode"}, People: []PersonSummary{}}, nil
	}}
	r.structure = func(_ context.Context, p string, canonical cinemeta.Series, _ Document) (cinemeta.Series, error) {
		canonical.Episodes = append([]cinemeta.Episode(nil), canonical.Episodes...)
		for i := range canonical.Episodes {
			canonical.Episodes[i].Season = 20
			canonical.Episodes[i].Number = i + 432
			canonical.Episodes[i].TMDBID = int64(301 + i)
		}
		return canonical, nil
	}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	canonical := providerCanonical()
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: canonical.ID}, CinemetaSeed(canonical.Movie))
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, root.Item.ID)
	ready, err := s.Get(ctx, root.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UploadArtwork(ctx, 1, ready.Images[0].ID, ready.Images[0].Generation, artworkPNG(t, color.White)); err != nil {
		t.Fatal(err)
	}
	custom, err := s.Get(ctx, root.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.Ensure(ctx, "Tvdb", Request{Kind: "episode", TitleID: canonical.ID, VideoID: canonical.Episodes[0].ID, Season: 1, Number: 1}, Item{Type: "Episode", Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	var media string
	if err = s.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt1234567:1:1','episode','{}') RETURNING id::text`).Scan(&media); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,'fixture','torrent',$2,1,'episode.mkv',123,'{}',1)`, media, fmt.Sprintf("%040d", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,revision,observed_at) VALUES(1,$1,'{"played":true,"position_ticks":0,"play_count":2,"last_played":"2020-01-01T00:00:00Z"}','TEDLocal',9,'2020-01-01')`, media); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES(1,$1,'ted_watched','{"played":true}','2020-01-01')`, media); err != nil {
		t.Fatal(err)
	}
	protected := protectedMetadataState(t, s)
	old, err := s.readEntity(ctx, ep.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{ID: old.ID, Generation: old.Generation}})
	}()
	<-entered
	review, err := s.SeriesProvider(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{Action: "keep", Version: review.Overrides.Version})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Provider != "Tmdb" || changed.Status != "queued" {
		t.Fatal(changed)
	}
	runEntity(t, s, root.Item.ID)
	once.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	season, err := s.Lookup(ctx, Request{Kind: "season", TitleID: canonical.ID, Season: 20})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, season.Item.ID)
	var episodeEntities int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_entities WHERE kind='episode' AND request->>'title_id'=$1`, canonical.ID).Scan(&episodeEntities); err != nil || episodeEntities != 2 {
		t.Fatal("season thumbnails created a second identity for a reordered episode", episodeEntities, err)
	}
	current, err := s.Ensure(ctx, "Tvdb", Request{Kind: "episode", TitleID: canonical.ID, VideoID: canonical.Episodes[0].ID, Season: 20, Number: 432}, Item{})
	if err != nil {
		t.Fatal(err)
	}
	if current.Item.ID != ep.Item.ID || current.Provider != "Tmdb" || current.Item.Name == "Stale TVDB episode" {
		t.Fatal("old worker or caller overrode policy", current)
	}
	runEntity(t, s, current.Item.ID)
	current, err = s.Get(ctx, current.Item.ID)
	if err != nil || !current.ScopedCast || current.Item.IndexNumber == nil || *current.Item.IndexNumber != 432 || *current.Item.ParentIndexNumber != 20 {
		t.Fatal(current, err)
	}
	if got := protectedMetadataState(t, s); got != protected {
		t.Fatal("provider switch changed identity, viewing or source mappings")
	}
	final, err := s.Get(ctx, root.Item.ID)
	if err != nil || final.Images[0].Tag != custom.Images[0].Tag || !final.Images[0].UserModified {
		t.Fatal("custom artwork lost", final, err)
	}
	if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	projected, err := catalog.New(s.pool).NativeSeries(ctx, canonical.ID)
	if err != nil || projected.Episodes[1].Season != 20 || projected.Episodes[1].Number != 433 || projected.Episodes[1].ID != "tt1234567:2:1" {
		t.Fatal("Cinemeta refresh undid layout", projected, err)
	}
	if _, err = s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{}); !errors.Is(err, ErrProviderChanged) {
		t.Fatal("stale confirmation accepted", err)
	}
}

func TestSeasonPosterAdmissionUpgradesToScopedCastAndFencesItsHeldWorker(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	r := &fixtureReader{fetch: func(_ context.Context, _ string, req Request) (Document, error) {
		if req.Kind == "series" {
			return Document{ProviderID: "100", Item: Item{Name: "Series"}}, nil
		}
		if req.SkipCast {
			close(entered)
			<-release
			return Document{ProviderID: "10", Item: Item{Name: "Poster metadata"}}, nil
		}
		return Document{ProviderID: "10", Item: Item{Name: "Season cast"}, ScopedCastChecked: true, People: []PersonSummary{{Provider: "Tmdb", ID: "1", Name: "Scoped actor", Type: "Actor"}}}, nil
	}}
	s := metadataFixture(t, r)
	canonical := providerCanonical()
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: canonical.ID}, Item{Name: "Series"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, root.Item.ID)
	poster := Request{Kind: "season", TitleID: canonical.ID, Season: 1, SkipCast: true}
	initial, err := s.Ensure(ctx, "Tvdb", poster, seed(poster))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{initial.Item.ID, initial.Generation}})
	}()
	<-entered
	full := poster
	full.SkipCast = false
	next, err := s.Ensure(ctx, "Tvdb", full, seed(full))
	if err != nil || next.Generation <= initial.Generation {
		t.Fatal(next, err)
	}
	once.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, initial.Item.ID)
	if err != nil || current.Item.Name == "Poster metadata" {
		t.Fatal("stale poster worker published", current, err)
	}
	runEntity(t, s, current.Item.ID)
	current, err = s.Get(ctx, current.Item.ID)
	if err != nil || !current.ScopedCast || len(current.Item.People) != 1 {
		t.Fatal(current, err)
	}
	again, err := s.Ensure(ctx, "Tvdb", poster, seed(poster))
	if err != nil || again.Generation != current.Generation || !again.ScopedCast {
		t.Fatal("poster visit downgraded full cast", again, err)
	}
}

func TestScopedCastRetriesNewExactEpisodeIdentityOnlyOnce(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(_ context.Context, _ string, req Request) (Document, error) {
		return Document{ProviderID: "101", Item: Item{Name: "Metadata"}, ScopedCastChecked: len(req.CanonicalEpisodes) == 1 && req.CanonicalEpisodes[0].TVDBID > 0}, nil
	}}
	s := metadataFixture(t, r)
	canonical := providerCanonical()
	canonical.Episodes = canonical.Episodes[:1]
	canonical.Episodes[0].TVDBID = 0
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	req := Request{Kind: "episode", TitleID: canonical.ID, VideoID: canonical.Episodes[0].ID, Season: 1, Number: 1}
	first, err := s.Ensure(ctx, "Tvdb", req, seed(req))
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, first.Item.ID)
	for i := 0; i < 3; i++ {
		same, err := s.Ensure(ctx, "Tvdb", req, seed(req))
		if err != nil || same.Generation != first.Generation || same.ScopedCast {
			t.Fatal("unchanged visit repeated fetch", same, err)
		}
	}
	canonical.Episodes[0].TVDBID = 101
	if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Ensure(ctx, "Tvdb", req, seed(req))
	if err != nil || updated.Generation != first.Generation+1 {
		t.Fatal(updated, err)
	}
	runEntity(t, s, first.Item.ID)
	for i := 0; i < 3; i++ {
		same, err := s.Ensure(ctx, "Tvdb", req, seed(req))
		if err != nil || same.Generation != updated.Generation || !same.ScopedCast {
			t.Fatal("new exact identity was lost or repeated", same, err)
		}
	}
}

func TestChosenSeriesStructureRefreshIsDurableAndCoalescesSourceChanges(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	artworkUser(t, s)
	canonical := providerCanonical()
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: canonical.ID}, Item{Name: "Series"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_series_providers(title_id,provider,changed_by) VALUES($1,'Tvdb',1)`, canonical.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET status='ready',fetched_at=now() WHERE id=$1`, root.Item.ID); err != nil {
		t.Fatal(err)
	}
	count := func(want int) {
		t.Helper()
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='metadata_series_structure_refresh' AND state IN ('available','pending','running','retryable','scheduled')`).Scan(&n); err != nil || n != want {
			t.Fatal(n, want, err)
		}
	}
	if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	count(0)
	canonical.Episodes[0].TMDBID = 201
	for i := 0; i < 2; i++ {
		if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	count(1)
	if _, err = s.pool.Exec(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE kind='metadata_series_structure_refresh'`); err != nil {
		t.Fatal(err)
	}
	if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	count(0)
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET fetched_at=now()-interval '25 hours' WHERE id=$1`, root.Item.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	count(1)
	var raw json.RawMessage
	if err = s.pool.QueryRow(ctx, `SELECT canonical_episodes FROM catalog_episode_schedules WHERE title_id=$1`, canonical.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved []cinemeta.Episode
	if err = json.Unmarshal(raw, &saved); err != nil || saved[0].TMDBID != 201 {
		t.Fatal(saved, err)
	}
}

func TestSeriesProviderChangeRejectsDemotedAdminAndUnverifiedStructure(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) {
		t.Error("provider switch performed an eager network read")
		return Document{}, ErrProvider
	}}

	s := metadataFixture(t, r)
	artworkUser(t, s)
	canonical := providerCanonical()
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Hold the authorization row after the preliminary check. The final
	// FOR SHARE check must wait for the actual demotion to commit.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM users WHERE id=$1 FOR SHARE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider change never reached authorization lock")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET role='user' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrArtworkForbidden) {
		t.Fatal("late demotion allowed", err)
	}

	choice, err := s.SeriesProvider(ctx, canonical.ID)
	if err != nil || choice.Provider != "Tvdb" || choice.Revision != 0 {
		t.Fatal(choice, err)
	}
	r.fetch = func(context.Context, string, Request) (Document, error) {
		return Document{ProviderID: "200", Item: Item{Name: "Target"}}, nil
	}
	r.structure = func(context.Context, string, cinemeta.Series, Document) (cinemeta.Series, error) {
		return cinemeta.Series{}, ErrAmbiguous
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.readEntity(ctx, changed.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{v.ID, v.Generation}})
	if err == nil {
		t.Fatal("unverified structure published")
	}
	projected, err := catalog.New(s.pool).NativeSeries(ctx, canonical.ID)
	if err != nil || projected.Episodes[1].Season != 2 {
		t.Fatal("failed layout replaced source identities", projected, err)
	}
	var layouts int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_episode_layout`).Scan(&layouts); err != nil || layouts != 0 {
		t.Fatal(layouts, err)
	}
}
