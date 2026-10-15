package stremio

import (
	"encoding/json"
	"strings"
	"time"
)

// copyUnion can only add watched flags. The current destination owns all other
// fields. Retained intent also protects watched flags observed before a retry.
func copyUnion(current Record, entries []Entry, ids []string, retained json.RawMessage, at time.Time) (json.RawMessage, error) {
	var value, state map[string]json.RawMessage
	if json.Unmarshal(current.Raw, &value) != nil || json.Unmarshal(value["state"], &state) != nil || state == nil {
		return nil, ErrIncomplete
	}
	var previous Record
	if len(retained) > 0 {
		var err error
		previous, err = parseRecord(retained)
		if err != nil || previous.ID != current.ID || previous.Type != current.Type {
			return nil, ErrIncomplete
		}
	}
	changed := false
	switch current.Type {
	case "movie":
		watched, err := MovieWatched(current)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.ID != current.ID {
				return nil, ErrAmbiguous
			}
		}
		if !watched && len(entries) > 0 {
			state["timesWatched"] = json.RawMessage("1")
			changed = true
		}
	case "series":
		for _, id := range ids {
			if !strings.HasPrefix(id, current.ID+":") {
				return nil, ErrAmbiguous
			}
		}
		flags, err := copyFlags(current, ids)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, e := range entries {
			wanted[e.ID] = true
		}
		if len(retained) > 0 {
			old, err := copyFlags(previous, ids)
			if err != nil {
				return nil, err
			}
			for id, played := range old {
				if played {
					wanted[id] = true
				}
			}
		}
		for id := range wanted {
			played, ok := flags[id]
			if !ok {
				return nil, ErrAmbiguous
			}
			if !played {
				flags[id] = true
				changed = true
			}
		}
		if changed {
			encoded, err := EncodeWatched(ids, flags)
			if err != nil {
				return nil, err
			}
			state["watched"], _ = json.Marshal(encoded)
		}
	default:
		return nil, ErrAmbiguous
	}
	if !changed {
		return append(json.RawMessage(nil), current.Raw...), nil
	}
	value["state"], _ = json.Marshal(state)
	if at.UnixMilli() <= current.Modified {
		at = time.UnixMilli(current.Modified + 1)
	}
	value["_mtime"], _ = json.Marshal(at.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano))
	return json.Marshal(value)
}

func copyFlags(r Record, ids []string) (map[string]bool, error) {
	var encoded string
	if raw := r.State["watched"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &encoded) != nil {
			return nil, ErrIncomplete
		}
	}
	return DecodeWatched(encoded, ids)
}
