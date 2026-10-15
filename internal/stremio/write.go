package stremio

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// ReadRecord distinguishes confirmed absence from incomplete or failed reads.
func (c *Client) ReadRecord(ctx context.Context, key, id string) (Record, error) {
	var raw []json.RawMessage
	if err := c.post(ctx, "datastoreGet", map[string]any{"authKey": key, "collection": "libraryItem", "all": false, "ids": []string{id}}, &raw); err != nil {
		return Record{}, err
	}
	if raw != nil && len(raw) == 0 {
		return Record{}, ErrNotFound
	}
	if raw == nil || len(raw) != 1 {
		return Record{}, ErrIncomplete
	}
	r, err := parseRecord(raw[0])
	if err != nil {
		return Record{}, err
	}
	if r.ID != id {
		return Record{}, ErrIncomplete
	}
	return r, nil
}

// PutRecord replaces a full Stremio record. The service does not provide CAS:
// callers must retain the before/desired records durably, serialize account
// writes, check fresh state and verify the result. Never log these records.
func (c *Client) PutRecord(ctx context.Context, key string, raw json.RawMessage) error {
	if _, err := parseRecord(raw); err != nil {
		return err
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := c.post(ctx, "datastorePut", map[string]any{"authKey": key, "collection": "libraryItem", "changes": []json.RawMessage{raw}}, &result); err != nil {
		return err
	}
	if !result.Success {
		return ErrUnavailable
	}
	return nil
}

// WatchedPatch changes only the selected watched flag and the datastore version.
// Unknown JSON fields and unrelated watched episodes remain intact.
func WatchedPatch(r Record, identity string, played bool, ids []string, at time.Time) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if json.Unmarshal(r.Raw, &value) != nil {
		return nil, ErrIncomplete
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(value["state"], &state) != nil || state == nil {
		return nil, ErrIncomplete
	}
	switch r.Type {
	case "movie":
		if identity != r.ID {
			return nil, ErrAmbiguous
		}
		watched, err := MovieWatched(r)
		if err != nil {
			return nil, err
		}
		if watched == played {
			return append(json.RawMessage(nil), r.Raw...), nil
		}
		if played {
			state["timesWatched"] = json.RawMessage("1")
		} else {
			state["timesWatched"] = json.RawMessage("0")
		}
	case "series":
		if !strings.HasPrefix(identity, r.ID+":") {
			return nil, ErrAmbiguous
		}
		var encoded string
		if raw := state["watched"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &encoded) != nil {
				return nil, ErrIncomplete
			}
		}
		watched, err := DecodeWatched(encoded, ids)
		if err != nil {
			return nil, err
		}
		if _, ok := watched[identity]; !ok {
			return nil, ErrAmbiguous
		}
		if watched[identity] == played {
			return append(json.RawMessage(nil), r.Raw...), nil
		}
		watched[identity] = played
		encoded, err = EncodeWatched(ids, watched)
		if err != nil {
			return nil, err
		}
		state["watched"], _ = json.Marshal(encoded)
	default:
		return nil, ErrAmbiguous
	}
	value["state"], _ = json.Marshal(state)
	if at.UnixMilli() <= r.Modified {
		at = time.UnixMilli(r.Modified + 1)
	}
	value["_mtime"], _ = json.Marshal(at.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano))
	return json.Marshal(value)
}

func sameRecord(a, b json.RawMessage, ignoreVersion bool) bool {
	var x, y map[string]json.RawMessage
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	if !ignoreVersion && (len(x["_mtime"]) > 0 || len(y["_mtime"]) > 0) {
		var ax, by string
		if json.Unmarshal(x["_mtime"], &ax) != nil || json.Unmarshal(y["_mtime"], &by) != nil {
			return false
		}
		aTime, aErr := time.Parse(time.RFC3339Nano, ax)
		bTime, bErr := time.Parse(time.RFC3339Nano, by)
		if aErr != nil || bErr != nil || !aTime.Equal(bTime) {
			return false
		}
	}
	delete(x, "_mtime")
	delete(y, "_mtime")
	// Compare decoded values so field ordering and JSONB formatting are irrelevant.
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	var xv, yv any
	dx := json.NewDecoder(strings.NewReader(string(xb)))
	dx.UseNumber()
	dy := json.NewDecoder(strings.NewReader(string(yb)))
	dy.UseNumber()
	return dx.Decode(&xv) == nil && dy.Decode(&yv) == nil && reflect.DeepEqual(xv, yv)
}
