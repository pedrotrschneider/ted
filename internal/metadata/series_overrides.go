package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrOverridesReview = errors.New("review series overrides before changing provider")

// SeriesOverrides describes manual edits only, including hidden old seasons.
// The version fences edits made after the confirmation was presented.
type SeriesOverrides struct {
	Artwork  int    `json:"artwork"`
	Metadata int    `json:"metadata"`
	Version  string `json:"version"`
}

type OverrideDecision struct {
	Action  string `json:"override_action"`
	Version string `json:"overrides_version"`
}

func seriesOverrides(ctx context.Context, q scopeReader, title string) (SeriesOverrides, error) {
	state := SeriesOverrides{}
	parts := []any{}
	rows, err := q.Query(ctx, `SELECT a.id::text,a.generation FROM metadata_artwork a JOIN metadata_entities e ON e.id=a.entity_id WHERE e.request->>'title_id'=$1 AND e.kind IN ('series','season','episode') AND a.user_modified ORDER BY a.id`, title)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var id string
		var generation int64
		if err = rows.Scan(&id, &generation); err != nil {
			break
		}
		parts = append(parts, []any{"artwork", id, generation})
		state.Artwork++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return state, err
	}
	rows, err = q.Query(ctx, `SELECT id::text,item FROM metadata_entities WHERE request->>'title_id'=$1 AND kind IN ('series','season','episode') ORDER BY id`, title)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var id string
		var item Item
		if err = rows.Scan(&id, &item); err != nil {
			break
		}
		if (item.LockData == nil || !*item.LockData) && len(item.LockedFields) == 0 {
			continue
		}
		raw, _ := json.Marshal(item)
		values := map[string]json.RawMessage{}
		if err = json.Unmarshal(raw, &values); err != nil {
			break
		}
		locked := map[string]json.RawMessage{}
		if item.LockData != nil && *item.LockData {
			locked = values
		} else {
			for _, field := range item.LockedFields {
				switch field {
				case "Cast":
					field = "People"
				case "Runtime":
					field = "RunTimeTicks"
				}
				locked[field] = values[field]
			}
		}
		parts = append(parts, []any{"metadata", id, locked})
		state.Metadata++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return state, err
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return state, err
	}
	digest := sha256.Sum256(raw)
	state.Version = hex.EncodeToString(digest[:])
	return state, nil
}

func (d OverrideDecision) validate(current SeriesOverrides) error {
	if d.Action != "" && d.Action != "keep" && d.Action != "remove" {
		return ErrInvalid
	}
	if d.Version != "" && d.Version != current.Version {
		return ErrOverridesReview
	}
	if current.Artwork+current.Metadata > 0 && (d.Action == "" || d.Version == "") {
		return ErrOverridesReview
	}
	return nil
}

// Order this lock before the asset lock so provider changes and manual edits
// cannot race an override review, clear a new edit, or deadlock in reverse order.
func lockArtworkSeries(ctx context.Context, tx pgx.Tx, id string) error {
	var kind, title string
	if err := tx.QueryRow(ctx, `SELECT e.kind,COALESCE(e.request->>'title_id','') FROM metadata_artwork a JOIN metadata_entities e ON e.id=a.entity_id WHERE a.id=$1`, id).Scan(&kind, &title); err != nil {
		return err
	}
	if kind == "series" || kind == "season" || kind == "episode" {
		if !titleID.MatchString(title) {
			return fmt.Errorf("artwork series: %w", ErrInvalid)
		}
		_, err := lockSeriesProvider(ctx, tx, title)
		return err
	}
	return nil
}
