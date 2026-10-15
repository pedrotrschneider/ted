package stremio

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAccountCreationDateIsSeparateFromModificationAndViewing(t *testing.T) {
	for _, created := range []any{"2025-10-13T12:00:00Z", nil, "invalid", "2027-01-01T12:00:00Z"} {
		raw, _ := json.Marshal(map[string]any{"_id": "tt2359704", "type": "series", "_mtime": "2026-10-13T12:00:00Z", "_ctime": created, "state": map[string]any{"lastWatched": "2026-10-01T00:00:00Z"}})
		v, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		if created == "2025-10-13T12:00:00Z" {
			want, _ := time.Parse(time.RFC3339, created.(string))
			if v.AddedAt == nil || !v.AddedAt.Equal(want) {
				t.Fatal(v)
			}
		} else if v.AddedAt != nil {
			t.Fatal("invented account-addition date", v.AddedAt)
		}
	}
}
