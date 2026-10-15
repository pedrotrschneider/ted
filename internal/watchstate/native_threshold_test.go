package watchstate

import (
	"context"
	"testing"
	"time"
)

func TestNativeMinimumViewingThresholdAndBackwardSeek(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	session := nativeWatchSession(t, s, g)
	brief := []NativeEvent{nativeWatchEvent(session, 701, 1, "start", 0), nativeWatchEvent(session, 702, 2, "progress", 14999), nativeWatchEvent(session, 703, 3, "stop", 14999)}
	for _, result := range ingestNativeEvents(t, s, a, g, brief...) {
		if result.Outcome != "ignored" {
			t.Fatal(result)
		}
	}
	duplicate := ingestNativeEvents(t, s, a, g, brief[2])[0]
	if !duplicate.Duplicate || duplicate.Outcome != "ignored" {
		t.Fatal(duplicate)
	}
	var history int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, g.User.ID).Scan(&history); err != nil || history != 0 {
		t.Fatal(history, err)
	}
	state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.PositionMS != 0 || state.Revision != 0 {
		t.Fatal(state, err)
	}
	rows, err := s.PersonalRows(ctx, g.User.ID, "continue-watching", "", "2026-10-15")
	if err != nil || len(rows.Items) != 0 {
		t.Fatal(rows, err)
	}
	session = nativeWatchSession(t, s, g)
	ingestNativeEvents(t, s, a, g, nativeWatchEvent(session, 704, 1, "start", 0))
	result := ingestNativeEvents(t, s, a, g, nativeWatchEvent(session, 705, 2, "progress", 15000))[0]
	if result.Outcome != "applied" {
		t.Fatal(result)
	}
	ingestNativeEvents(t, s, a, g, nativeWatchEvent(session, 706, 3, "progress", 5000), nativeWatchEvent(session, 707, 4, "stop", 5000))
	state, err = s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.PositionMS != 5000 || state.Played {
		t.Fatal(state, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, g.User.ID).Scan(&history); err != nil || history != 2 {
		t.Fatal(history, err)
	}
}

func TestOfflineMinimumViewingPreservesPreviousStateAndExplicitDecision(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	original, err := s.EditNative(ctx, a, g, NativeEdit{Media: nativeMovie, Action: "watched"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	session := "77777777-7777-4777-8777-777777777777"
	start := nativeWatchEvent(session, 710, 1, "start", 0)
	start.Origin = "offline"
	start.ObservedAt = at
	stop := nativeWatchEvent(session, 711, 2, "stop", 14000)
	stop.Origin = "offline"
	stop.ObservedAt = at.Add(time.Second)
	results, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{start, stop}, []NativeOfflineSession{{ID: session, Media: nativeMovie, WatchRevision: original.Revision, StartedAt: at}})
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	for _, r := range results {
		if r.Outcome != "ignored" {
			t.Fatal(r)
		}
	}
	state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || !state.Played || state.Revision != original.Revision || state.PositionMS != 0 {
		t.Fatal(state, err)
	}
	var history int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, g.User.ID).Scan(&history); err != nil || history != 1 {
		t.Fatal(history, err)
	}
}
