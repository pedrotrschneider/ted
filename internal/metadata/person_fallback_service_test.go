package metadata

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

type gatedPersonReader struct {
	*fixtureReader
	gate             atomic.Bool
	entered, release chan struct{}
}

func (r *gatedPersonReader) Enabled(provider string) bool {
	if provider == "Tmdb" && r.gate.Load() {
		r.entered <- struct{}{}
		<-r.release
	}
	return true
}

func TestMetadataPersonFallbackLazyBackfillCacheAndProvenance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var phase atomic.Int32
	r := &gatedPersonReader{entered: make(chan struct{}, 2), release: make(chan struct{}), fixtureReader: &fixtureReader{}}
	r.fetch = func(context.Context, string, Request) (Document, error) {
		doc := Document{ProviderID: "255067", Item: Item{Name: "Jenna Fischer", ProviderIDs: map[string]string{"Tvdb": "255067"}},
			Credits: []Credit{{Provider: "Tvdb", Kind: "series", ID: "73244", Item: Item{Type: "Series", Name: "The Office"}, Roles: []string{"Pam Beesly"}}},
			Images:  []Image{{Type: "Primary", URL: "https://artworks.thetvdb.com/jenna.jpg"}}, TVDBAppearanceChecked: true, TMDBBiographyChecked: true, TMDBBiographyNameChecked: phase.Load() > 0}
		if phase.Load() == 2 {
			doc.Item.Overview = "TMDb biography"
			doc.BiographyMatch = &BiographyMatch{Provider: "Tmdb", ProviderID: "51856", Method: "name", Name: "Jenna Fischer"}
			doc.FieldSources = map[string]string{"Overview": "Tmdb"}
		}
		if phase.Load() == 3 {
			return doc, ErrProvider
		}
		if phase.Load() == 4 {
			doc.Item.Overview = "TVDB biography"
		}
		return doc, nil
	}
	s := metadataFixture(t, r)
	person, err := s.Ensure(ctx, "Tvdb", Request{Kind: "person", ProviderID: "255067"}, Item{Type: "Person", Name: "Jenna Fischer"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID) // Identifier-only fallback already checked this no-ID cache.
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET item=item||'{"LockedFields":["Overview"]}'::jsonb WHERE id=$1`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	locked, err := s.RequestPerson(ctx, person.Item.ID)
	if err != nil || locked.Generation != 1 || locked.Status != "ready" {
		t.Fatal("a locked empty biography triggered name backfill", locked, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET item=item-'LockedFields' WHERE id=$1`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_artwork SET user_modified=true,source_url='https://artworks.thetvdb.com/custom.jpg' WHERE entity_id=$1`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	holder, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM metadata_entities WHERE id=$1 FOR UPDATE`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	r.gate.Store(true)
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.RequestPerson(ctx, person.Item.ID); done <- err }()
	}
	// Both callers have read the legacy ready record before either can acquire
	// the held PostgreSQL row lock. A force decision made from that stale read
	// would queue two generations rather than the single permitted backfill.
	for range 2 {
		select {
		case <-r.entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(r.release)
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	r.gate.Store(false)
	var jobs int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='native_metadata_enrichment' AND args->>'id'=$1`, person.Item.ID).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatal("concurrent visits queued duplicate fallback generations", jobs, err)
	}
	v, err := s.Get(ctx, person.Item.ID)
	if err != nil || v.Generation != 2 || v.Status != "queued" {
		t.Fatal(v, err)
	}
	phase.Store(1) // TMDb returned no biography; the attempt must remain cached.
	runEntity(t, s, person.Item.ID)
	restarted, err := New(s.pool, r, "", s.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	v, err = restarted.RequestPerson(ctx, person.Item.ID)
	if err != nil || v.Generation != 2 || v.Status != "ready" || v.Item.Overview != "" {
		t.Fatal("restart repeated a successful empty fallback", v, err)
	}
	phase.Store(2)
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	v, err = s.Get(ctx, person.Item.ID)
	if err != nil || v.Provider != "Tvdb" || v.Item.ID != person.Item.ID || v.Item.Overview != "TMDb biography" || v.FieldSources["Overview"] != "Tmdb" || v.Item.ProviderIDs["Tmdb"] != "" || v.BiographyMatch == nil || v.BiographyMatch.Method != "name" || v.BiographyMatch.ProviderID != "51856" {
		t.Fatal("fallback lost stable person identity or field provenance", v, err)
	}
	var source string
	if err = s.pool.QueryRow(ctx, `SELECT source_url FROM metadata_artwork WHERE entity_id=$1`, person.Item.ID).Scan(&source); err != nil || source != "https://artworks.thetvdb.com/custom.jpg" {
		t.Fatal("fallback replaced user artwork", source, err)
	}
	phase.Store(3)
	stale, err := s.readEntity(ctx, person.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.apply(ctx, stale, Document{ProviderID: "255067", Item: Item{Name: "Jenna Fischer", Overview: "Stale biography"}, BiographyMatch: &BiographyMatch{Provider: "Tmdb", ProviderID: "7", Method: "name", Name: "Wrong person"}}, nil); err != nil {
		t.Fatal(err)
	}
	v, err = s.Get(ctx, person.Item.ID)
	if err != nil || v.Status != "queued" || v.Item.Overview != "TMDb biography" || v.BiographyMatch == nil || v.BiographyMatch.ProviderID != "51856" {
		t.Fatal("stale generation overwrote biography/match provenance", v, err)
	}
	err = (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{ID: person.Item.ID, Generation: v.Generation}})
	if !errors.Is(err, ErrProvider) {
		t.Fatal("fallback failure was not retryable", err)
	}
	v, err = s.Get(ctx, person.Item.ID)
	if err != nil || v.Status != "partial" || v.Item.Overview != "TMDb biography" || v.FieldSources["Overview"] != "Tmdb" || v.BiographyMatch == nil || v.BiographyMatch.ProviderID != "51856" {
		t.Fatal("fallback outage erased usable cached biography", v, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET item=item||'{"Overview":"Custom biography","LockedFields":["Overview"]}'::jsonb,field_sources=field_sources||'{"Overview":"TED"}'::jsonb,request=request-'biography_match' WHERE id=$1`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	phase.Store(2)
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	v, err = s.Get(ctx, person.Item.ID)
	if err != nil || v.Item.Overview != "Custom biography" || v.FieldSources["Overview"] != "TED" || v.BiographyMatch != nil {
		t.Fatal("fallback replaced a locked biography", v, err)
	}
	var credits int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_credits WHERE person_id=$1 AND provider='Tvdb'`, person.Item.ID).Scan(&credits); err != nil || credits != 1 {
		t.Fatal("fallback changed TVDB filmography", credits, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET item=item-'LockedFields' WHERE id=$1`, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	phase.Store(4)
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	v, err = restarted.Get(ctx, person.Item.ID)
	if err != nil || v.Item.Overview != "TVDB biography" || v.FieldSources["Overview"] != "Tvdb" || v.BiographyMatch != nil {
		t.Fatal("primary replacement retained stale heuristic provenance", v, err)
	}
}
