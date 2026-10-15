// Package cinemeta adapts the public Cinemeta catalog without exposing arbitrary upstream URLs.
package cinemeta

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid catalog request")
	ErrUnavailable = errors.New("Cinemeta unavailable")
	ErrIncomplete  = errors.New("incomplete Cinemeta response")
	ErrNotFound    = errors.New("movie not found")
)
var imdb = regexp.MustCompile(`^tt[0-9]{7,12}$`)

// Cinemeta's popular catalog uses 50 upstream slots per page. Some slots are
// omitted from responses; advancing by the returned length repeats earlier items.
const PageSize = 50

func ValidID(id string) bool { return imdb.MatchString(id) }

type Movie struct {
	IMDbRating  *float64 `json:"imdb_rating,omitempty"`
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Year        string   `json:"year"`
	Description string   `json:"description"`
	Poster      string   `json:"poster"`
	Background  string   `json:"background,omitempty"`
	Logo        string   `json:"logo,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Genres      []string `json:"genres"`
}
type Page struct {
	Movies   []Movie `json:"movies"`
	Skip     int     `json:"skip"`
	NextSkip *int    `json:"next_skip,omitempty"`
}
type rawMovie struct {
	IMDbRating  json.RawMessage `json:"imdbRating"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Year        string          `json:"releaseInfo"`
	Description string          `json:"description"`
	Poster      string          `json:"poster"`
	Background  string          `json:"background"`
	Logo        string          `json:"logo"`
	Runtime     string          `json:"runtime"`
	Genres      []string        `json:"genres"`
}

func textOK(s string, limit int) bool {
	if !utf8.ValidString(s) || len(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}
func (m rawMovie) movie() (Movie, error) { return m.title("movie") }
func (m rawMovie) title(kind string) (Movie, error) {
	if !ValidID(m.ID) || m.Type != kind || strings.TrimSpace(m.Name) == "" || !textOK(m.Name, 1024) || !textOK(m.Year, 64) || !textOK(m.Description, 20000) || len(m.Genres) > 50 {
		return Movie{}, ErrIncomplete
	}
	for _, g := range m.Genres {
		if !textOK(g, 100) {
			return Movie{}, ErrIncomplete
		}
	}
	// Only the known Cinemeta artwork hosts are used in the browser. Missing or other artwork is optional.
	poster := PosterURL(m.Poster)
	genres := m.Genres
	if genres == nil {
		genres = []string{}
	}
	runtime := m.Runtime
	if !textOK(runtime, 128) {
		runtime = ""
	}
	return Movie{ID: m.ID, Type: m.Type, Name: m.Name, Year: m.Year, Description: m.Description, Poster: poster, Background: PosterURL(m.Background), Logo: PosterURL(m.Logo), Runtime: runtime, Genres: genres, IMDbRating: parseRating(m.IMDbRating)}, nil
}

// PosterURL limits browser artwork to the catalog's known image hosts.
func PosterURL(value string) string {
	u, err := url.Parse(value)
	if err == nil && len(value) < 2048 && u.Scheme == "https" && (u.Host == "images.metahub.space" || u.Host == "live.metahub.space" || u.Host == "m.media-amazon.com" || u.Host == "image.tmdb.org") && u.User == nil {
		return u.String()
	}
	return ""
}

type Client struct {
	base string
	http *http.Client
}

func New() *Client {
	return &Client{base: "https://v3-cinemeta.strem.io", http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: catalogRedirect}}
}

// Cinemeta delegates popular catalogs to this official service. Follow only
// bounded HTTPS redirects within the known catalog hosts, never arbitrary URLs.
func catalogRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Host != "v3-cinemeta.strem.io" && req.URL.Host != "cinemeta-catalogs.strem.io" && req.URL.Host != "cinemeta-live.strem.io") {
		return http.ErrUseLastResponse
	}
	return nil
}
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return ErrNotFound
	}
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil {
		return ErrUnavailable
	}
	if len(data) > 4<<20 || json.Unmarshal(data, out) != nil {
		return ErrIncomplete
	}
	return nil
}
func (c *Client) Browse(ctx context.Context, search string, skip int, filters Filters) (Page, error) {
	return c.browse(ctx, "movie", search, skip, filters)
}
func (c *Client) browse(ctx context.Context, kind, search string, skip int, filters Filters) (Page, error) {
	search = strings.TrimSpace(search)
	catalog, err := filters.catalogID(kind, search)
	if err != nil {
		return Page{}, err
	}
	if !textOK(search, 200) || skip < 0 || skip > 10000 || skip%PageSize != 0 || (search != "" && skip != 0) {
		return Page{}, ErrInvalid
	}
	extra := "skip=" + strconv.Itoa(skip)
	if search != "" {
		// Search is finite and ignores skip. Use Stremio's path-extra encoding
		// and canonical search-only URL so Cinemeta can share its cached result
		// instead of starting a separate cold search for redundant pagination.
		extra = "search=" + strings.ReplaceAll(url.QueryEscape(search), "+", "%20")
	}
	if filters.Genre != "" {
		extra += "&genre=" + url.QueryEscape(filters.Genre)
	}
	if catalog == "year" {
		extra += "&genre=" + strconv.Itoa(filters.Year)
	}
	var raw struct {
		Metas *[]rawMovie `json:"metas"`
	}
	if err := c.get(ctx, "/catalog/"+kind+"/"+catalog+"/"+extra+".json", &raw); err != nil {
		return Page{}, err
	}
	if raw.Metas == nil || len(*raw.Metas) > 200 || (search == "" && len(*raw.Metas) > PageSize) {
		return Page{}, ErrIncomplete
	}
	result := Page{Movies: []Movie{}, Skip: skip}
	seen := map[string]bool{}
	for _, r := range *raw.Metas {
		m, err := r.title(kind)
		if err != nil || seen[m.ID] {
			return Page{}, ErrIncomplete
		}
		seen[m.ID] = true
		result.Movies = append(result.Movies, m)
	}
	// Search ignores skip on the live service; its returned result set is not paginated.
	// Browsing provides no total. A subsequent empty page establishes the end.
	if search == "" && len(result.Movies) > 0 && skip+PageSize <= 10000 {
		n := skip + PageSize
		result.NextSkip = &n
	}
	return result, nil
}
func (c *Client) Movie(ctx context.Context, id string) (Movie, error) {
	if !ValidID(id) {
		return Movie{}, ErrInvalid
	}
	var raw struct {
		Meta *rawMovie `json:"meta"`
	}
	if err := c.get(ctx, "/meta/movie/"+id+".json", &raw); err != nil {
		return Movie{}, err
	}
	if raw.Meta == nil || raw.Meta.ID != id {
		return Movie{}, ErrIncomplete
	}
	return raw.Meta.movie()
}

// Ratings are optional: malformed or unavailable values must not hide a title.
func parseRating(raw json.RawMessage) *float64 {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 || number > 10 {
		return nil
	}
	return &number
}
