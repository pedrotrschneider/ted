package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestTMDBMovieAndLazyPersonContract(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.URL.Query().Get("language") != "en" {
			t.Error("missing provider authentication/language")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/find/tt1345836":
			w.Write([]byte(`{"movie_results":[{"id":49026}]}`))
		case "/movie/49026":
			if r.URL.Query().Get("append_to_response") != "credits,images,release_dates,videos" {
				t.Error("missing title components")
			}
			w.Write([]byte(`{"id":49026,"imdb_id":"tt1345836","title":"The Dark Knight Rises","original_title":"The Dark Knight Rises","original_language":"en","overview":"Description","release_date":"2012-07-20","runtime":165,"vote_average":7.8,"poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","genres":[{"id":28,"name":"Action"}],"production_companies":[{"id":999,"name":"Studio"}],"credits":{"cast":[{"id":3894,"name":"Christian Bale","character":"Bruce Wayne","profile_path":"/bale.jpg"}],"crew":[{"id":525,"name":"Christopher Nolan","department":"Directing","job":"Director"}]},"images":{"backdrops":[{"file_path":"/backdrop.jpg"},{"file_path":"/second.jpg"}],"logos":[{"file_path":"/logo.png"}]},"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13"}]}]},"videos":{"results":[{"site":"YouTube","key":"abc_123","type":"Trailer"}]}}`))
		case "/person/3894":
			w.Write([]byte(`{"id":3894,"name":"Christian Bale","biography":"Biography","birthday":"1974-01-30","place_of_birth":"Wales","external_ids":{"imdb_id":"nm0000288"},"combined_credits":{"cast":[{"id":49026,"media_type":"movie","title":"The Dark Knight Rises","release_date":"2012-07-20","character":"Bruce Wayne","poster_path":"/poster.jpg"},{"id":7,"media_type":"tv","name":"A show","first_air_date":"2001-01-01"}],"crew":[]}}`))
		case "/tv/7/external_ids":
			w.Write([]byte(`{"imdb_id":"tt1234567"}`))
		default:
			t.Errorf("unexpected eager lookup %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TMDBToken: "secret-token"})
	p.tmdbURL = server.URL
	d, err := p.Fetch(context.Background(), "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"})
	if err != nil || d.ProviderID != "49026" || d.OriginalLanguage != "en" || !d.OriginalLanguageChecked || d.Item.OfficialRating != "PG-13" || len(d.People) != 2 || d.People[0].Role != "Bruce Wayne" || len(d.Images) != 3 || *d.Item.RunTimeTicks != 165*60*10000000 {
		t.Fatalf("movie contract: %+v %v", d, err)
	}
	if len(paths) != 2 {
		t.Fatal("cast list eagerly fetched biographies", paths)
	}
	person, err := p.Fetch(context.Background(), "Tmdb", Request{Kind: "person", ProviderID: "3894"})
	if err != nil || person.Item.Overview != "Biography" || len(person.Credits) != 2 || person.Credits[0].Poster == "" || len(paths) != 3 {
		t.Fatalf("lazy person/filmography contract: %+v %v", person, err)
	}
	id, err := p.ResolveCredit(context.Background(), "Tmdb", "series", "7")
	if err != nil || id != "tt1234567" || len(paths) != 4 {
		t.Fatal(id, err, paths)
	}
	raw, _ := json.Marshal(d.Item)
	if !strings.Contains(string(raw), `"RunTimeTicks"`) || strings.Contains(string(raw), `"runtime"`) || strings.Contains(string(raw), "secret-token") {
		t.Fatal("DTO/credential contract", string(raw))
	}
}

func TestTMDBPartialIdentityAndExplicitEmptyCredits(t *testing.T) {
	responses := []string{
		`{"id":1,"title":"Movie","imdb_id":"tt1234567"}`,
		`{"id":1,"title":"Movie","imdb_id":"tt7654321","credits":{"cast":[],"crew":[]}}`,
		`{"id":1,"title":"Movie","imdb_id":"tt1234567","credits":{"cast":[],"crew":[]}}`,
	}
	for i, raw := range responses {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(raw)) }))
			defer server.Close()
			p := NewProviders(ProviderConfig{TMDBToken: "private"})
			p.tmdbURL = server.URL
			d, err := p.Fetch(context.Background(), "Tmdb", Request{Kind: "movie", TitleID: "tt1234567", ProviderID: "1"})
			if i < 2 && !errors.Is(err, ErrPartial) {
				t.Fatal("accepted missing cast or conflicting identity", err)
			}
			if i == 0 && (d.Item.Name != "Movie" || d.People != nil) {
				t.Fatal("lost usable core or fabricated empty cast")
			}
			if i == 2 && (err != nil || d.People == nil || len(d.People) != 0) {
				t.Fatal("complete empty cast rejected", err)
			}
		})
	}
}

