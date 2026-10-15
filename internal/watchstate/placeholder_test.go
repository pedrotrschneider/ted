package watchstate

import (
	"context"
	"testing"
	"time"
)

func TestPlaceholderReportsPreserveCurrentStateHistoryAndReceipts(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	good := nativeWatchSession(t, s, g)
	ingestNativeEvents(t, s, a, g, nativeWatchEvent(good, 1, 1, "start", 10000), nativeWatchEvent(good, 2, 2, "stop", 20000))
	before, e := s.NativeState(ctx, g.User.ID, nativeMovie)
	if e != nil {
		t.Fatal(e)
	}
	bad := nativeWatchSession(t, s, g)
	if _, e = s.pool.Exec(ctx, `UPDATE native_playback_sessions SET expected_duration_ms=100000,duration_ms=0 WHERE id=$1`, bad); e != nil {
		t.Fatal(e)
	}
	first := nativeWatchEvent(bad, 3, 1, "start", 1000)
	first.DurationMS = 30000
	end := nativeWatchEvent(bad, 4, 2, "stop", 30000)
	end.NaturalEOF = true
	end.DurationMS = 30000
	end.ObservedAt = first.ObservedAt.Add(time.Second)
	out := ingestNativeEvents(t, s, a, g, first, end)
	if out[0].Outcome != "ignored" || out[1].Outcome != "ignored" {
		t.Fatal("short status video became viewing", out)
	}
	again := ingestNativeEvents(t, s, a, g, first)
	if !again[0].Duplicate || again[0].Outcome != "ignored" {
		t.Fatal("ignored report cannot be replayed", again)
	}
	after, e := s.NativeState(ctx, g.User.ID, nativeMovie)
	if e != nil || after.Revision != before.Revision || after.PositionMS != before.PositionMS || after.PlayCount != before.PlayCount || after.Played != before.Played {
		t.Fatal("placeholder replaced legitimate state", after, e)
	}
	var count int
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE native_event_id=ANY($1::uuid[])`, []string{first.ID, end.ID}).Scan(&count); e != nil || count != 0 {
		t.Fatal("placeholder created viewing history", count, e)
	}
}
func TestRuntimeThresholdUnknownAndRegularMedia(t *testing.T) {
	if !ShortRuntime(30000, 100000) || ShortRuntime(30001, 100000) || ShortRuntime(0, 100000) || ShortRuntime(30000, 0) {
		t.Fatal("inclusive 30 percent or unknown-duration policy changed")
	}
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	id := nativeWatchSession(t, s, g)
	if _, e := s.pool.Exec(ctx, `UPDATE native_playback_sessions SET expected_duration_ms=100000,duration_ms=0 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	start := nativeWatchEvent(id, 1, 1, "start", 5000)
	start.DurationMS = 30001
	out := ingestNativeEvents(t, s, a, g, start)
	if out[0].Outcome != "applied" {
		t.Fatal("real media above threshold was blocked", out)
	}
}
