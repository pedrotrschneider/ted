package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"testing"
	"time"
)

func TestOfflineRegistrationReplayAndNewLogin(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	session := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	start := nativeWatchEvent(session, 501, 1, "start", 1000)
	start.Origin = "offline"
	stop := nativeWatchEvent(session, 502, 2, "stop", 25000)
	stop.Origin = "offline"
	stop.ObservedAt = start.ObservedAt.Add(time.Millisecond)
	registration := NativeOfflineSession{ID: session, Media: nativeMovie, StartedAt: start.ObservedAt}
	events := []NativeEvent{start, stop}
	out, err := s.IngestNativeOffline(ctx, a, g, events, []NativeOfflineSession{registration})
	if err != nil {
		t.Fatal(err)
	}
	if out[1].Outcome != "applied" {
		t.Fatal(out)
	}
	// Re-authentication creates a different family without changing device-owned replay.
	pair, err := a.Login(ctx, "viewer", "", "127.0.0.1", g.DeviceID, "Player")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := a.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.IngestNativeOffline(ctx, a, fresh, events, []NativeOfflineSession{registration})
	if err != nil || !out[0].Duplicate || !out[1].Duplicate {
		t.Fatalf("%v %v", out, err)
	}
	state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.PositionMS != 25000 || state.Played {
		t.Fatalf("%+v %v", state, err)
	}
	registration.WatchRevision++
	if _, err = s.IngestNativeOffline(ctx, a, fresh, events, []NativeOfflineSession{registration}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	other := fresh
	other.DeviceID = "other"
	if _, err = s.IngestNativeOffline(ctx, a, other, events, []NativeOfflineSession{registration}); err == nil {
		t.Fatal("another device adopted a session")
	}
}

func TestOfflineConsecutiveSessionsAndNewerEdit(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "consecutive", true: "newer-edit"}[edit], func(t *testing.T) {
			s, a, g := nativeWatchFixture(t)
			ctx := context.Background()
			first := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			second := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			e := nativeWatchEvent(first, 601, 1, "start", 1000)
			e.Origin = "offline"
			e.ObservedAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
			stop := e
			stop.ID = nativeWatchEvent(first, 602, 2, "stop", 1000).ID
			stop.Sequence = 2
			stop.Kind = "stop"
			stop.ObservedAt = e.ObservedAt.Add(time.Second)
			reg := NativeOfflineSession{ID: first, Media: nativeMovie, StartedAt: e.ObservedAt}
			if _, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{e, stop}, []NativeOfflineSession{reg}); err != nil {
				t.Fatal(err)
			}
			if edit {
				state, _ := s.NativeState(ctx, g.User.ID, nativeMovie)
				if _, err := s.EditNative(ctx, a, g, NativeEdit{Media: nativeMovie, Revision: state.Revision, Action: "unwatched"}); err != nil {
					t.Fatal(err)
				}
			}
			e.ID = nativeWatchEvent(second, 603, 1, "start", 1000).ID
			e.SessionID = second
			e.ObservedAt = stop.ObservedAt.Add(time.Second)
			stop = e
			stop.ID = nativeWatchEvent(second, 604, 2, "stop", 50000).ID
			stop.Sequence = 2
			stop.Kind = "stop"
			stop.PositionMS = 50000
			stop.ObservedAt = e.ObservedAt.Add(time.Second)
			reg = NativeOfflineSession{ID: second, Media: nativeMovie, StartedAt: e.ObservedAt, PreviousSession: first}
			out, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{e, stop}, []NativeOfflineSession{reg})
			if err != nil {
				t.Fatal(err)
			}
			expected := "applied"
			if edit {
				expected = "stale"
			}
			if out[1].Outcome != expected {
				t.Fatal(out)
			}
		})
	}
}

func TestOfflineInvalidFirstEventRollsBackRegistration(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	e := nativeWatchEvent(id, 701, 2, "progress", 1000)
	e.Origin = "offline"
	reg := NativeOfflineSession{ID: id, Media: nativeMovie, StartedAt: e.ObservedAt}
	if _, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{e}, []NativeOfflineSession{reg}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM native_playback_sessions WHERE id=$1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	// A valid grant is still required even for disconnected historical sessions.
	if err := a.Logout(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestNativeOffline(ctx, a, g, []NativeEvent{e}, []NativeOfflineSession{reg}); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
}
