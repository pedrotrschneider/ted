package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/cinemeta"
)

func TestMissingTVDBEpisodeCastStillAdmitsTMDBSupplement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/episodes/101/extended":
			fmt.Fprint(w, `{"status":"success","data":{"id":101,"name":"Episode","seriesId":10,"seasonNumber":6,"number":4}}`)
		case "/find/101":
			fmt.Fprint(w, `{"tv_episode_results":[{"id":201,"show_id":70,"season_number":6,"episode_number":4}]}`)
		case "/tv/70/season/6/episode/4":
			fmt.Fprint(w, `{"id":201,"season_number":6,"episode_number":4,"external_ids":{"tvdb_id":101},"credits":{"cast":[{"id":5,"name":"New voice actor","character":"New lead (voice)"}],"guest_stars":[]}}`)
		default:
			t.Errorf("unexpected provider operation %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := NewProviders(ProviderConfig{TMDBToken: "fixture", TVDBKey: "fixture"})
	p.tvdbURL = server.URL
	p.tmdbURL = server.URL
	p.tvdbToken = "fixture"
	p.tokenUntil = time.Now().Add(time.Hour)
	p.typesUntil = time.Now().Add(time.Hour)
	doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "episode", TitleID: "tt1234567", ProviderID: "101", ParentProviderID: "10", Season: 6, Number: 4, CanonicalEpisodes: []cinemeta.Episode{{ID: "tt1234567:6:4", TVDBID: 101}}})
	if err != nil || !doc.ScopedCastChecked || len(doc.People) != 1 || doc.People[0].Name != "New voice actor" || doc.People[0].Role != "New lead" || doc.FieldSources["People"] != "Tmdb" {
		t.Fatal(doc, err)
	}
}

func TestScopedCastUsesEpisodeIdentityAcrossAnthologiesAndRetainsDistinctRoles(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/find/101":
					fmt.Fprint(w, `{"tv_episode_results":[{"id":201,"show_id":70,"season_number":1,"episode_number":1}],"tv_season_results":[{"id":999}]}`)
				case "/find/102":
					fmt.Fprint(w, `{"tv_episode_results":[{"id":202,"show_id":80,"season_number":1,"episode_number":1}]}`)
				case "/tv/70/season/1/episode/1", "/tv/80/season/1/episode/1":
					id, tvdb, role := 201, 101, "First role"
					if strings.Contains(r.URL.Path, "/80/") {
						id, tvdb, role = 202, 102, "Second role"
					}
					if bad {
						tvdb = 999
					}
					fmt.Fprintf(w, `{"id":%d,"season_number":1,"episode_number":1,"external_ids":{"tvdb_id":%d},"credits":{"cast":[{"id":5,"name":"Actor","character":%q}],"guest_stars":[{"id":5,"name":"Actor","character":%q},{"id":6,"name":"Guest","character":"Guest"}]}}`, id, tvdb, role, role)
				default:
					t.Errorf("unexpected lookup %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TMDBToken: "fixture"})
			p.tmdbURL = server.URL
			doc := Document{ProviderID: "1000", Item: Item{Name: "Canonical season"}, People: []PersonSummary{{Provider: "Tvdb", ID: "4", Name: "Old guest"}}}
			r := Request{Kind: "season", TitleID: "tt1234567", Season: 2, CanonicalEpisodes: []cinemeta.Episode{{ID: "tt1234567:2:1", TVDBID: 101, ReleaseDate: "2020-01-01"}, {ID: "tt1234567:2:2", TVDBID: 102, ReleaseDate: "2020-01-02"}, {ID: "tt1234567:2:3", TVDBID: 103, ReleaseDate: "2099-01-01"}}}
			got, err := p.tmdbTVDBCast(context.Background(), r, doc)
			if bad {
				if !errors.Is(err, ErrAmbiguous) || got.People != nil || got.ScopedCastChecked {
					t.Fatal("wrong association replaced cast", got, err)
				}
				return
			}
			if err != nil || !got.ScopedCastChecked || got.ProviderID != "1000" || got.Item.Name != "Canonical season" || got.FieldSources["People"] != "Tmdb" || len(got.People) != 3 {
				t.Fatal(got, err)
			}
			if got.People[0].Role != "First role" || got.People[2].Role != "Second role" {
				t.Fatal("roles collapsed across episodes", got.People)
			}
		})
	}
}

