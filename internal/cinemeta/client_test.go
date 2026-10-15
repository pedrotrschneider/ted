package cinemeta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const movieJSON = `{"id":"tt1345836","type":"movie","name":"The Dark Knight Rises","releaseInfo":"2012","poster":"https://images.metahub.space/poster/small/tt1345836/img"}`

func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c := New()
	c.base = s.URL
	return c
}
func TestMovieIdentityAndBrowseEncoding(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/catalog/movie/top/search=A%20%26%20B%2FC.json":
			fmt.Fprint(w, `{"metas":[`+movieJSON+`]}`)
		case "/catalog/movie/top/skip=0.json":
			fmt.Fprint(w, `{"metas":[`+movieJSON+`]}`)
		case "/catalog/movie/top/skip=50.json":
			fmt.Fprint(w, `{"metas":[]}`)
		case "/meta/movie/tt1345836.json":
			fmt.Fprint(w, `{"meta":`+movieJSON+`}`)
		default:
			t.Errorf("unexpected route %s", r.URL.EscapedPath())
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	p, err := c.Browse(ctx, "A & B/C", 0, Filters{})
	if err != nil || len(p.Movies) != 1 || p.NextSkip != nil {
		t.Fatal(p, err)
	}
	p, err = c.Browse(ctx, "", 0, Filters{})
	if err != nil || p.NextSkip == nil || *p.NextSkip != 50 {
		t.Fatal("short upstream batch must advance by 50 slots", p, err)
	}
	p, err = c.Browse(ctx, "", 50, Filters{})
	if err != nil || len(p.Movies) != 0 || p.NextSkip != nil {
		t.Fatal(p, err)
	}
	m, err := c.Movie(ctx, "tt1345836")
	if err != nil || m.Year != "2012" || m.ID != "tt1345836" {
		t.Fatal(m, err)
	}
	if _, err = c.Movie(ctx, "../../bad"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = c.Browse(ctx, "query", 50, Filters{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("search must not paginate", err)
	}
}

func TestTextSearchExtrasPreserveLiteralCharactersAndStayUnpaginated(t *testing.T) {
	for _, kind := range []string{"movie", "series"} {
		t.Run(kind, func(t *testing.T) {
			const query = "Café + A&B / ?#"
			calls := 0
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				prefix := "/catalog/" + kind + "/top/"
				path := r.URL.EscapedPath()
				if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".json") || r.URL.RawQuery != "" {
					t.Fatalf("search escaped out of its catalog path: %s", path)
				}
				extra := strings.TrimSuffix(strings.TrimPrefix(path, prefix), ".json")
				values, err := url.ParseQuery(extra)
				if err != nil || len(values) != 1 || values.Get("search") != query || strings.Contains(extra, "+") {
					t.Fatalf("noncanonical or changed search extras: %q %v", extra, err)
				}
				fmt.Fprint(w, `{"metas":[]}`)
			})
			page, err := c.browse(context.Background(), kind, "  "+query+"  ", 0, Filters{})
			if err != nil || page.Movies == nil || len(page.Movies) != 0 || page.NextSkip != nil {
				t.Fatal("successful empty search must remain finite", page, err)
			}
			if _, err := c.browse(context.Background(), kind, query, 50, Filters{}); !errors.Is(err, ErrInvalid) || calls != 1 {
				t.Fatal("search pagination reached the provider", calls, err)
			}
		})
	}
}
func TestIncompleteCatalogNeverBecomesEmptySuccess(t *testing.T) {
	for _, body := range []string{`{}`, `{"metas":null}`, `{"metas":[` + movieJSON + `,` + movieJSON + `]}`, `{"metas":[{"id":"tt1345836","type":"series","name":"Wrong"}]}`, `{"metas":[]} trailing`} {
		c := fake(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.Browse(context.Background(), "", 0, Filters{}); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("accepted %s: %v", body, err)
		}
	}
}
func TestDetailMismatchArtworkAndProviderFailures(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"meta":`+strings.ReplaceAll(movieJSON, "tt1345836", "tt0111161")+`}`)
	})
	if _, err := c.Movie(context.Background(), "tt1345836"); !errors.Is(err, ErrIncomplete) {
		t.Fatal(err)
	}
	for _, url := range []string{"javascript:alert(1)", "https://images.metahub.space.evil.invalid/img", "http://127.0.0.1/img"} {
		m, err := (rawMovie{ID: "tt1345836", Type: "movie", Name: "Movie", Poster: url}).movie()
		if err != nil || m.Poster != "" {
			t.Fatal("unsafe artwork", err)
		}
	}
	c = fake(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://secret.invalid", 302) })
	if _, err := c.Movie(context.Background(), "tt1345836"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Movie(ctx, "tt1345836"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c = fake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	if _, err := c.Movie(context.Background(), "tt1345836"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCatalogRedirectBoundaries(t *testing.T) {
	for _, tc := range []struct {
		url     string
		allowed bool
	}{{"https://cinemeta-catalogs.strem.io/top/catalog/movie/top/skip=0.json", true}, {"https://v3-cinemeta.strem.io/catalog/movie/top.json", true}, {"http://cinemeta-catalogs.strem.io/catalog", false}, {"https://cinemeta-catalogs.strem.io.evil.invalid/catalog", false}, {"https://user@cinemeta-catalogs.strem.io/catalog", false}} {
		req, err := http.NewRequest("GET", tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if (catalogRedirect(req, []*http.Request{req}) == nil) != tc.allowed {
			t.Fatal("wrong redirect decision", tc.url)
		}
		if catalogRedirect(req, []*http.Request{req, req, req}) == nil {
			t.Fatal("redirect loop accepted")
		}
	}
}

func TestPosterLiveHostAndLookalikes(t *testing.T) {
	for _, tc := range []struct {
		url     string
		allowed bool
	}{{"https://live.metahub.space/poster/small/tt6933238/img", true}, {"https://images.metahub.space/poster/small/tt6933238/img", true}, {"https://live.metahub.space.evil.invalid/poster", false}, {"http://live.metahub.space/poster", false}, {"https://user@live.metahub.space/poster", false}} {
		m, err := (rawMovie{ID: "tt6933238", Type: "movie", Name: "Movie", Poster: tc.url}).movie()
		if err != nil || (m.Poster != "") != tc.allowed {
			t.Fatalf("incorrect poster validation for %s: %v", tc.url, err)
		}
	}
}

func TestOptionalIMDbRating(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{`"7.5"`, 7.5}, {`8.4`, 8.4}, {`"10"`, 10}, {`null`, 0}, {`"N/A"`, 0}, {`"NaN"`, 0}, {`"11"`, 0}, {`"0"`, 0},
	} {
		c := fake(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"meta":{"id":"tt1345836","type":"movie","name":"Movie","imdbRating":%s}}`, tc.raw)
		})
		m, err := c.Movie(context.Background(), "tt1345836")
		if err != nil {
			t.Fatal(err)
		}
		if tc.want == 0 {
			if m.IMDbRating != nil {
				t.Fatal(tc.raw, *m.IMDbRating)
			}
		} else if m.IMDbRating == nil || *m.IMDbRating != tc.want {
			t.Fatal(tc.raw, m.IMDbRating)
		}
	}
}
