package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultTVDBArtworkUsesOneImagePerSupportedType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"status":"success","data":{"token":"fixture-token-1234567890"}}`))
		case "/artwork/types":
			w.Write([]byte(`{"status":"success","data":[{"id":1,"name":"Series Poster"},{"id":2,"name":"Series Background"},{"id":3,"name":"Series Banner"},{"id":4,"name":"ClearLogo"},{"id":5,"name":"ClearArt"}]}`))
		case "/people/types":
			w.Write([]byte(`{"status":"success","data":[]}`))
		case "/series/7/extended", "/seasons/8/extended":
			id := "7"
			if r.URL.Path == "/seasons/8/extended" {
				id = "8"
			}
			w.Write([]byte(`{"status":"success","data":{"id":` + id + `,"seriesId":7,"number":1,"name":"Title","characters":[],"seasons":[],"episodes":[],"image":"https://artworks.thetvdb.com/poster.jpg","artworks":[
 {"type":1,"image":"https://artworks.thetvdb.com/other-poster.jpg"},
 {"type":2,"image":"https://artworks.thetvdb.com/lower-back.jpg","score":1},
 {"type":2,"image":"https://artworks.thetvdb.com/best-back.jpg","score":5},
 {"type":3,"image":"https://artworks.thetvdb.com/banner.jpg"},
 {"type":4,"image":"https://artworks.thetvdb.com/logo.png"},
 {"type":5,"image":"https://artworks.thetvdb.com/art.png"}]}}`))
		default:
			t.Errorf("unexpected eager request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "fixture-key"})
	p.tvdbURL = server.URL
	d, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "series", ProviderID: "7"})
	if err != nil || len(d.Images) != 4 {
		t.Fatal(d.Images, err)
	}
	seen := map[string]bool{}
	for _, a := range d.Images {
		if a.Index != 0 || seen[a.Type] || !editableImageType("series", a.Type) {
			t.Fatal("default enrichment enrolled alternatives", d.Images)
		}
		if a.Type == "Backdrop" && a.URL != "https://artworks.thetvdb.com/best-back.jpg" {
			t.Fatal("best backdrop lost", a)
		}
		seen[a.Type] = true
	}
	season, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "season", ProviderID: "8", ParentProviderID: "7", Season: 1})
	if err != nil || len(season.Images) != 1 || season.Images[0].Type != "Primary" {
		t.Fatal("season enrolled non-poster artwork", season.Images, err)
	}
}

func TestDefaultTMDBBackdropFallbackSelectsOneAlternative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/7" {
			t.Errorf("unexpected eager request %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":7,"title":"Movie","credits":{"cast":[],"crew":[]},"images":{"backdrops":[{"file_path":"/first.jpg"},{"file_path":"/second.jpg"}],"logos":[]}}`))
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TMDBToken: "fixture-token"})
	p.tmdbURL = server.URL
	d, err := p.Fetch(context.Background(), "Tmdb", Request{Kind: "movie", ProviderID: "7"})
	if err != nil || len(d.Images) != 1 || d.Images[0].Type != "Backdrop" || d.Images[0].Index != 0 || d.Images[0].URL != "https://image.tmdb.org/t/p/original/first.jpg" {
		t.Fatal(d.Images, err)
	}
}
