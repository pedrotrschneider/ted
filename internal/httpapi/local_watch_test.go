package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/watchstate"
	"testing"
)

func TestLocalWatchDiscoveryWithoutMappings(t *testing.T) {
	f, _ := posterFixture(t)
	if _, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	cookie := login(t, h, "viewer")
	requireStatus(t, call(t, h, "GET", "/api/series/tt1245698", nil, cookie), 200)
	read := func() watchstate.TitleState {
		t.Helper()
		r := call(t, h, "GET", "/api/watch-titles/tt1245698", nil, cookie)
		requireStatus(t, r, 200)
		var v watchstate.TitleState
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := read()
	if len(v.Items) != 2 {
		t.Fatal(v)
	}
	requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1245698", map[string]any{"version": v.Version, "episode": "tt1245698:1:1", "played": true}, cookie), 202)
	v = read()
	if !v.Items[0].Played || v.Items[0].Pending || v.Items[1].Played {
		t.Fatal(v)
	}
	if w := posterRead(t, f, cookie, "series", "tt1245698"); !w.Seen || w.Watched != 1 || w.Available != 2 {
		t.Fatal(w)
	}
	if got := libraryIDs(t, f, cookie, "kind=series&watched=true"); len(got) != 1 || got[0] != "tt1245698" {
		t.Fatal(got)
	}
	var mappings int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_mappings`).Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal(mappings, err)
	}
	requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1245698", map[string]any{"version": v.Version, "played": false}, cookie), 202)
	for _, item := range read().Items {
		if item.Played || item.Pending {
			t.Fatal(item)
		}
	}
}

func TestWatchTitleDateModes(t *testing.T) {
	f, _ := posterFixture(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/series/tt1245698", nil, cookie), 200)
	for _, mode := range []string{"now", "undated"} {
		var title watchstate.TitleState
		r := call(t, h, "GET", "/api/watch-titles/tt1245698", nil, cookie)
		requireStatus(t, r, 200)
		if err := json.Unmarshal(r.Body.Bytes(), &title); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1245698", map[string]any{"version": title.Version, "played": true, "date_mode": "yesterday"}, cookie), 400)
		episode := "tt1245698:1:1"
		if mode == "undated" {
			episode = "tt1245698:1:2"
		}
		requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1245698", map[string]any{"version": title.Version, "episode": episode, "played": true, "date_mode": mode}, cookie), 202)
		r = call(t, h, "GET", "/api/watch-history", nil, cookie)
		requireStatus(t, r, 200)
		var history watchstate.History
		if err := json.Unmarshal(r.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if len(history.Entries) == 0 || history.Entries[0].Kind != "ted_watched" || (history.Entries[0].State.LastPlayed == nil) != (mode == "undated") {
			t.Fatal(history)
		}
	}
}
