package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"testing"
	"time"
)

func TestSchedulePosterExcludesUnreleasedWithoutChangingWatchState(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	show, _ := seriesStub{}.Series(context.Background(), "tt1245698")
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: show.ID + ":1:3", Season: 1, Number: 3}, cinemeta.Episode{ID: show.ID + ":1:4", Season: 1, Number: 4, ReleaseDate: "9999-01-01"})
	for _, e := range show.Episodes {
		media := posterMedia(t, f, exec, e.ID, "episode", true)
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"play_count":1}','TEDLocal',now())`, f.admin.ID, media)
	}
	if err := f.api.rememberWatchSeries(context.Background(), identity(f.admin), show); err != nil {
		t.Fatal(err)
	}
	r := call(t, f.api.Handler(), "GET", "/api/title-statuses?kind=series&ids="+show.ID, nil, cookie)
	requireStatus(t, r, 200)
	var statuses struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	green := statuses.Statuses[0]
	if green.Added != 2 || green.Total == nil || *green.Total != 2 {
		t.Fatal("green includes future/unknown episodes", green)
	}
	w := posterRead(t, f, cookie, "series", show.ID)
	if !w.Seen || w.Watched != 2 || w.Available != 2 {
		t.Fatal(w)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM watch_current WHERE (state->>'played')::bool`).Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	show.Episodes[2].ReleaseDate = time.Now().In(f.api.CalendarLocation).Format(time.DateOnly)
	if err := f.api.catalog.SaveSchedule(context.Background(), show, time.Now()); err != nil {
		t.Fatal(err)
	}
	w = posterRead(t, f, cookie, "series", show.ID)
	if w.Watched != 3 || w.Available != 3 {
		t.Fatal(w)
	}
}
