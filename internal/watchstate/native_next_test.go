package watchstate

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"errors"
	"testing"
	"time"
)

func TestExplicitNextCompletesEarlyOfflineEpisodeAndReplaysExactlyOnce(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	media := catalog.MediaIdentity{Kind: "episode", ID: "tt1234567:1:2"}
	session := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	start := nativeWatchEvent(session, 801, 1, "start", 7000)
	start.Media, start.Origin = media, "offline"
	start.ObservedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	end := start
	end.ID, end.Sequence, end.Kind = nativeWatchEvent(session, 802, 2, "stop", 7000).ID, 2, "complete"
	end.ObservedAt = start.ObservedAt.Add(time.Second)
	registration := NativeOfflineSession{ID: session, Media: media, StartedAt: start.ObservedAt}
	events := []NativeEvent{start, end}
	for attempt := 0; attempt < 2; attempt++ {
		out, err := s.IngestNativeOffline(ctx, a, g, events, []NativeOfflineSession{registration})
		if err != nil || out[1].Outcome != "applied" || out[1].Duplicate != (attempt == 1) {
			t.Fatalf("%+v %v", out, err)
		}
	}
	state, err := s.NativeState(ctx, g.User.ID, media)
	if err != nil || !state.Played || state.PositionMS != 0 {
		t.Fatalf("%+v %v", state, err)
	}
	var raw []byte
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT envelope,(SELECT count(*) FROM watch_history WHERE native_event_id=$1) FROM native_watch_events WHERE id=$1`, end.ID).Scan(&raw, &count); err != nil {
		t.Fatal(err)
	}
	var retained NativeEvent
	if err = json.Unmarshal(raw, &retained); err != nil || retained.PositionMS != 7000 || retained.NaturalEOF || count != 1 {
		t.Fatalf("%+v count=%d %v", retained, count, err)
	}
	later := end
	later.ID, later.Sequence, later.Kind = nativeWatchEvent(session, 803, 3, "stop", 8000).ID, 3, "stop"
	if _, err = s.IngestNative(ctx, a, g, []NativeEvent{later}); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal completion accepted more reports", err)
	}
}

func TestExplicitNextCannotRevertNewerDecisionOrCompleteMovie(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	media := catalog.MediaIdentity{Kind: "episode", ID: "tt1234567:1:2"}
	session := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	start := nativeWatchEvent(session, 811, 1, "start", 7000)
	start.Media, start.Origin = media, "offline"
	start.ObservedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	reg := NativeOfflineSession{ID: session, Media: media, StartedAt: start.ObservedAt}
	if _, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{start}, []NativeOfflineSession{reg}); err != nil {
		t.Fatal(err)
	}
	state, _ := s.NativeState(ctx, g.User.ID, media)
	if _, err := s.EditNative(ctx, a, g, NativeEdit{Media: media, Revision: state.Revision, Action: "unwatched"}); err != nil {
		t.Fatal(err)
	}
	end := start
	end.ID, end.Sequence, end.Kind = nativeWatchEvent(session, 812, 2, "stop", 7000).ID, 2, "complete"
	end.ObservedAt = start.ObservedAt.Add(time.Second)
	out, err := s.IngestNative(ctx, a, g, []NativeEvent{end})
	if err != nil || out[0].Outcome != "stale" {
		t.Fatalf("%+v %v", out, err)
	}
	state, err = s.NativeState(ctx, g.User.ID, media)
	if err != nil || state.Played {
		t.Fatalf("%+v %v", state, err)
	}
	end.Media = nativeMovie
	if _, err = s.IngestNative(ctx, a, g, []NativeEvent{end}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
