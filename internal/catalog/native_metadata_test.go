package catalog

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"testing"
	"time"
)

func TestNativeMetadataRetainedSeparatelyAndFenced(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	at := time.Now()
	title := cinemeta.Movie{ID: "tt1234567", Type: "movie", Name: "New", Background: "https://images.metahub.space/new.jpg", Runtime: "90 min"}
	if err := s.SaveNativeTitle(ctx, title, at); err != nil {
		t.Fatal(err)
	}
	old := title
	old.Name = "Old"
	if err := s.SaveNativeTitle(ctx, old, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.NativeTitle(ctx, title.Type, title.ID)
	if err != nil || got.Name != "New" || got.Runtime != "90 min" {
		t.Fatal(got, err)
	}
	var n int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM catalog_media)+(SELECT count(*) FROM media_mappings)+(SELECT count(*) FROM watch_current)+(SELECT count(*) FROM watch_history)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("discovery created membership/history", n, err)
	}
}

func TestNativeSeriesCacheAndScheduleShareRecovery(t *testing.T) {
	s := metadataStore(t)
	ctx := context.Background()
	at := time.Now()
	show := scheduleShow()
	show.Name = "New"
	if err := s.SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	old := show
	old.Name = "Old"
	old.Episodes = []cinemeta.Episode{}
	if err := s.SaveSchedule(ctx, old, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	malformed := show
	malformed.Name = "Broken"
	malformed.Episodes = nil
	if err := s.SaveSchedule(ctx, malformed, at.Add(time.Hour)); err == nil {
		t.Fatal("partial schedule accepted")
	}
	retained, err := New(s.pool).NativeSeries(ctx, show.ID)
	if err != nil || retained.Name != "New" || len(retained.Episodes) != 4 {
		t.Fatal("stale/partial/restart discarded cache", retained, err)
	}
	// Fail at the actual transaction commit after both cache updates executed.
	metadataExec(t, s, `CREATE FUNCTION reject_native_schedule() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled schedule commit rejection'; END $$;
 CREATE CONSTRAINT TRIGGER reject_native_schedule AFTER UPDATE ON catalog_episode_schedules DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_native_schedule()`)
	replacement := show
	replacement.Name = "Uncommitted"
	replacement.Episodes = []cinemeta.Episode{}
	if err = s.SaveSchedule(ctx, replacement, at.Add(time.Hour)); err == nil {
		t.Fatal("commit rejection ignored")
	}
	retained, err = s.NativeSeries(ctx, show.ID)
	if err != nil || retained.Name != "New" || len(retained.Episodes) != 4 {
		t.Fatal("failed commit split metadata and schedule", retained, err)
	}
}
