package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTMDBArtworkReturnsAllKindsWithoutChangingDescriptionsOrFetchingCast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/49026/images" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected artwork lookup %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":49026,"posters":[{"file_path":"/poster.jpg","iso_639_1":"en","width":100,"height":200}],"backdrops":[{"file_path":"/background.jpg","iso_639_1":null}],"logos":[{"file_path":"/logo.png","iso_639_1":"ja"}]}`))
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TMDBToken: "test-key"})
	p.tmdbURL = server.URL
	images, err := p.Artwork(context.Background(), "Tmdb", Request{Kind: "movie", ProviderID: "49026"})
	if err != nil || len(images) != 3 || images[0].Type != "Primary" || images[2].Language != "ja" {
		t.Fatal(images, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Artwork(canceled, "Tmdb", Request{Kind: "movie", ProviderID: "49026"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
func TestTVDBArtworkUsesResolvedSeriesAndSeasonIDsAndRejectsEpisodeEditing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"status":"success","data":{"token":"test-token-1234567890"}}`))
		case "/artwork/types":
			w.Write([]byte(`{"status":"success","data":[{"id":1,"name":"Series Poster"},{"id":2,"name":"Series Background"},{"id":3,"name":"ClearLogo"}]}`))
		case "/people/types":
			w.Write([]byte(`{"status":"success","data":[]}`))
		case "/series/7/artworks":
			w.Write([]byte(`{"status":"success","data":{"id":7,"artworks":[{"image":"https://artworks.thetvdb.com/poster.jpg","type":1,"language":"eng","thumbnail":"https://artworks.thetvdb.com/poster_t.jpg"},{"image":"https://artworks.thetvdb.com/back.jpg","type":2}]}}`))
		case "/seasons/8/extended":
			w.Write([]byte(`{"status":"success","data":{"id":8,"image":"https://artworks.thetvdb.com/season.jpg","artwork":[]}}`))
		default:
			t.Errorf("unexpected eager lookup %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "test-key"})
	p.tvdbURL = server.URL
	for _, v := range []struct {
		kind, id string
		count    int
	}{{"series", "7", 2}, {"season", "8", 1}} {
		images, err := p.Artwork(context.Background(), "Tvdb", Request{Kind: v.kind, ProviderID: v.id})
		if err != nil || len(images) != v.count {
			t.Fatal(v, images, err)
		}
	}
	if _, err := p.Artwork(context.Background(), "Tvdb", Request{Kind: "episode", ProviderID: "9"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("episode alternatives were requested", err)
	}
}
