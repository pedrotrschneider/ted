package watchstate

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"entertainmentdistrict/internal/catalog"
	"github.com/jackc/pgx/v5"
)

type runtimeReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

var runtimeMinutes = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(?:min|minutes|m)?\s*$`)

// Runtime is advisory catalog metadata, kept separate from the actual selected
// file's duration. A series average is usable for regular episodes, not specials.
func ExpectedRuntime(ctx context.Context, q runtimeReader, media catalog.MediaIdentity) (int64, error) {
	keys := []string{media.Kind + ":" + media.ID}
	kind, title := media.Kind, media.ID
	if media.Kind == "episode" {
		p := strings.Split(media.ID, ":")
		kind, title = "series", p[0]
		if len(p) == 3 && p[1] != "0" {
			keys = append(keys, "series:"+p[0])
		}
	}
	var raw []byte
	e := q.QueryRow(ctx, `SELECT item FROM metadata_entities WHERE identity=ANY($1::text[]) AND item ? 'RunTimeTicks' ORDER BY CASE WHEN identity=$2 THEN 0 ELSE 1 END LIMIT 1`, keys, keys[0]).Scan(&raw)
	if e != nil && e != pgx.ErrNoRows {
		return 0, e
	}
	if e == nil {
		var v struct {
			Ticks int64 `json:"RunTimeTicks"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return 0, ErrInvalid
		}
		n := v.Ticks / 10000
		if n > 0 && n <= 604800000 {
			return n, nil
		}
	}
	if media.Kind == "episode" && len(keys) == 1 {
		return 0, nil
	}
	e = q.QueryRow(ctx, `SELECT metadata FROM native_catalog_titles WHERE kind=$1 AND title_id=$2`, kind, title).Scan(&raw)
	if e == pgx.ErrNoRows {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	var v struct {
		Runtime string `json:"runtime"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return 0, ErrInvalid
	}
	m := runtimeMinutes.FindStringSubmatch(v.Runtime)
	if m == nil {
		return 0, nil
	}
	minutes, e := strconv.ParseFloat(m[1], 64)
	if e != nil {
		return 0, nil
	}
	n := int64(minutes * 60000)
	if n <= 0 || n > 604800000 {
		return 0, nil
	}
	return n, nil
}
func ShortRuntime(actual, expected int64) bool {
	return actual > 0 && expected > 0 && actual*10 <= expected*3
}
