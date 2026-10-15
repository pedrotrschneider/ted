package stremio

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/watchstate"
)

func recencyRecord(t *testing.T, video string, at time.Time, position, watched, duration int64, version int) Record {
	t.Helper()
	bits, err := EncodeWatched([]string{"tt2934286:1:1", "tt2934286:1:2"}, map[string]bool{"tt2934286:1:1": true, "tt2934286:1:2": true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"_id": "tt2934286", "type": "series", "name": "Rewatch", "_mtime": time.Date(2026, 10, 11, 12, 0, version, 0, time.UTC).Format(time.RFC3339), "state": map[string]any{"watched": bits, "video_id": video, "lastWatched": at, "timeOffset": position, "timeWatched": watched, "duration": duration}})
	r, err := parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStremioEpisodeRecencyRewatchWithoutFlagChangesAndRestart(t *testing.T) {
	s, who, remote := storeFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt2934286", Type: "series", Name: "Rewatch"}, Episodes: []cinemeta.Episode{{ID: "tt2934286:1:1", Season: 1, Number: 1, ReleaseDate: "2000-01-01"}, {ID: "tt2934286:1:2", Season: 1, Number: 2, ReleaseDate: "2000-01-01"}}}
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	remote.records = []Record{recencyRecord(t, show.Episodes[1].ID, at, 0, 95000, 100000, 1)}
	connectSync(t, s, who)
	pollAndDeliver(t, s)
	check := func(want string) {
		t.Helper()
		up, err := watchstate.New(s.pool).PersonalRows(ctx, who.ID, "next-up", "", "2026-10-11")
		if err != nil || (want == "" && len(up.Items) != 0) || (want != "" && (len(up.Items) != 1 || up.Items[0].Media.ID != want || !up.Items[0].State.Played)) {
			t.Fatal("Stremio recency successor", up, err)
		}
	}
	checkAnchor := func(want string, wantAt time.Time) {
		t.Helper()
		var id string
		var savedAt time.Time
		if err := s.pool.QueryRow(ctx, `SELECT c.cinemeta_id,r.watched_at FROM stremio_episode_recency r JOIN catalog_media c ON c.id=r.media_id WHERE r.user_id=$1 ORDER BY r.watched_at DESC LIMIT 1`, who.ID).Scan(&id, &savedAt); err != nil || id != want || !savedAt.Equal(wantAt) {
			t.Fatal("completed rewatch anchor", id, savedAt, err)
		}
	}
	check("")
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// Already-watched episode 1 is now being replayed. A partial viewing must
	// not reuse the old bit as completion evidence or import its resume offset.
	remote.records = []Record{recencyRecord(t, show.Episodes[0].ID, at.Add(time.Minute), 30000, 30000, 100000, 2)}
	pollAndDeliver(t, s)
	check("")
	remote.records = []Record{recencyRecord(t, show.Episodes[0].ID, at.Add(2*time.Minute), 95000, 95000, 100000, 3)}
	pollAndDeliver(t, s)
	// All successors remain watched, so Next Up stays empty even though the
	// completed rewatch moves the durable recency anchor back to episode 1.
	check("")
	checkAnchor(show.Episodes[0].ID, at.Add(2*time.Minute))
	s = NewStore(s.pool, remote, catalogStub{})
	pollAndDeliver(t, s)
	check("")
	checkAnchor(show.Episodes[0].ID, at.Add(2*time.Minute))
	// Starting the next episode must retain the last completed anchor.
	remote.records = []Record{recencyRecord(t, show.Episodes[1].ID, at.Add(3*time.Minute), 10000, 10000, 100000, 4)}
	pollAndDeliver(t, s)
	check("")
	checkAnchor(show.Episodes[0].ID, at.Add(2*time.Minute))
	var unchanged bool
	if err := s.pool.QueryRow(ctx, `SELECT count(*)=2 AND bool_and((state->>'played')::boolean AND (state->>'position_ticks')::bigint=0 AND state->>'last_played' IS NULL) FROM watch_current WHERE user_id=$1`, who.ID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("recency changed watch state", unchanged, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&count); err != nil || count != 2 {
		t.Fatal("recency fabricated history", count, err)
	}
}

func TestStremioCompletionHintRequiresCanonicalCompletedViewing(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Millisecond)
	entries := []Entry{{ID: "tt2934286:1:1", Season: 1, Episode: 1}}
	for _, test := range []struct {
		name, id                    string
		position, watched, duration int64
		at                          time.Time
		valid                       bool
	}{
		{"threshold", "tt2934286:1:1", 90000, 90000, 100000, at, true},
		{"zero without completion evidence", "tt2934286:1:1", 0, 95000, 100000, at, false},
		{"partial rewatch with old counter", "tt2934286:1:1", 10000, 150000, 100000, at, false},
		{"seek without viewing", "tt2934286:1:1", 95000, 1000, 100000, at, false},
		{"unknown duration", "tt2934286:1:1", 0, 0, 0, at, false},
		{"wrong show", "tt9999999:1:1", 95000, 95000, 100000, at, false},
		{"unknown episode", "tt2934286:1:9", 95000, 95000, 100000, at, false},
		{"special", "tt2934286:0:1", 95000, 95000, 100000, at, false},
		{"future timestamp", "tt2934286:1:1", 95000, 95000, 100000, at.Add(time.Hour), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := recencyRecord(t, test.id, test.at, test.position, test.watched, test.duration, 1)
			if got := completedEpisode(r, entries, at, nil); (got != nil) != test.valid {
				t.Fatal(got)
			}
		})
	}
	withCount := func(r Record, count int) Record {
		t.Helper()
		var record map[string]any
		if err := json.Unmarshal(r.Raw, &record); err != nil {
			t.Fatal(err)
		}
		record["state"].(map[string]any)["timesWatched"] = count
		raw, _ := json.Marshal(record)
		parsed, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	old := withCount(recencyRecord(t, "tt2934286:1:1", at.Add(-time.Minute), 30000, 30000, 100000, 1), 1)
	done := withCount(recencyRecord(t, "tt2934286:1:1", at, 0, 95000, 100000, 2), 2)
	prior := pollTitle{Record: old.Raw, Entries: entries}
	hint := completedEpisode(done, entries, at, &prior)
	if hint == nil {
		t.Fatal("completed counter transition at zero not recognized")
	}
	prior = pollTitle{Record: done.Raw, Entries: entries, Completion: hint}
	if got := completedEpisode(done, entries, at, &prior); got == nil {
		t.Fatal("accepted completion lost on unchanged read")
	}
	reset := withCount(recencyRecord(t, "tt2934286:1:1", at.Add(time.Minute), 0, 95000, 100000, 3), 2)
	if got := completedEpisode(reset, entries, at.Add(time.Minute), &prior); got != nil && !got.At.Equal(hint.At) {
		t.Fatal("fresh zero/old viewing counter fabricated a newer completion", got)
	}
	seek := withCount(recencyRecord(t, "tt2934286:1:1", at.Add(time.Minute), 95000, 96000, 100000, 3), 2)
	if got := completedEpisode(seek, entries, at.Add(time.Minute), &prior); got == nil || !got.At.Equal(hint.At) {
		t.Fatal("seek reused previous viewing as a new completion", got)
	}
	if got := completedEpisode(seek, nil, at.Add(time.Minute), &prior); got != nil {
		t.Fatal("unwatched episode retained completion hint", got)
	}
	prior.Completion = nil
	if got := completedEpisode(seek, entries, at.Add(time.Minute), &prior); got != nil {
		t.Fatal("unverified old counters fabricated completion after seek", got)
	}
}
