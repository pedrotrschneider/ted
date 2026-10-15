package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func posterFixture(t *testing.T) (*fixture, func(string, ...any)) {
	t.Helper()
	f, _, _ := mappingSetup(t)
	f.api.series = seriesStub{}
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := f.pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	return f, exec
}
func posterRead(t *testing.T, f *fixture, cookie *http.Cookie, kind, id string) posterWatch {
	t.Helper()
	r := call(t, f.api.Handler(), "GET", "/api/title-statuses?kind="+kind+"&ids="+id, nil, cookie)
	requireStatus(t, r, 200)
	var result struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if e := json.Unmarshal(r.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Statuses) != 1 {
		t.Fatal(result)
	}
	return result.Statuses[0].Watch
}
func posterMedia(t *testing.T, f *fixture, exec func(string, ...any), id, kind string, published bool) string {
	t.Helper()
	var media, mapping string
	ctx := context.Background()
	if e := f.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,'{}') ON CONFLICT(cinemeta_id) DO UPDATE SET kind=EXCLUDED.kind RETURNING id::text`, id, kind).Scan(&media); e != nil {
		t.Fatal(e)
	}
	if e := f.pool.QueryRow(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,'1','t',repeat('a',40),1,'/file',100,'{}',$2) RETURNING id::text`, media, f.admin.ID).Scan(&mapping); e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO streaming_items(movie_id,mapping_id,phase,probe,published) VALUES($1,$2,'ready','{"format":{"duration":"600"}}',$3)`, media, mapping, published)
	return media
}
func TestPosterMovieProgressAndUserIsolation(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	media := posterMedia(t, f, exec, "tt1345836", "movie", true)
	if w := posterRead(t, f, cookie, "movie", "tt1345836"); w.Seen || w.Progress != nil {
		t.Fatal(w)
	}
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":1500000000,"play_count":1}','PlaybackProgress',now())`, f.admin.ID, media)
	w := posterRead(t, f, cookie, "movie", "tt1345836")
	if !w.Seen || w.Progress == nil || *w.Progress != 25 {
		t.Fatal(w)
	}
	if _, e := f.service.Create(context.Background(), identity(f.admin), "other", "Other", testPassword, "user"); e != nil {
		t.Fatal(e)
	}
	if w = posterRead(t, f, login(t, f.api.Handler(), "other"), "movie", "tt1345836"); w.Seen || w.Progress != nil {
		t.Fatal("another user's watch data leaked", w)
	}
	exec(`UPDATE watch_current SET state='{"played":true,"position_ticks":0,"play_count":1}' WHERE media_id=$1`, media)
	w = posterRead(t, f, cookie, "movie", "tt1345836")
	if !w.Seen || w.Progress != nil {
		t.Fatal("completed movie must not have a progress bar", w)
	}
	exec(`UPDATE watch_current SET state='{"played":false,"position_ticks":9000000000,"play_count":1}' WHERE media_id=$1`, media)
	w = posterRead(t, f, cookie, "movie", "tt1345836")
	if w.Progress == nil || *w.Progress != 100 {
		t.Fatal("progress not bounded", w)
	}
	exec(`UPDATE streaming_items SET probe='{}' WHERE movie_id=$1`, media)
	w = posterRead(t, f, cookie, "movie", "tt1345836")
	if !w.Seen || w.Progress != nil {
		t.Fatal("invented duration", w)
	}
}
func TestPosterShowCountsAllMainCatalogEpisodes(t *testing.T) {
	f, exec := posterFixture(t)
	f.api.series = seriesStub{count: 50}
	cookie := login(t, f.api.Handler(), "admin")
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"regular_episode_count":50}')`)
	for i, id := range []string{"tt1245698:1:1", "tt1245698:1:2", "tt1245698:1:3"} {
		media := posterMedia(t, f, exec, id, "episode", i < 2)
		state := `{"played":true,"position_ticks":0,"play_count":1}`
		if i == 1 {
			state = `{"played":false,"position_ticks":1000000000,"play_count":1}`
		}
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,$3,'Snapshot',now())`, f.admin.ID, media, []byte(state))
	}
	w := posterRead(t, f, cookie, "series", "tt1245698")
	if !w.Seen || w.Watched != 2 || w.Available != 50 || w.Progress != nil {
		t.Fatal(w)
	}
	exec(`UPDATE watch_current SET state='{"played":false,"position_ticks":1000000000,"play_count":1}' WHERE user_id=$1`, f.admin.ID)
	w = posterRead(t, f, cookie, "series", "tt1245698")
	if !w.Seen || w.Watched != 0 || w.Available != 50 {
		t.Fatal("partial viewing must show 0 / available", w)
	}
	exec(`UPDATE watch_current SET state='{"played":true,"position_ticks":0,"play_count":1}' WHERE user_id=$1`, f.admin.ID)
	w = posterRead(t, f, cookie, "series", "tt1245698")
	if w.Watched != 3 || w.Available != 50 {
		t.Fatal(w)
	}
}
func TestPosterRetainsEverViewedHistoryAfterResetAndRemoval(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	media := posterMedia(t, f, exec, "tt1345836", "movie", true)
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,'playback_started','{"played":false,"position_ticks":0,"play_count":1}',now())`, f.admin.ID, media)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":0,"play_count":0}','TogglePlayed',now())`, f.admin.ID, media)
	exec(`INSERT INTO library_removals(title_id,kind,phase) VALUES('tt1345836','movie','complete')`)
	w := posterRead(t, f, cookie, "movie", "tt1345836")
	if !w.Seen || w.Progress != nil || w.Available != 1 || w.Watched != 0 {
		t.Fatal(w)
	}
	// Removing a mapping cannot hide the account's independent watch status.
	exec(`UPDATE watch_current SET state='{"played":true,"position_ticks":0,"play_count":1}' WHERE user_id=$1 AND media_id=$2`, f.admin.ID, media)
	w = posterRead(t, f, cookie, "movie", "tt1345836")
	if !w.Seen || w.Watched != 1 || w.Available != 1 {
		t.Fatal("unmapped watched movie lost its completion indicator", w)
	}
}

