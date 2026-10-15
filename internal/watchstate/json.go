package watchstate

import (
	"bytes"
	"encoding/json"
)

// A partial external read must never become a default unwatched state.
func completeJSON(raw []byte, target any, nullable string, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || key != nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func (s *State) UnmarshalJSON(raw []byte) error {
	type plain State
	var out plain
	if err := completeJSON(raw, &out, "last_played", "played", "position_ticks", "play_count", "last_played"); err != nil {
		return err
	}
	*s = State(out)
	return nil
}
