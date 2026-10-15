package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/watchstate"
)

func nativeSeasonWatchFixture(t *testing.T) (*fixture, cinemeta.Series) {
	t.Helper()
	f, show := nativeSeriesWatchedFixture(t)
	if _, err := f.pool.Exec(context.Background(), `UPDATE watch_current SET state=state || '{"position_ticks":0,"last_played":null}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	return f, show
}

func TestNativeSeasonWatchScopesAllEpisodesAndPreservesSiblings(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	show.Episodes = append(show.Episodes,
		cinemeta.Episode{ID: show.ID + ":0:1", Season: 0, Number: 1, Name: "Special"},
		cinemeta.Episode{ID: show.ID + ":2:1", Season: 2, Number: 1, Name: "Future", ReleaseDate: "2099-01-01"})
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	read := func(season int) watchstate.NativeSeriesWatch {
		r := nativeCall(t, h, "GET", "/api/client/v1/titles/series/"+show.ID+"/seasons/"+strconv.Itoa(season)+"/watch-state", pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		var state watchstate.NativeSeriesWatch
		if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	edit := func(season int, watched bool) {
		before := read(season)
		path := "/api/client/v1/titles/series/" + show.ID + "/seasons/" + strconv.Itoa(season) + "/watch-state"
		requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": watched}, nil), 200)
		for _, ep := range read(season).Items {
			if ep.Played != watched {
				t.Fatal(ep)
			}
		}
	}
	// A whole-show badge is not the selected season's watch state. Every
	// canonical episode in this season is included, irrespective of release.
	if len(read(1).Items) != 2 || len(read(2).Items) != 1 || len(read(0).Items) != 1 {
		t.Fatal("wrong scope")
	}
	seasonOne := read(1)
	edit(2, true)
	edit(0, true)
	if read(1).Version != seasonOne.Version {
		t.Fatal("another season changed the selected season")
	}
	edit(1, false)
	for _, season := range []int{0, 2} {
		if !read(season).Items[0].Played {
			t.Fatal("sibling season was cleared")
		}
	}
	edit(1, true)
	var history, mappings int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1 AND kind IN ('ted_watched','ted_unwatched')`, f.admin.ID).Scan(&history); err != nil || history != 6 {
		t.Fatal(history, err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM media_mappings`).Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal(mappings, err)
	}
	title := nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/titles/series/"+show.ID)
	if title.EpisodeCounts == nil || title.EpisodeCounts.Total != 2 || title.EpisodeCounts.Watched != 2 {
		t.Fatal(title)
	}
}

func TestNativeSeasonWatchRejectsStateChangedWhileWaitingAndRevokedFamily(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	path := "/api/client/v1/titles/series/" + show.ID + "/seasons/1/watch-state"
	before, err := f.api.watch.NativeSeasonWatch(ctx, f.admin.ID, show.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": false}, nil).Code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked int
		if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id, username,%FROM users WHERE id=$1 FOR UPDATE'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mutation never reached held user lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, `UPDATE watch_current SET revision=revision+1 WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, show.Episodes[1].ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != http.StatusConflict {
		t.Fatal(code)
	}
	after, err := f.api.watch.NativeSeasonWatch(ctx, f.admin.ID, show.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range after.Items {
		if !ep.Played {
			t.Fatal(ep)
		}
	}
	var history int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, f.admin.ID).Scan(&history); err != nil || history != 0 {
		t.Fatal(history, err)
	}
	for _, bad := range []string{"-1", "10000", "no-season"} {
		requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/series/"+show.ID+"/seasons/"+bad+"/watch-state", pair.AccessToken, nil, nil), 400)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, "", map[string]any{"version": after.Version, "watched": false}, nil), 401)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/logout", pair.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": after.Version, "watched": false}, nil), 401)
}

func TestNativeSeasonWatchRollsBackEarlierEpisodesOnDeliveryFailure(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	before, err := f.api.watch.NativeSeasonWatch(ctx, f.admin.ID, show.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Fail after the first episode edit has already written its state/history.
	if _, err = f.pool.Exec(ctx, `CREATE FUNCTION fail_season_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF (SELECT cinemeta_id FROM catalog_media WHERE id=NEW.media_id) LIKE '%:1:2' THEN RAISE EXCEPTION 'controlled season failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_season_history BEFORE INSERT ON watch_history FOR EACH ROW EXECUTE FUNCTION fail_season_history()`); err != nil {
		t.Fatal(err)
	}
	path := "/api/client/v1/titles/series/" + show.ID + "/seasons/1/watch-state"
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": false}, nil), 503)
	after, err := f.api.watch.NativeSeasonWatch(ctx, f.admin.ID, show.ID, 1)
	if err != nil || after.Version != before.Version {
		t.Fatal(after, err)
	}
	var history int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, f.admin.ID).Scan(&history); err != nil || history != 0 {
		t.Fatal(history, err)
	}
	if _, err = f.pool.Exec(ctx, `DROP TRIGGER fail_season_history ON watch_history; DROP FUNCTION fail_season_history()`); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"version": before.Version, "watched": false}, nil), 200)
	current, err := f.api.watch.NativeState(ctx, f.admin.ID, catalog.MediaIdentity{Kind: "episode", ID: show.Episodes[0].ID})
	if err != nil || current.Played {
		t.Fatal(current, err)
	}
}
