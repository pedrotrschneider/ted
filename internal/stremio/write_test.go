package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func writeRecord(t *testing.T, kind string, state map[string]any) Record {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"_id": "tt1234567", "type": kind, "name": "Fixture", "_mtime": "2026-01-01T00:00:00Z", "removed": false, "temp": false, "unknown": map[string]any{"nested": true}, "state": state})
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestWatchedPatchPreservesMovieFields(t *testing.T) {
	before := writeRecord(t, "movie", map[string]any{"timesWatched": 3, "timeOffset": 12345, "duration": 98765, "lastWatched": "2025-12-31T00:00:00Z", "flaggedWatched": 1, "futureField": "keep"})
	raw, err := WatchedPatch(before, before.ID, false, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	played, err := MovieWatched(after)
	if err != nil || played {
		t.Fatal(played, err)
	}
	delete(before.State, "timesWatched")
	delete(after.State, "timesWatched")
	a, _ := json.Marshal(before.State)
	b, _ := json.Marshal(after.State)
	if !sameRecord(a, b, false) {
		t.Fatal("Playback or unrelated fields changed")
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if string(fields["unknown"]) != "{\"nested\":true}" || string(fields["removed"]) != "false" {
		t.Fatal("Library fields lost")
	}
	original := writeRecord(t, "movie", map[string]any{"timesWatched": 3})
	noop, err := WatchedPatch(original, original.ID, true, nil, time.Now())
	if err != nil || !sameRecord(noop, original.Raw, false) {
		t.Fatal("Already watched must preserve count and timestamp")
	}
}
func TestWatchedPatchEpisodePreservesOtherFlags(t *testing.T) {
	ids := []string{"tt1234567:1:1", "tt1234567:1:2", "tt1234567:2:1"}
	encoded, err := EncodeWatched(ids, map[string]bool{ids[0]: true, ids[2]: true})
	if err != nil {
		t.Fatal(err)
	}
	before := writeRecord(t, "series", map[string]any{"watched": encoded, "timeOffset": 456, "video_id": ids[2], "timesWatched": 9})
	for _, played := range []bool{true, false} {
		raw, err := WatchedPatch(before, ids[1], played, ids, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		after, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		var value string
		json.Unmarshal(after.State["watched"], &value)
		flags, err := DecodeWatched(value, ids)
		if err != nil || flags[ids[1]] != played || !flags[ids[0]] || !flags[ids[2]] {
			t.Fatal(flags, err)
		}
		for _, key := range []string{"timeOffset", "video_id", "timesWatched"} {
			if string(after.State[key]) != string(before.State[key]) {
				t.Fatal("Unrelated field changed", key)
			}
		}
	}
	if _, err = WatchedPatch(before, "tt1234567:1:9", true, ids, time.Now()); err == nil {
		t.Fatal("Unknown episode accepted")
	}
	if _, err = WatchedPatch(before, ids[1], true, ids[:2], time.Now()); err == nil {
		t.Fatal("Dropped positive episode flag accepted")
	}
}
func TestStremioWriteProtocolAndMissingRead(t *testing.T) {
	record := writeRecord(t, "movie", map[string]any{"timesWatched": 1})
	puts := 0
	missing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("Bad request")
		}
		if string(body["authKey"]) != "\"test-key\"" || string(body["collection"]) != "\"libraryItem\"" {
			t.Error("Wrong request scope")
		}
		switch r.URL.Path {
		case "/datastoreGet":
			if string(body["all"]) != "false" {
				t.Error("Unbounded read")
			}
			if missing {
				w.Write([]byte(`{"result":[]}`))
			} else {
				json.NewEncoder(w).Encode(map[string]any{"result": []json.RawMessage{record.Raw}})
			}
		case "/datastorePut":
			puts++
			var changes []json.RawMessage
			json.Unmarshal(body["changes"], &changes)
			if len(changes) != 1 || !sameRecord(changes[0], record.Raw, false) {
				t.Error("Record not preserved")
			}
			w.Write([]byte(`{"result":{"success":true}}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{base: server.URL, http: server.Client()}
	got, err := client.ReadRecord(context.Background(), "test-key", record.ID)
	if err != nil || got.ID != record.ID {
		t.Fatal(err)
	}
	if err = client.PutRecord(context.Background(), "test-key", got.Raw); err != nil {
		t.Fatal(err)
	}
	missing = true
	if _, err = client.ReadRecord(context.Background(), "test-key", record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("Confirmed absence not distinguished", err)
	}
	if puts != 1 {
		t.Fatal(puts)
	}
}

func TestRecordVersionFormattingPreservesConflictDetection(t *testing.T) {
	a := json.RawMessage(`{"_mtime":"2026-01-01T00:00:00Z","state":{"timesWatched":1}}`)
	b := json.RawMessage(`{"_mtime":"2026-01-01T00:00:00.000Z","state":{"timesWatched":1}}`)
	c := json.RawMessage(`{"_mtime":"2026-01-01T00:00:00.001Z","state":{"timesWatched":1}}`)
	if !sameRecord(a, b, false) || sameRecord(a, c, false) {
		t.Fatal("Version formatting confused with a changed record")
	}
}

func TestNewSeriesRecordCanMarkOnlySpecial(t *testing.T) {
	meta := cinemeta.Movie{ID: "tt1224144", Type: "series", Name: "Reborn!", Poster: "https://example.test/poster.jpg"}
	record, err := newWatchedRecord(meta, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"tt1224144:0:1", "tt1224144:1:1", "tt1224144:1:2"}
	raw, err := WatchedPatch(record, ids[0], true, ids, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	var encoded string
	json.Unmarshal(after.State["watched"], &encoded)
	flags, err := DecodeWatched(encoded, ids)
	if err != nil || !flags[ids[0]] || flags[ids[1]] || flags[ids[2]] {
		t.Fatal(flags, err)
	}
	if string(after.State["timeOffset"]) != "0" {
		t.Fatal("invented playback progress")
	}
}
func TestMissingRecordRequiresExplicitEmptyArray(t *testing.T) {
	for _, result := range []string{"null", "{}", "[null]"} {
		t.Run(result, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"result":` + result + `}`)) }))
			defer server.Close()
			c := &Client{base: server.URL, http: server.Client()}
			if _, err := c.ReadRecord(context.Background(), "key", "tt1234567"); !errors.Is(err, ErrIncomplete) {
				t.Fatal(err)
			}
		})
	}
}

func TestCreatedRecordNormalizationDoesNotHideChangedState(t *testing.T) {
	expected := json.RawMessage(`{"_mtime":"2026-01-01T00:00:00Z","state":{"video_id":null,"watched":"flags"},"behaviorHints":{}}`)
	actual := json.RawMessage(`{"_mtime":"2026-01-01T00:00:00Z","background":"","logo":"","year":"","state":{"video_id":"","watched":"flags","season":0,"episode":0}}`)
	if !sameCreatedRecord(actual, expected) {
		t.Fatal("datastore creation defaults not recognized")
	}
	for _, changed := range []string{strings.Replace(string(actual), `"flags"`, `"different"`, 1), strings.Replace(string(actual), `"season":0`, `"season":1`, 1), strings.Replace(string(actual), `"year":""`, `"year":"2026"`, 1), strings.Replace(string(actual), `00:00:00Z`, `00:00:01Z`, 1)} {
		if sameCreatedRecord(json.RawMessage(changed), expected) {
			t.Fatal("actual remote change ignored")
		}
	}
}
