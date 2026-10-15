package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fallbackProviders(t *testing.T, biography string, ids []tvRemote, tmdb http.HandlerFunc) *Providers {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Write([]byte(`{"status":"success","data":{"token":"tvdb-fixture"}}`))
		case "/people/255067/extended":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{
				"id": 255067, "name": "Jenna Fischer", "image": "https://artworks.thetvdb.com/jenna.jpg", "remoteIds": ids,
				"biographies": []map[string]string{{"language": "eng", "biography": biography}},
				"characters":  []map[string]any{{"seriesId": 73244, "name": "Pam Beesly", "series": map[string]string{"name": "The Office", "year": "2005"}}},
			}})
		default:
			if r.Header.Get("Authorization") != "Bearer tmdb-fixture" || r.URL.Query().Get("language") != "en" {
				t.Error("missing fallback authentication/language")
			}
			tmdb(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProviders(ProviderConfig{TVDBKey: "tvdb-fixture", TMDBToken: "tmdb-fixture"})
	p.tvdbURL, p.tmdbURL = server.URL, server.URL
	return p
}

func TestTVDBPersonBiographyFallbackVerifiedIdentityAndScope(t *testing.T) {
	for _, tc := range []struct {
		name, biography string
		ids             []tvRemote
		find, fallback  bool
	}{
		{"explicit TMDb link", "", []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}, {ID: "nm0278979", SourceName: "IMDB"}}, false, true},
		{"IMDb lookup only", "", []tvRemote{{ID: "nm0278979", SourceName: "IMDB"}}, true, true},
		{"TVDB biography remains authoritative", "TVDB biography", []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			p := fallbackProviders(t, tc.biography, tc.ids, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/find/nm0278979":
					if r.URL.Query().Get("external_source") != "imdb_id" {
						t.Error("wrong identifier source")
					}
					w.Write([]byte(`{"person_results":[{"id":51856}]}`))
				case "/person/51856":
					if r.URL.Query().Get("append_to_response") != "external_ids" {
						t.Error("fallback eagerly requested filmography")
					}
					w.Write([]byte(`{"id":51856,"name":"A different display name","biography":"TMDb biography","external_ids":{"imdb_id":"nm0278979"}}`))
				default:
					t.Errorf("unexpected fallback endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "255067"})
			if err != nil || doc.ProviderID != "255067" || doc.Item.Name != "Jenna Fischer" || len(doc.Credits) != 1 || doc.Credits[0].Provider != "Tvdb" || doc.Credits[0].Roles[0] != "Pam Beesly" || len(doc.Images) != 1 {
				t.Fatal("fallback changed primary person/credits/artwork", doc, err)
			}
			if tc.fallback {
				wantCalls := 1
				if tc.find {
					wantCalls++
				}
				if doc.Item.Overview != "TMDb biography" || doc.FieldSources["Overview"] != "Tmdb" || doc.Item.ProviderIDs["Tmdb"] != "51856" || !doc.TMDBBiographyChecked || len(calls) != wantCalls {
					t.Fatal("missing verified fallback", doc, calls)
				}
			} else if doc.Item.Overview != tc.biography || len(calls) != 0 {
				t.Fatal("fallback replaced existing biography or guessed identity", doc, calls)
			}
		})
	}
}

func TestTVDBBiographyFallbackFailuresKeepPrimaryData(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		ids            []tvRemote
		want           error
	}{
		{"conflicting external ID", `{"id":51856,"name":"Wrong person","biography":"Wrong biography","external_ids":{"imdb_id":"nm0000001"}}`, 200, []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}, {ID: "nm0278979", SourceName: "IMDB"}}, ErrAmbiguous},
		{"ambiguous IMDb lookup", `{"person_results":[{"id":51856},{"id":2}]}`, 200, []tvRemote{{ID: "nm0278979", SourceName: "IMDB"}}, ErrAmbiguous},
		{"fallback outage", "", 503, []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}}, ErrProvider},
		{"successful missing match", `{"person_results":[]}`, 200, []tvRemote{{ID: "nm0278979", SourceName: "IMDB"}}, nil},
		{"successful empty biography", `{"id":51856,"name":"Jenna Fischer","biography":""}`, 200, []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}}, nil},
		{"conflicting TVDB links", "", 200, []tvRemote{{ID: "51856", SourceName: "TheMovieDB.com"}, {ID: "7", SourceName: "Tmdb"}}, ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fallbackProviders(t, "", tc.ids, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/search/person" {
					t.Error("supplied/conflicting identifiers fell through to name search")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.response))
			})
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "255067"})
			if !errors.Is(err, tc.want) || doc.ProviderID != "255067" || doc.Item.Name != "Jenna Fischer" || doc.Item.Overview != "" {
				t.Fatal("failure lost primary data or imported unverified biography", doc, err)
			}
			if tc.name != "conflicting TVDB links" && (len(doc.Credits) != 1 || !doc.TMDBBiographyChecked) {
				t.Fatal("fallback failure discarded primary credits or attempt state", doc)
			}
			if tc.name == "conflicting TVDB links" && doc.Item.ProviderIDs != nil {
				t.Fatal("conflicting cross-provider IDs remained publishable", doc.Item.ProviderIDs)
			}
		})
	}
}

func TestTVDBBiographyFallbackCancellationAndMissingConfiguration(t *testing.T) {
	for _, ids := range [][]tvRemote{nil, {{ID: "51856", SourceName: "TheMovieDB.com"}}} {
		t.Run(map[bool]string{true: "name search", false: "identifier lookup"}[ids == nil], func(t *testing.T) {
			entered := make(chan struct{})
			p := fallbackProviders(t, "", ids, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				doc Document
				err error
			}
			done := make(chan result, 1)
			go func() {
				doc, err := p.Fetch(ctx, "Tvdb", Request{Kind: "person", ProviderID: "255067"})
				done <- result{doc, err}
			}()
			<-entered
			cancel()
			r := <-done
			if !errors.Is(r.err, context.Canceled) || r.doc.Item.Name != "Jenna Fischer" || len(r.doc.Credits) != 1 || r.doc.Item.Overview != "" {
				t.Fatal("cancelled fallback lost primary data or published a biography", r)
			}
			p.config.TMDBToken = ""
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "255067"})
			if err != nil || doc.TMDBBiographyChecked || doc.TMDBBiographyNameChecked || doc.Item.Overview != "" || len(doc.Credits) != 1 {
				t.Fatal("missing TMDb configuration was treated as completed lookup", doc, err)
			}
		})
	}
}
