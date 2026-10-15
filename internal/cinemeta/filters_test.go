package cinemeta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestDiscoveryCatalogFilters(t *testing.T) {
	for _, tc := range []struct {
		filters Filters
		path    string
	}{
		{Filters{Catalog: "popular", Genre: "Sci-Fi"}, "/catalog/movie/top/skip=50&genre=Sci-Fi.json"},
		{Filters{Catalog: "featured", Genre: "Action"}, "/catalog/movie/imdbRating/skip=50&genre=Action.json"},
		{Filters{Catalog: "new", Year: 2025}, "/catalog/movie/year/skip=50&genre=2025.json"},
	} {
		c := fake(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.EscapedPath() != tc.path {
				t.Error(r.URL.EscapedPath())
			}
			fmt.Fprint(w, `{"metas":[`+movieJSON+`]}`)
		})
		p, err := c.Browse(context.Background(), "", 50, tc.filters)
		if err != nil || len(p.Movies) != 1 || p.NextSkip == nil || *p.NextSkip != 100 {
			t.Fatal(p, err)
		}
	}
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog/series/year/skip=0&genre=2025.json" {
			t.Error(r.URL.Path)
		}
		fmt.Fprint(w, `{"metas":[]}`)
	})
	p, err := c.BrowseSeries(context.Background(), "", 0, Filters{Catalog: "new", Year: 2025})
	if err != nil || p.NextSkip != nil || len(p.Series) != 0 {
		t.Fatal(p, err)
	}
}
func TestDiscoveryRejectsUnsupportedCombinations(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid filters reached upstream") })
	for _, f := range []Filters{{Catalog: "unknown"}, {Genre: "../../wrong"}, {Catalog: "new"}, {Catalog: "new", Year: 2025, Genre: "Action"}, {Year: 2025}, {Catalog: "featured", Year: 2025}, {Catalog: "new", Year: 9999}} {
		if _, err := c.Browse(context.Background(), "", 0, f); !errors.Is(err, ErrInvalid) {
			t.Fatal(f, err)
		}
	}
	for _, f := range []Filters{{Catalog: "featured"}, {Genre: "Action"}, {Catalog: "new", Year: 2025}} {
		if _, err := c.Browse(context.Background(), "Batman", 0, f); !errors.Is(err, ErrInvalid) {
			t.Fatal(f, err)
		}
	}
	if _, err := c.BrowseSeries(context.Background(), "", 0, Filters{Catalog: "new", Year: 1959}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
