package catalog

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"testing"
	"time"
)

func scheduleShow() cinemeta.Series {
	return cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1245698", Type: "series"}, Episodes: []cinemeta.Episode{
		{ID: "tt1245698:0:1", Season: 0, Number: 1, ReleaseDate: "2020-01-01"},
		{ID: "tt1245698:1:1", Season: 1, Number: 1, ReleaseDate: "2026-10-09"},
		{ID: "tt1245698:1:2", Season: 1, Number: 2, ReleaseDate: "2026-10-10"},
		{ID: "tt1245698:1:3", Season: 1, Number: 3},
	}}
}
func TestScheduleReleaseDayAndUnknownDates(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	show := scheduleShow()
	if err := s.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	for day, want := range map[string]int{"2026-10-08": 0, "2026-10-09": 1, "2026-10-10": 2} {
		counts, err := s.ReleasedCounts(ctx, []string{show.ID}, 0, day)
		if err != nil || counts[show.ID].Available != want || counts[show.ID].Watched != 0 {
			t.Fatal(day, counts, err)
		}
	}
	show.Episodes[3].ReleaseDate = "2026-10-10"
	if err := s.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	counts, err := s.ReleasedCounts(ctx, []string{show.ID}, 0, "2026-10-10")
	if err != nil || counts[show.ID].Available != 3 {
		t.Fatal(counts, err)
	}
	show.Episodes[1].ReleaseDate = "" // A successful correction back to unknown is authoritative.
	if err := s.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	counts, err = s.ReleasedCounts(ctx, []string{show.ID}, 0, "2026-10-10")
	if err != nil || counts[show.ID].Available != 2 {
		t.Fatal(counts, err)
	}
}
func TestScheduleStaleAndFailedRefreshPreserveCache(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	show := scheduleShow()
	newer := time.Now()
	if err := s.SaveSchedule(ctx, show, newer); err != nil {
		t.Fatal(err)
	}
	old := show
	old.Episodes = []cinemeta.Episode{}
	if err := s.SaveSchedule(ctx, old, newer.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	w := scheduleWorker{store: s, reader: metadataFake{err: errors.New("offline")}}
	if err := w.Work(ctx, &river.Job[scheduleArgs]{Args: scheduleArgs{ID: show.ID}}); err == nil {
		t.Fatal("failure discarded")
	}
	invalid := show
	invalid.Episodes = nil
	if err := s.SaveSchedule(ctx, invalid, newer.Add(time.Hour)); err == nil {
		t.Fatal("incomplete accepted")
	}
	counts, err := s.ReleasedCounts(ctx, []string{show.ID}, 0, "2026-10-10")
	if err != nil || counts[show.ID].Available != 2 {
		t.Fatal(counts, err)
	}
}
func TestScheduleDailyDispatch(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	var schema string
	if err := s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(s.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	show := scheduleShow()
	if err = s.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.DispatchSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='catalog_schedule'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	metadataExec(t, s, `UPDATE catalog_episode_schedules SET fetched_at=now()-interval '25 hours'`)
	for i := 0; i < 2; i++ {
		if err = s.DispatchSchedules(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='catalog_schedule'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