func TestTVDBOfficialSchemasAndLazyHierarchy(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/login" && r.Header.Get("Authorization") != "Bearer private-tv-token" {
			t.Error("missing TVDB bearer")
		}
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"status":"success","data":{"token":"private-tv-token"}}`))
		case "/search/remoteid/tt0386676":
			w.Write([]byte(`{"status":"success","data":[{"series":{"id":73244}}]}`))
		case "/series/73244/extended":
			w.Write([]byte(`{"status":"success","data":{"id":73244,"originalLanguage":"jpn","name":"The Office","overview":"Overview","remoteIds":[{"sourceName":"IMDB","id":"tt0386676"}],"score":234567,"firstAired":"2005-03-24","status":{"name":"Ended"},"characters":[{"peopleId":8,"personName":"Steve Carell","peopleType":"Actor","name":"Michael Scott","personImgURL":"https://artworks.thetvdb.com/actor.jpg"}],"seasons":[{"id":100,"number":1,"seriesId":73244,"type":{"type":"official"}}],"artworks":[{"type":2,"image":"https://artworks.thetvdb.com/poster.jpg"}]}}`))
		case "/artwork/types":
			w.Write([]byte(`{"status":"success","data":[{"id":2,"name":"Series Poster"}]}`))
		case "/people/types":
			w.Write([]byte(`{"status":"success","data":[{"id":3,"name":"Actor"}]}`))
		case "/seasons/100/extended":
			w.Write([]byte(`{"status":"success","data":{"id":100,"number":1,"seriesId":73244,"companies":{"studio":[{"id":2,"name":"Studio"}]},"translations":[{"language":"eng","name":"Season 1"}],"episodes":[{"id":10,"seriesId":73244,"seasonNumber":1,"number":1,"name":"Pilot","aired":"2005-03-24","image":"https://artworks.thetvdb.com/pilot.jpg"}]}}`))
		case "/episodes/10/extended":
			w.Write([]byte(`{"status":"success","data":{"id":10,"seriesId":73244,"seasonNumber":1,"number":1,"name":"Pilot","aired":"2005-03-24","overview":"Episode details","characters":[],"runtime":22}}`))
		case "/people/8/extended":
			w.Write([]byte(`{"status":"success","data":{"id":8,"name":"Steve Carell","birth":"1962-08-16","biographies":[{"language":"eng","biography":"Biography"}],"characters":[{"seriesId":73244,"series":{"name":"The Office","year":"2005","image":"https://artworks.thetvdb.com/poster.jpg"},"peopleType":"Actor","name":"Michael Scott"}]}}`))
		default:
			t.Errorf("unexpected eager request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "private-key"})
	p.tvdbURL = server.URL
	d, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "series", TitleID: "tt0386676"})
	if err != nil || len(d.People) != 1 || d.People[0].Type != "Actor" || d.Item.CommunityRating != nil || len(d.Children) != 1 || len(d.Images) != 1 || len(paths) != 5 {
		t.Fatalf("series contract: %+v %v %v", d, err, paths)
	}
	d, err = p.Fetch(context.Background(), "Tvdb", Request{Kind: "season", TitleID: "tt0386676", ProviderID: "100", ParentProviderID: "73244", Season: 1})
	if err != nil || len(d.Children) != 1 || d.Children[0].Image == "" || len(d.Item.Studios) != 1 || len(paths) != 6 {
		t.Fatalf("season contract: %+v %v %v", d, err, paths)
	}
	d, err = p.Fetch(context.Background(), "Tvdb", Request{Kind: "episode", TitleID: "tt0386676", ProviderID: "10", ParentProviderID: "73244", Season: 1, Number: 1, EpisodeName: "Pilot", EpisodeDate: "2005-03-24"})
	if err != nil || d.Item.Overview != "Episode details" || len(paths) != 7 {
		t.Fatal(d, err, paths)
	}
	d, err = p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "8"})
	if err != nil || d.Item.Overview != "Biography" || len(d.Credits) != 1 || d.Credits[0].Item.Name != "The Office" || len(paths) != 8 {
		t.Fatal(d, err, paths)
	}
	_, err = p.Fetch(context.Background(), "Tvdb", Request{Kind: "episode", ProviderID: "10", ParentProviderID: "73244", Season: 1, Number: 2, EpisodeName: "Different", EpisodeDate: "2005-03-25"})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatal("provider numbering changed TED identity", err)
	}
}

func TestTVDBEpisodeIdentityUsesProviderIDsAndOfficialTuple(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   error
	}{
		{"different release date", map[string]any{"aired": "2005-03-23"}, nil},
		{"different names and dates", map[string]any{"name": "生かす時間", "aired": "2020-07-14"}, nil},
		{"wrong provider ID", map[string]any{"id": 11}, ErrPartial},
		{"wrong parent series", map[string]any{"seriesId": 123}, ErrAmbiguous},
		{"wrong season", map[string]any{"seasonNumber": 2}, ErrAmbiguous},
		{"wrong episode", map[string]any{"number": 2}, ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := map[string]any{
				"id": 10, "seriesId": 73244, "seasonNumber": 1, "number": 1,
				"name": "Pilot", "aired": "2005-03-24", "characters": []any{},
				"image":        "https://artworks.thetvdb.com/episode.jpg",
				"translations": map[string]any{"nameTranslations": []any{map[string]any{"language": "eng", "name": "Provider title", "isPrimary": true}}},
			}
			for key, value := range tc.fields {
				record[key] = value
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/login":
					json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"token": "fixture-token"}})
				case "/episodes/10/extended":
					json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": record})
				case "/artwork/types", "/people/types":
					json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []any{}})
				default:
					t.Errorf("unexpected lookup: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TVDBKey: "fixture-key", Language: "en"})
			p.tvdbURL = server.URL
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "episode", TitleID: "tt0386676", ProviderID: "10", ParentProviderID: "73244", Season: 1, Number: 1, EpisodeName: "Pilot", EpisodeDate: "2005-03-24"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("identity result: got %v, want %v", err, tc.want)
			}
			if err == nil && (doc.ProviderID != "10" || doc.Item.Name != "Provider title" || doc.Item.PremiereDate.Format("2006-01-02") != record["aired"] || len(doc.Images) != 1) {
				t.Fatal("provider descriptions/artwork not returned", doc)
			}
		})
	}
}

func TestTVDBPrimaryTranslationsNeverSelectAliases(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		translations           []tvTranslation
		wantName, wantOverview string
	}{
		{"primary over other translations", []tvTranslation{{Language: "eng", Name: "Other name", Overview: "Other overview"}, {Language: "eng", Name: "The Office (US)", Overview: "Primary overview", IsPrimary: true}, {Language: "eng", Name: "Superfan alias", Overview: "Alias overview", IsAlias: true}}, "The Office (US)", "Primary overview"},
		{"first non-alias without primary", []tvTranslation{{Language: "eng", Name: "Alias first", Overview: "Alias first", IsAlias: true}, {Language: "eng", Name: "Localized name", Overview: "Localized overview"}, {Language: "eng", Name: "Alias last", Overview: "Alias last", IsAlias: true}}, "Localized name", "Localized overview"},
		{"retain original when only aliases exist", []tvTranslation{{Language: "eng", Name: "Superfan alias", Overview: "Alias overview", IsAlias: true}}, "Original name", "Original overview"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/login":
					w.Write([]byte(`{"status":"success","data":{"token":"fixture"}}`))
				case "/series/73244/extended":
					json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{
						"id": 73244, "name": "Original name", "overview": "Original overview", "remoteIds": []tvRemote{{ID: "tt0386676", SourceName: "IMDB"}}, "characters": []tvCharacter{},
						"translations": tvTranslations{NameTranslations: tc.translations, OverviewTranslations: tc.translations},
					}})
				case "/artwork/types", "/people/types":
					w.Write([]byte(`{"status":"success","data":[]}`))
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TVDBKey: "fixture"})
			p.tvdbURL = server.URL
			d, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "series", TitleID: "tt0386676", ProviderID: "73244"})
			if err != nil || d.Item.Name != tc.wantName || d.Item.Overview != tc.wantOverview {
				t.Fatalf("primary translation contract: %+v, %v", d, err)
			}
		})
	}
}

func TestTVDBLoginWaitCancellationAndSafeFailures(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		w.Write([]byte(`{"status":"success","data":{"token":"secret"}}`))
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TVDBKey: "private"})
	p.tvdbURL = server.URL
	done := make(chan error, 1)
	go func() { _, err := p.token(context.Background()); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.token(ctx)
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("waiting login ignored cancellation", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private-credential-in-provider-error", 503)
	}))
	defer bad.Close()
	p.tmdbURL = bad.URL
	p.config.TMDBToken = "private"
	_, err = p.ResolveCredit(context.Background(), "Tmdb", "movie", "1")
	if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "private") {
		t.Fatal("provider error disclosed credentials", err)
	}
}
