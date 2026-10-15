package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"fmt"
)

type Catalog interface {
	Series(context.Context, string) (cinemeta.Series, error)
}
type Entry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

// Resolve returns watched identities only, without importing Stremio positions or
// claiming lastWatched is an original episode completion date.
func Resolve(ctx context.Context, r Record, catalog Catalog) ([]Entry, []string, error) {
	entries, ids := []Entry{}, []string{}
	if !cinemeta.ValidID(r.ID) {
		return nil, nil, ErrAmbiguous
	}
	switch r.Type {
	case "movie":
		watched, err := MovieWatched(r)
		if err != nil {
			return nil, nil, err
		}
		if watched {
			entries = append(entries, Entry{ID: r.ID, Name: r.Name})
		}
		return entries, ids, nil
	case "series":
		var value string
		if raw, ok := r.State["watched"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &value) != nil {
				return nil, nil, ErrIncomplete
			}
		}
		if value == "" {
			return entries, ids, nil
		}
		show, err := catalog.Series(ctx, r.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range show.Episodes {
			ids = append(ids, e.ID)
		}
		flags, err := DecodeWatched(value, ids)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range show.Episodes {
			if flags[e.ID] {
				name := e.Name
				if name == "" {
					name = fmt.Sprintf("Episode %d", e.Number)
				}
				entries = append(entries, Entry{e.ID, name, e.Season, e.Number})
			}
		}
		return entries, ids, nil
	default:
		return nil, nil, ErrAmbiguous
	}
}

func entryMetadata(r Record, e Entry) []byte {
	var wire struct {
		Poster string `json:"poster"`
	}
	_ = json.Unmarshal(r.Raw, &wire)
	name := r.Name
	if name == "" {
		name = r.ID
	}
	parent := cinemeta.Movie{ID: r.ID, Name: name, Type: r.Type, Poster: cinemeta.PosterURL(wire.Poster), Genres: []string{}}
	if r.Type == "movie" {
		value, _ := json.Marshal(parent)
		return value
	}
	value, _ := json.Marshal(map[string]any{"id": e.ID, "type": "episode", "name": fmt.Sprintf("%s · S%d E%d · %s", name, e.Season, e.Episode, e.Name), "poster": parent.Poster, "series": parent})
	return value
}
