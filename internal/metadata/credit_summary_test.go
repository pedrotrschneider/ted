package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/riverqueue/river"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTVDBCreditCharactersRolesAndEnglishSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"status":"success","data":{"token":"private"}}`))
		case "/people/10/extended":
			w.Write([]byte(`{"status":"success","data":{"id":10,"name":"Actor","characters":[{"seriesId":20,"name":"One","image":"https://artworks.thetvdb.com/character.jpg","series":{"name":"日本語","year":"2020","image":"https://artworks.thetvdb.com/poster.jpg"}},{"seriesId":20,"name":"Two","series":{"name":"日本語"}},{"seriesId":20,"name":"One","series":{"name":"日本語"}}]}}`))
		case "/series/20/extended":
			if r.URL.Query().Get("meta") != "translations" {
				t.Error("English summaries need translations")
			}
			w.Write([]byte(`{"status":"success","data":{"id":20,"name":"日本語","remoteIds":[{"sourceName":"IMDB","id":"tt1234567"}],"translations":{"nameTranslations":[{"language":"eng","name":"Alias","isAlias":true},{"language":"eng","name":"English Title","isPrimary":true}]}}}`))
		default:
			t.Errorf("unexpected full title enrichment %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "private"})
	p.tvdbURL = server.URL
	doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "10"})
	if err != nil || !doc.TVDBAppearanceChecked || len(doc.Credits) != 1 || doc.Credits[0].CharacterImage == "" || len(doc.Credits[0].Roles) != 2 {
		t.Fatal(doc, err)
	}
	imdb, item, err := p.ResolveCreditSummary(context.Background(), "Tvdb", "series", "20")
	if err != nil || imdb != "tt1234567" || item.Name != "English Title" || item.PreferredMetadataLanguage != "en" {
		t.Fatal(imdb, item, err)
	}
}

type localizedReader struct{ *fixtureReader }

func (r *localizedReader) ResolveCreditSummary(context.Context, string, string, string) (string, Item, error) {
	return "tt1234567", Item{Name: "English Title", PreferredMetadataLanguage: "en"}, nil
}

func TestFilmographyCachesEnglishSummaryAndCharacterArtworkWithoutTitleEnrichment(t *testing.T) {
	ctx := context.Background()
	reader := &localizedReader{&fixtureReader{fetch: func(context.Context, string, Request) (Document, error) {
		return Document{ProviderID: "10", TVDBAppearanceChecked: true, Item: Item{Name: "Actor", Overview: "Biography"}, Credits: []Credit{{Provider: "Tvdb", Kind: "series", ID: "20", TitleID: "tt1234567", Item: Item{Type: "Series", Name: "日本語"}, Roles: []string{"One", "Two"}, Poster: "https://artworks.thetvdb.com/poster.jpg", CharacterImage: "https://artworks.thetvdb.com/character.jpg"}}}, nil
	}}}
	s := metadataFixture(t, reader)
	person, err := s.Ensure(ctx, "Tvdb", Request{Kind: "person", ProviderID: "10"}, Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	page, err := s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].IdentityStatus != "queued" || len(page.Items[0].Images) != 2 {
		t.Fatal(page, err)
	}
	ref := page.Items[0].Item.ID
	job := &river.Job[creditArgs]{Args: creditArgs{ref, 1}}
	if err = (&creditWorker{s: s}).Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	page, err = s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || page.Items[0].Item.Name != "English Title" || page.Items[0].Media.ID != "tt1234567" || page.Items[0].IdentityStatus != "ready" {
		t.Fatal(page, err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_entities`).Scan(&count); err != nil || count != 1 {
		t.Fatal("filmography enriched other titles", count, err)
	}
	// Reapplying the same person retains translated summaries and image caches.
	if _, err = s.Refresh(ctx, person.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	page, err = s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || page.Items[0].Item.Name != "English Title" {
		t.Fatal(page, err)
	}
}

func TestEnglishCreditFallbackAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"status":"success","data":{"token":"private"}}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"id": 20, "name": "Original Title"}})
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "private"})
	p.tvdbURL = server.URL
	_, item, err := p.ResolveCreditSummary(context.Background(), "Tvdb", "series", "20")
	if !errors.Is(err, ErrMissing) || item.Name != "Original Title" {
		t.Fatal(item, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = p.ResolveCreditSummary(ctx, "Tvdb", "series", "20"); err == nil {
		t.Fatal("cancelled summary request succeeded")
	}
}
