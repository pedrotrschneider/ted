package cinemeta

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var videoID = regexp.MustCompile(`^tt[0-9]{7,12}:[0-9]{1,4}:[0-9]{1,5}$`)

func ValidVideoID(id string) bool { return videoID.MatchString(id) }

type Episode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Season      int    `json:"season"`
	Number      int    `json:"number"`
	ReleaseDate string `json:"release_date,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	TVDBID      int64  `json:"tvdb_id,omitempty"`
	TMDBID      int64  `json:"tmdb_id,omitempty"`
}
type Series struct {
	Movie
	Episodes        []Episode `json:"episodes"`
	IdentityVersion int       `json:"-"`
}
type SeriesPage struct {
	Series   []Movie `json:"series"`
	Skip     int     `json:"skip"`
	NextSkip *int    `json:"next_skip,omitempty"`
}

func (c *Client) BrowseSeries(ctx context.Context, search string, skip int, filters Filters) (SeriesPage, error) {
	p, err := c.browse(ctx, "series", search, skip, filters)
	return SeriesPage{p.Movies, p.Skip, p.NextSkip}, err
}
func (c *Client) Series(ctx context.Context, id string) (Series, error) {
	if !ValidID(id) {
		return Series{}, ErrInvalid
	}
	var raw struct {
		Meta *struct {
			rawMovie
			Videos *[]struct {
				ID          string          `json:"id"`
				Name        string          `json:"name"`
				Title       string          `json:"title"`
				Season      *int            `json:"season"`
				Episode     *int            `json:"episode"`
				Released    string          `json:"released"`
				Overview    string          `json:"overview"`
				Description string          `json:"description"`
				Thumbnail   string          `json:"thumbnail"`
				TVDBID      json.RawMessage `json:"tvdb_id"`
				TMDBID      json.RawMessage `json:"moviedb_id"`
			} `json:"videos"`
		} `json:"meta"`
	}
	if err := c.get(ctx, "/meta/series/"+id+".json", &raw); err != nil {
		return Series{}, err
	}
	if raw.Meta == nil || raw.Meta.ID != id || raw.Meta.Videos == nil || len(*raw.Meta.Videos) > 5000 {
		return Series{}, ErrIncomplete
	}
	title, err := raw.Meta.title("series")
	if err != nil {
		return Series{}, err
	}
	result := Series{Movie: title, Episodes: []Episode{}}
	seen := map[string]bool{}
	for _, v := range *raw.Meta.Videos {
		if v.Season == nil || v.Episode == nil || *v.Season < 0 || *v.Season > 9999 || *v.Episode < 0 || *v.Episode > 99999 || v.ID != fmt.Sprintf("%s:%d:%d", id, *v.Season, *v.Episode) || seen[v.ID] {
			return Series{}, ErrIncomplete
		}
		name := v.Name
		if name == "" {
			name = v.Title
		}
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("Episode %d", *v.Episode)
		}
		if !textOK(name, 1024) {
			return Series{}, ErrIncomplete
		}
		seen[v.ID] = true
		description := v.Overview
		if description == "" {
			description = v.Description
		}
		if !textOK(description, 20000) {
			description = ""
		}
		result.Episodes = append(result.Episodes, Episode{ID: v.ID, Name: name, Season: *v.Season, Number: *v.Episode, ReleaseDate: episodeReleaseDate(v.Released), Description: description, Image: PosterURL(v.Thumbnail), TVDBID: externalEpisodeID(v.TVDBID), TMDBID: externalEpisodeID(v.TMDBID)})
	}
	sort.Slice(result.Episodes, func(i, j int) bool {
		a, b := result.Episodes[i], result.Episodes[j]
		return a.Season < b.Season || a.Season == b.Season && a.Number < b.Number
	})
	return result, nil
}

// External IDs are hints for verified provider association, never TED identity.
// Cinemeta emits both JSON numbers and strings; malformed optional hints stay absent.
func externalEpisodeID(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil && n > 0 && n < 1000000000000000 {
		return n
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		n, err := strconv.ParseInt(text, 10, 64)
		if err == nil && n > 0 && n < 1000000000000000 {
			return n
		}
	}
	return 0
}

// RegularEpisodeCount excludes specials from show library-completion indicators.
func RegularEpisodeCount(episodes []Episode) int {
	count := 0
	for _, episode := range episodes {
		if episode.Season > 0 {
			count++
		}
	}
	return count
}

// Release dates are calendar dates; do not shift midnight into the viewer's timezone.
func episodeReleaseDate(value string) string {
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.Format(time.DateOnly)
		}
	}
	return ""
}
