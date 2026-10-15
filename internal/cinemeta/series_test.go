package cinemeta

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestSeriesEpisodeIdentityAndIncompleteResponses(t *testing.T) {
	for _, tc := range []struct {
		name, videos string
		valid        bool
	}{
		{"season and special", `[{"id":"tt1245698:1:2","name":"Second","season":1,"episode":2},{"id":"tt1245698:0:1","title":"Special","season":0,"episode":1}]`, true},
		{"missing list", `null`, false},
		{"wrong identity", `[{"id":"tt1245698:2:1","name":"Wrong","season":1,"episode":1}]`, false},
		{"missing season", `[{"id":"tt1245698:0:1","name":"Unknown","episode":1}]`, false},
		{"duplicates", `[{"id":"tt1245698:1:1","season":1,"episode":1},{"id":"tt1245698:1:1","season":1,"episode":1}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"meta":{"id":"tt1245698","type":"series","name":"Birdy","videos":%s}}`, tc.videos)
			})
			s, err := c.Series(context.Background(), "tt1245698")
			if (err == nil) != tc.valid {
				t.Fatalf("%+v %v", s, err)
			}
			if tc.valid && (s.Episodes[0].Season != 0 || s.Episodes[0].Name != "Special") {
				t.Fatal(s)
			}
		})
	}
}
func TestSeriesBrowseUsesSeriesCatalog(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/series/top/search=Birdy.json" {
			t.Error(r.URL.Path)
		}
		fmt.Fprint(w, `{"metas":[{"id":"tt1245698","type":"series","name":"Birdy"}]}`)
	})
	p, err := c.BrowseSeries(context.Background(), "Birdy", 0, Filters{})
	if err != nil || len(p.Series) != 1 || p.NextSkip != nil {
		t.Fatal(p, err)
	}
}

func TestSeriesReleaseDates(t *testing.T) {
	for _, tc := range []struct{ name, released, want string }{
		{"timestamp", "2002-12-01T00:00:00.000Z", "2002-12-01"},
		{"calendar date", "2026-10-09", "2026-10-09"},
		{"offset preserves calendar day", "2026-10-09T00:00:00+09:00", "2026-10-09"},
		{"missing", "", ""},
		{"invalid calendar date", "2026-02-30", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"meta":{"id":"tt1245698","type":"series","name":"Birdy","videos":[{"id":"tt1245698:1:1","season":1,"episode":1,"released":%q}]}}`, tc.released)
			})
			show, err := c.Series(context.Background(), "tt1245698")
			if err != nil {
				t.Fatal(err)
			}
			if got := show.Episodes[0].ReleaseDate; got != tc.want {
				t.Fatalf("release date = %q, want %q", got, tc.want)
			}
		})
	}
}