func TestTMDBStructureReordersExistingIdentitiesAndRejectsUnmatchedEpisodes(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/tv/70/season/20":
					fmt.Fprint(w, `{"id":71,"season_number":20,"episodes":[{"id":201,"name":"Different name","season_number":20,"episode_number":432,"air_date":"2015-10-01"},{"id":202,"name":"Other","season_number":20,"episode_number":451,"air_date":"2016-03-03"}]}`)
				case "/tv/70/season/20/episode/432/external_ids":
					fmt.Fprint(w, `{"id":201,"tvdb_id":101}`)
				case "/tv/70/season/20/episode/451/external_ids":
					id := 102
					if missing {
						id = 999
					}
					fmt.Fprintf(w, `{"id":202,"tvdb_id":%d}`, id)
				default:
					t.Errorf("unexpected lookup %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TMDBToken: "fixture"})
			p.tmdbURL = server.URL
			canonical := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1234567", Type: "series", Name: "Series"}, Episodes: []cinemeta.Episode{{ID: "tt1234567:20:1", Season: 20, Number: 1, Name: "Original", TVDBID: 101}, {ID: "tt1234567:21:1", Season: 21, Number: 1, Name: "Original", TVDBID: 102}}}
			doc := Document{ProviderID: "70", Children: []Child{{Request: Request{Kind: "season", Season: 20, ProviderID: "71"}}}}
			got, err := p.SeriesStructure(context.Background(), "Tmdb", canonical, doc)
			if missing {
				if !errors.Is(err, ErrAmbiguous) {
					t.Fatal("guessed an unmatched episode", got, err)
				}
				return
			}
			if err != nil || len(got.Episodes) != 2 || got.Episodes[0].ID != "tt1234567:20:1" || got.Episodes[1].ID != "tt1234567:21:1" || got.Episodes[1].Season != 20 || got.Episodes[1].Number != 451 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestTMDBSeriesNeverUsesTVDBAndRejectsAConflictingParent(t *testing.T) {
	var tvdb atomic.Int32
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/find/tt1234567":
					fmt.Fprint(w, `{"tv_results":[{"id":70}]}`)
				case "/tv/70":
					fmt.Fprint(w, `{"id":70,"name":"Chosen series","external_ids":{"imdb_id":"tt1234567"},"seasons":[{"id":71,"season_number":20}],"credits":{"cast":[],"crew":[]}}`)
				case "/find/101":
					parent := 70
					if wrong {
						parent = 80
					}
					fmt.Fprintf(w, `{"tv_episode_results":[{"id":201,"show_id":%d,"season_number":20,"episode_number":432}]}`, parent)
				case "/tv/70/season/20/episode/432":
					fmt.Fprint(w, `{"id":201,"name":"Episode","season_number":20,"episode_number":432,"external_ids":{"tvdb_id":101},"credits":{"cast":[],"guest_stars":[]}}`)
				default:
					tvdb.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TMDBToken: "fixture", TVDBKey: "unused"})
			p.tmdbURL, p.tvdbURL = server.URL, server.URL
			r := Request{Kind: "episode", TitleID: "tt1234567", Season: 20, Number: 432, CanonicalEpisodes: []cinemeta.Episode{{ID: "tt1234567:20:1", TVDBID: 101}}}
			got, err := p.Fetch(context.Background(), "Tmdb", r)
			if wrong {
				if !errors.Is(err, ErrAmbiguous) {
					t.Fatal(got, err)
				}
				return
			}
			if err != nil || !got.ScopedCastChecked || got.ProviderID != "201" {
				raw, _ := json.Marshal(got)
				t.Fatal(string(raw), err)
			}
		})
	}
	if tvdb.Load() != 0 {
		t.Fatal("TMDb series contacted TVDB")
	}
}
