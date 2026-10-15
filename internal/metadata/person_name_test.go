package metadata

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestTVDBBiographyNameFallbackCandidateSelectionAndScope(t *testing.T) {
	for _, tc := range []struct {
		name, response, matched string
		pages                   int
	}{
		{"same-name candidates across pages", `{"page":1,"total_pages":9,"results":[{"id":7,"name":"Jenna Fischer","popularity":1000,"known_for_department":"Acting","known_for":[{"media_type":"tv","name":"Unrelated show"}]},{"id":8,"name":"Jenna F. Fischer","popularity":2000,"known_for":[{"media_type":"tv","name":"The Office"}]}]}`, "Jenna Fischer", 2},
		{"search accepts an alias with a known title", `{"page":1,"total_pages":1,"results":[{"id":7,"name":"Similar alias","popularity":1000},{"id":51856,"name":"Regina Fischer","popularity":1,"known_for":[{"media_type":"tv","name":"The Office"}]}]}`, "Regina Fischer", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searches, details := 0, 0
			p := fallbackProviders(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/search/person":
					searches++
					if r.URL.Query().Get("query") != "Jenna Fischer" || r.URL.Query().Get("include_adult") != "false" {
						t.Error("wrong name search")
					}
					if r.URL.Query().Get("page") == "1" {
						w.Write([]byte(tc.response))
					} else if r.URL.Query().Get("page") == "2" {
						w.Write([]byte(`{"page":2,"total_pages":9,"results":[{"id":51856,"name":"Jenna Fischer","popularity":1,"known_for_department":"Acting","known_for":[{"media_type":"tv","name":"The Office"}]}]}`))
					} else {
						t.Error("search exceeded its page bound")
					}
				case "/person/51856":
					details++
					if r.URL.Query().Get("append_to_response") != "external_ids" {
						t.Error("name fallback fetched filmography")
					}
					w.Write([]byte(`{"id":51856,"name":"` + tc.matched + `","biography":"Fallback biography"}`))
				default:
					t.Errorf("unexpected detail request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "255067"})
			if err != nil || doc.Item.Overview != "Fallback biography" || searches != tc.pages || details != 1 {
				t.Fatal("did not select the bounded best-effort match", doc, err, searches, details)
			}
			if doc.BiographyMatch == nil || doc.BiographyMatch.Method != "name" || doc.BiographyMatch.ProviderID != "51856" || doc.BiographyMatch.Name != tc.matched || doc.FieldSources["Overview"] != "Tmdb" || !doc.TMDBBiographyNameChecked {
				t.Fatal("name match lacks heuristic provenance", doc)
			}
			if doc.ProviderID != "255067" || doc.Item.Name != "Jenna Fischer" || doc.Item.ProviderIDs["Tmdb"] != "" || len(doc.Credits) != 1 || doc.Credits[0].Roles[0] != "Pam Beesly" || len(doc.Images) != 1 {
				t.Fatal("name match changed verified identity, primary credits or artwork", doc)
			}
		})
	}
}

func TestTVDBBiographyNameFallbackEmptyPartialAndFailedResponses(t *testing.T) {
	for _, tc := range []struct {
		name, search, details string
		status                int
		want                  error
	}{
		{"confirmed empty", `{"page":1,"total_pages":0,"results":[]}`, "", 200, nil},
		{"missing results", `{"page":1,"total_pages":1}`, "", 200, ErrPartial},
		{"missing pagination", `{"page":1,"results":[]}`, "", 200, ErrPartial},
		{"empty advertised page", `{"page":1,"total_pages":2,"results":[]}`, "", 200, ErrPartial},
		{"invalid page", `{"page":0,"total_pages":1,"results":[]}`, "", 200, ErrPartial},
		{"invalid candidate", `{"page":1,"total_pages":1,"results":[{"id":0,"name":"Jenna Fischer"}]}`, "", 200, ErrPartial},
		{"outage", "", "", 503, ErrProvider},
		{"candidate changed", `{"page":1,"total_pages":1,"results":[{"id":51856,"name":"Jenna Fischer"}]}`, `{"id":51856,"name":"Unrelated name","biography":"Wrong text"}`, 200, ErrAmbiguous},
		{"empty biography", `{"page":1,"total_pages":1,"results":[{"id":51856,"name":"Jenna Fischer"}]}`, `{"id":51856,"name":"Jenna Fischer","biography":""}`, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fallbackProviders(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if r.URL.Path == "/search/person" {
					w.Write([]byte(tc.search))
				} else {
					w.Write([]byte(tc.details))
				}
			})
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "person", ProviderID: "255067"})
			if !errors.Is(err, tc.want) || !doc.TMDBBiographyNameChecked || doc.Item.Overview != "" || doc.BiographyMatch != nil || doc.Item.ProviderIDs["Tmdb"] != "" || len(doc.Credits) != 1 {
				t.Fatal("name search failure/empty result lost primary data or published guessed association", doc, err)
			}
		})
	}
	// Provider-supplied names are query data, including punctuation and Unicode.
	p := fallbackProviders(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Zoë O'Connor & Sons?" || len(r.URL.Query()["query"]) != 1 {
			t.Error("name was not escaped as a single query value")
		}
		w.Write([]byte(`{"page":1,"total_pages":0,"results":[]}`))
	})
	if _, _, err := p.personByName(context.Background(), Document{Item: Item{Name: "Zoë O'Connor & Sons?"}}); err != nil {
		t.Fatal(err)
	}
}
