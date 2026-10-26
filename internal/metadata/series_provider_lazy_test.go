package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/riverqueue/river"
)

func TestSeriesProviderOverrideReviewKeepsOrRemovesHiddenSeasonEdits(t *testing.T) {
	for _, action := range []string{"keep", "remove"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			var calls atomic.Int32
			r := &fixtureReader{fetch: func(_ context.Context, p string, req Request) (Document, error) {
				calls.Add(1)
				return Document{ProviderID: "200", Item: Item{Name: p + " title", Overview: "Provider overview"}, Images: []Image{{Type: "Primary", URL: "https://image.tmdb.org/t/p/original/new.jpg"}}, Children: []Child{{Request: Request{Kind: "season", Season: 1, TitleID: req.TitleID, ProviderID: "20"}}, {Request: Request{Kind: "season", Season: 2, TitleID: req.TitleID, ProviderID: "21"}}}}, nil
			}}
			s := metadataFixture(t, r)
			artworkUser(t, s)
			canonical := providerCanonical()
			canonical.Episodes[0].TMDBID = 201
			canonical.Episodes[1].TMDBID = 202
			if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
				t.Fatal(err)
			}
			root, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: canonical.ID}, CinemetaSeed(canonical.Movie))
			if err != nil {
				t.Fatal(err)
			}
			runEntity(t, s, root.Item.ID)
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			hidden, err := s.insert(ctx, tx, "Tvdb", Request{Kind: "season", TitleID: canonical.ID, Season: 22}, Item{Type: "Season", Name: "Hidden season"}, root.Item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url) VALUES($1,'Primary',0,'')`, hidden); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `UPDATE metadata_entities SET item=item||'{"LockedFields":["Overview"],"Overview":"Custom text"}'::jsonb WHERE id=ANY($1::uuid[])`, []string{root.Item.ID, hidden}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{root.Item.ID, hidden} {
				v, e := s.Get(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.UploadArtwork(ctx, 1, v.Images[0].ID, v.Images[0].Generation, artworkPNG(t, color.White)); e != nil {
					t.Fatal(e)
				}
			}
			protected := protectedMetadataState(t, s)
			review, err := s.SeriesProvider(ctx, canonical.ID)
			if err != nil || review.Overrides.Artwork != 2 || review.Overrides.Metadata != 2 {
				t.Fatal(review, err)
			}
			if _, err = s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{}); !errors.Is(err, ErrOverridesReview) {
				t.Fatal("missing consent accepted", err)
			}
			// An edit after the dialog was displayed must invalidate its consent, even
			// when the edited season is absent from the new provider's structure.
			v, err := s.Get(ctx, hidden)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.UploadArtwork(ctx, 1, v.Images[0].ID, v.Images[0].Generation, artworkPNG(t, color.Black)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{Action: action, Version: review.Overrides.Version}); !errors.Is(err, ErrOverridesReview) {
				t.Fatal("stale override review accepted", err)
			}
			review, err = s.SeriesProvider(ctx, canonical.ID)
			if err != nil {
				t.Fatal(err)
			}
			before := calls.Load()
			if _, err = s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{Action: action, Version: review.Overrides.Version}); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != before {
				t.Fatal("switch made eager provider reads")
			}
			after, err := s.SeriesProvider(ctx, canonical.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if action == "keep" {
				want = 2
			}
			if after.Overrides.Artwork != want || after.Overrides.Metadata != want {
				t.Fatal(after)
			}
			v, err = s.Get(ctx, hidden)
			if err != nil {
				t.Fatal(err)
			}
			if action == "remove" && (v.Images[0].UserModified || v.Images[0].Tag != "" || v.Item.Overview != "" || len(v.Item.LockedFields) > 0) {
				t.Fatal("hidden override survived removal", v)
			}
			runEntity(t, s, root.Item.ID)
			v, err = s.Get(ctx, root.Item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "remove" && (v.Item.Overview != "Provider overview" || v.Images[0].UserModified) {
				t.Fatal("new provider not used", v)
			}
			if action == "keep" && (v.Item.Overview != "Custom text" || !v.Images[0].UserModified) {
				t.Fatal("manual values lost", v)
			}
			if got := protectedMetadataState(t, s); got != protected {
				t.Fatal("override decision changed accounts, viewing or mappings")
			}
		})
	}
}

func TestSeriesProviderSwitchSeedsOnlyAndScheduleRefreshRetainsVisitedChildren(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int32
	r := &fixtureReader{fetch: func(_ context.Context, _ string, req Request) (Document, error) {
		reads.Add(1)
		if req.Kind == "series" {
			return Document{ProviderID: "200", Item: Item{Name: "Series"}, Children: []Child{{Request: Request{Kind: "season", TitleID: req.TitleID, Season: 1, ProviderID: "20"}}}}, nil
		}
		return Document{ProviderID: req.ProviderID, Item: Item{Name: "Visited episode"}, ScopedCastChecked: true}, nil
	}}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	canonical := providerCanonical()
	canonical.Episodes = nil
	for i := 1; i <= 100; i++ {
		canonical.Episodes = append(canonical.Episodes, cinemeta.Episode{ID: fmt.Sprintf("tt1234567:1:%d", i), Season: 1, Number: i, TVDBID: int64(i), TMDBID: int64(1000 + i)})
	}
	if err := catalog.New(s.pool).SaveSchedule(ctx, canonical, time.Now()); err != nil {
		t.Fatal(err)
	}
	root, err := s.ChangeSeriesProvider(ctx, 1, canonical, "Tvdb", "Tmdb", OverrideDecision{})
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 0 {
		t.Fatal("synchronous provider reads", reads.Load())
	}
	runEntity(t, s, root.Item.ID)
	assertJobs := func(want int) {
		t.Helper()
		var n int
		if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='native_metadata_enrichment'`).Scan(&n); e != nil || n != want {
			t.Fatal("unexpected metadata fan-out", n, want, e)
		}
	}
	assertJobs(1)
	if reads.Load() != 1 {
		t.Fatal("child metadata fetched eagerly", reads.Load())
	}
	var children int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_entities WHERE kind IN ('season','episode') AND status='new'`).Scan(&children); err != nil || children != 101 {
		t.Fatal(children, err)
	}
	req := Request{Kind: "episode", TitleID: canonical.ID, VideoID: canonical.Episodes[0].ID, Season: 1, Number: 1}
	episode, err := s.Ensure(ctx, "Tvdb", req, Item{})
	if err != nil {
		t.Fatal(err)
	}
	assertJobs(2)
	runEntity(t, s, episode.Item.ID)
	visited, err := s.Get(ctx, episode.Item.ID)
	if err != nil || visited.Status != "ready" {
		t.Fatal(visited, err)
	}
	if err = (&seriesLayoutWorker{s: s}).Work(ctx, &river.Job[catalog.SeriesLayoutRefreshArgs]{Args: catalog.SeriesLayoutRefreshArgs{Title: canonical.ID}}); err != nil {
		t.Fatal(err)
	}
	assertJobs(3)
	runEntity(t, s, root.Item.ID)
	assertJobs(3)
	after, err := s.Get(ctx, episode.Item.ID)
	if err != nil || after.Status != "ready" || after.Generation != visited.Generation || after.Item.Name != visited.Item.Name {
		t.Fatal("root refresh invalidated visited child", after, err)
	}
}

func TestSeriesIdentityHintsReuseExactLinksWithoutReplacingCanonicalStructure(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	canonical := providerCanonical()
	artworkUser(t, s)
	if _, err := s.pool.Exec(ctx, `INSERT INTO metadata_series_providers(title_id,provider,changed_by) VALUES($1,'Tmdb',1)`, canonical.ID); err != nil {
		t.Fatal(err)
	}
	previous := append([]cinemeta.Episode(nil), canonical.Episodes...)
	previous[0].TMDBID = 201
	previous[0].Season = 20
	previous[0].Name = "Old provider title"
	previous[1].TMDBID = 202
	previous[1].TVDBID = 999 // Conflicting identity must never be reused.
	raw, _ := json.Marshal(previous)
	if _, err := s.pool.Exec(ctx, `INSERT INTO metadata_series_layouts(title_id,provider,generation,episodes) VALUES($1,'Tmdb',1,$2)`, canonical.ID, raw); err != nil {
		t.Fatal(err)
	}
	if err := s.addSeriesIdentityHints(ctx, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Episodes[0].TMDBID != 201 || canonical.Episodes[0].Season != 1 || canonical.Episodes[0].Name != "One" || canonical.Episodes[1].TMDBID != 0 {
		t.Fatal(canonical.Episodes)
	}
}
