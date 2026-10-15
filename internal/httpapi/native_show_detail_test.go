package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/watchstate"
)

func TestNativeWholeShowWatchIsAtomicAndKeepsHistory(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	if _, err := f.pool.Exec(context.Background(), `UPDATE watch_current SET state=state || '{"position_ticks":0,"last_played":null}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	path := "/api/client/v1/titles/series/" + show.ID + "/watch-state"
	read := func() watchstate.NativeSeriesWatch {
		r := nativeCall(t, h, "GET", path, pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		var state watchstate.NativeSeriesWatch
		if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := read()
	if len(before.Items) != 2 {
		t.Fatal(before)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": false}, nil), 200)
	after := read()
	for _, ep := range after.Items {
		if ep.Played || ep.Revision < 2 {
			t.Fatal(ep)
		}
	}
	// A repeated stale toggle cannot partially change newer episode state.
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": true}, nil), 409)
	if read().Version != after.Version {
		t.Fatal("stale mutation changed state")
	}
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": after.Version, "watched": true}, nil), 200)
	for _, ep := range read().Items {
		if !ep.Played {
			t.Fatal(ep)
		}
	}
	var history int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM watch_history WHERE user_id=$1 AND kind IN ('ted_watched','ted_unwatched')`, f.admin.ID).Scan(&history); err != nil || history != 4 {
		t.Fatal(history, err)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, "", map[string]any{"version": read().Version, "watched": false}, nil), 401)
}

func TestNativeWholeShowWatchIncludesUnseenSpecialsAndFutureEpisodes(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `DELETE FROM watch_current WHERE user_id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	show.Episodes = append(show.Episodes,
		cinemeta.Episode{ID: show.ID + ":0:1", Name: "Special", Season: 0, Number: 1},
		cinemeta.Episode{ID: show.ID + ":2:1", Name: "Future", Season: 2, Number: 1, ReleaseDate: "2099-01-01"})
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	path := "/api/client/v1/titles/series/" + show.ID + "/watch-state"
	r := nativeCall(t, h, "GET", path, pair.AccessToken, nil, nil)
	requireStatus(t, r, 200)
	var state watchstate.NativeSeriesWatch
	if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 4 {
		t.Fatal(state)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": state.Version, "watched": true}, nil), 200)
	for _, ep := range show.Episodes {
		current, err := f.api.watch.NativeState(ctx, f.admin.ID, catalog.MediaIdentity{Kind: "episode", ID: ep.ID})
		if err != nil || !current.Played {
			t.Fatal(ep.ID, current, err)
		}
	}
	// Explicit marking does not add files or library membership. Poster counts
	// still use released regular episodes only, independently of the bulk edit.
	title := nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/titles/series/"+show.ID)
	if title.EpisodeCounts == nil || title.EpisodeCounts.Total != 2 || title.EpisodeCounts.Watched != 2 {
		t.Fatal(title)
	}
}

func TestNativeWholeShowWatchRejectsChangedEpisodeAndRevokedFamily(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE watch_current SET state=state || '{"position_ticks":0,"last_played":null}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	state, err := f.api.watch.NativeSeriesWatch(ctx, f.admin.ID, show.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE watch_current SET revision=revision+1 WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, show.Episodes[1].ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/client/v1/titles/series/" + show.ID + "/watch-state"
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": state.Version, "watched": false}, nil), 409)
	var history int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, f.admin.ID).Scan(&history); err != nil || history != 0 {
		t.Fatal(history, err)
	}
	state, err = f.api.watch.NativeSeriesWatch(ctx, f.admin.ID, show.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/logout", pair.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": state.Version, "watched": false}, nil), 401)
	after, err := f.api.watch.NativeSeriesWatch(ctx, f.admin.ID, show.ID)
	if err != nil || state.Version != after.Version {
		t.Fatal(after, err)
	}
}
