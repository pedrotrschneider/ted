package stremio

import (
	"encoding/json"
	"strings"
	"time"

	"entertainmentdistrict/internal/cinemeta"
)

// A title record exposes only its current video, not a per-viewing event log.
// Require real viewing and completion position; an old watched bit alone is
// insufficient during a rewatch. These hints never import resume state.
type episodeCompletion struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

func (c episodeCompletion) valid(title string) bool {
	return cinemeta.ValidVideoID(c.ID) && strings.SplitN(c.ID, ":", 2)[0] == title &&
		c.At.Year() >= 1970 && c.At.Year() <= 2100
}

func (t pollTitle) validCompletion(title string) bool {
	if t.Completion == nil {
		return true
	}
	if !t.Completion.valid(title) {
		return false
	}
	for _, e := range t.Entries {
		if e.ID == t.Completion.ID && e.Season > 0 {
			return true
		}
	}
	return false
}

func completedEpisode(r Record, entries []Entry, now time.Time, before *pollTitle) *episodeCompletion {
	if r.Type != "series" {
		return nil
	}
	var state struct {
		ID       string     `json:"video_id"`
		At       *time.Time `json:"lastWatched"`
		Watched  int64      `json:"timeWatched"`
		Position int64      `json:"timeOffset"`
		Duration int64      `json:"duration"`
		Count    *uint32    `json:"timesWatched"`
	}
	raw, _ := json.Marshal(r.State)
	if json.Unmarshal(raw, &state) != nil || state.At == nil || state.Duration <= 0 || state.Duration > 604800000 ||
		state.Watched < 0 || state.Watched > 9007199254740991 || state.Position < 0 || state.Position > state.Duration ||
		state.Watched < (state.Duration*9+9)/10 || state.Position != 0 && state.Position*10 < state.Duration*9 ||
		state.At.After(now.Add(5*time.Minute)) {
		return nil
	}
	hint := episodeCompletion{state.ID, state.At.UTC()}
	if !hint.valid(r.ID) {
		return nil
	}
	regularWatched := false
	for _, e := range entries {
		if e.ID == hint.ID && e.Season > 0 {
			regularWatched = true
			break
		}
	}
	if !regularWatched {
		return nil
	}
	var previous struct {
		State struct {
			ID      string     `json:"video_id"`
			At      *time.Time `json:"lastWatched"`
			Watched int64      `json:"timeWatched"`
			Count   *uint32    `json:"timesWatched"`
		} `json:"state"`
	}
	countIncreased := false
	if before != nil && json.Unmarshal(before.Record, &previous) == nil {
		countIncreased = state.Count != nil && previous.State.Count != nil && *state.Count > *previous.State.Count
		// Some clients retain viewing counters when replaying the same video.
		// Seeking to its end must not reuse the earlier viewing as a new one.
		// Keep an accepted completion, or require a new counter transition or
		// another full threshold of real viewing after the previous observation.
		if previous.State.ID == state.ID && previous.State.At != nil && state.At.After(*previous.State.At) &&
			previous.State.Watched >= (state.Duration*9+9)/10 && state.Watched >= previous.State.Watched &&
			state.Watched-previous.State.Watched < (state.Duration*9+9)/10 && !countIncreased {
			if before.Completion != nil && before.Completion.ID == state.ID {
				return before.Completion
			}
			return nil
		}
	}
	// Zero is also a fresh start or seek. A carried-over viewing counter cannot
	// prove a new completion there: require a count increase or the exact hint
	// already accepted from a previous read.
	if state.Position == 0 {
		proven := before != nil && before.Completion != nil && *before.Completion == hint
		proven = proven || countIncreased
		if !proven {
			return nil
		}
	}
	return &hint
}