func TestPosterSpecialsDoNotContributeToWatchedBanner(t *testing.T) {
	f, exec := posterFixture(t)
	f.api.series = specialsCountCatalog{}
	cookie := login(t, f.api.Handler(), "admin")
	special := posterMedia(t, f, exec, "tt1245698:0:1", "episode", true)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"position_ticks":0,"play_count":1}','TEDLocal',now())`, f.admin.ID, special)
	if w := posterRead(t, f, cookie, "series", "tt1245698"); w.Seen || w.Watched != 0 {
		t.Fatal("special changed banner", w)
	}
	if ids := libraryIDs(t, f, cookie, "kind=series&watched=true"); len(ids) != 0 {
		t.Fatal("special appeared in Watched filter", ids)
	}
	regular := posterMedia(t, f, exec, "tt1245698:1:1", "episode", false)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"position_ticks":0,"play_count":1}','TEDLocal',now())`, f.admin.ID, regular)
	if w := posterRead(t, f, cookie, "series", "tt1245698"); !w.Seen || w.Watched != 1 || w.Available != 2 {
		t.Fatal("unpublished regular episode missing", w)
	}
}

func TestForgetTitleRemovesPosterEyeWhileUnwatchRetainsIt(t *testing.T) {
	f, exec := posterFixture(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	media := posterMedia(t, f, exec, "tt1345836", "movie", false)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"position_ticks":20,"play_count":1,"last_played":null}','StremioImport',now());`, f.admin.ID, media)
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT user_id,media_id,'stremio_import',state,now() FROM watch_current WHERE media_id=$1`, media)
	set := func(played, forget bool) {
		t.Helper()
		r := call(t, h, "GET", "/api/watch-titles/tt1345836", nil, cookie)
		requireStatus(t, r, 200)
		var v struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1345836", map[string]any{"version": v.Version, "played": played, "forget_history": forget}, cookie), 202)
	}
	set(false, false)
	if !posterRead(t, f, cookie, "movie", "tt1345836").Seen {
		t.Fatal("ordinary unwatch erased history")
	}
	set(false, true)
	if w := posterRead(t, f, cookie, "movie", "tt1345836"); w.Seen || w.Progress != nil {
		t.Fatal("forgotten title still viewed", w)
	}
	set(true, false)
	if !posterRead(t, f, cookie, "movie", "tt1345836").Seen {
		t.Fatal("new viewing did not restore eye")
	}
}
