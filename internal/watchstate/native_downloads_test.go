package watchstate

import (
	"context"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
)

func TestNativeDownloadWatchSeparatesCompletionFromRewatchProgress(t *testing.T) {
	s, auth, grant := nativeWatchFixture(t)
	ctx := context.Background()
	session := nativeWatchSession(t, s, grant)
	finish := nativeWatchEvent(session, 1, 1, "stop", 95000)
	ingestNativeEvents(t, s, auth, grant, finish)
	read := func() NativeDownloadWatch {
		t.Helper()
		values, err := s.NativeDownloadWatches(ctx, grant.User.ID, []catalog.MediaIdentity{nativeMovie})
		if err != nil || len(values) != 1 {
			t.Fatal(values, err)
		}
		return values[0]
	}
	value := read()
	if !value.State.Played || value.InProgress || value.CompletedAt == nil || !value.CompletedAt.Equal(finish.ObservedAt) {
		t.Fatal(value)
	}
	rewatch := nativeWatchEvent(nativeWatchSession(t, s, grant), 2, 1, "start", 20000)
	rewatch.ObservedAt = finish.ObservedAt.Add(time.Second)
	ingestNativeEvents(t, s, auth, grant, rewatch)
	value = read()
	if !value.State.Played || !value.InProgress || value.State.PositionMS != 20000 || !value.CompletedAt.Equal(finish.ObservedAt) || !value.ObservedAt.Equal(rewatch.ObservedAt) {
		t.Fatal("progress replaced completion evidence", value)
	}
	if _, err := s.EditNative(ctx, auth, grant, NativeEdit{Media: nativeMovie, Revision: value.State.Revision, Action: "unwatched"}); err != nil {
		t.Fatal(err)
	}
	if read().State.Played {
		t.Fatal("new unwatched decision was hidden")
	}
}

func TestNativeDownloadWatchRejectsStaleAndUnknownDateCompletionAndIsolatesUsers(t *testing.T) {
	s, auth, grant := nativeWatchFixture(t)
	ctx := context.Background()
	session := nativeWatchSession(t, s, grant)
	if _, err := s.EditNative(ctx, auth, grant, NativeEdit{Media: nativeMovie, Action: "watched"}); err != nil {
		t.Fatal(err)
	}
	initial, err := s.NativeDownloadWatches(ctx, grant.User.ID, []catalog.MediaIdentity{nativeMovie})
	if err != nil || initial[0].CompletedAt == nil {
		t.Fatal(initial, err)
	}
	late := nativeWatchEvent(session, 1, 1, "stop", 95000)
	late.ObservedAt = initial[0].CompletedAt.Add(time.Second)
	if ingestNativeEvents(t, s, auth, grant, late)[0].Outcome != "stale" {
		t.Fatal("fixture did not produce stale completion")
	}
	values, err := s.NativeDownloadWatches(ctx, grant.User.ID, []catalog.MediaIdentity{nativeMovie, {Kind: "movie", ID: "tt7654321"}})
	if err != nil || len(values) != 2 || !values[0].CompletedAt.Equal(*initial[0].CompletedAt) || values[1].State.Played {
		t.Fatal(values, err)
	}
	// Initial imported flags/activity have no trustworthy completion date.
	seedPersonal(t, s, "tt9999999:1:1", true, 0, time.Now())
	if _, err = s.pool.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at)
 SELECT user_id,media_id,'stremio_import',state,now() FROM watch_current WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id='tt9999999:1:1')`); err != nil {
		t.Fatal(err)
	}
	values, err = s.NativeDownloadWatches(ctx, 1, []catalog.MediaIdentity{{Kind: "episode", ID: "tt9999999:1:1"}})
	if err != nil || !values[0].State.Played || values[0].CompletedAt != nil {
		t.Fatal("import receipt became a completion date", values, err)
	}
	other, err := s.NativeDownloadWatches(ctx, 99, []catalog.MediaIdentity{nativeMovie})
	if err != nil || other[0].State.Played || other[0].CompletedAt != nil || other[0].ObservedAt != nil {
		t.Fatal("another user's activity leaked", other, err)
	}
	for _, input := range [][]catalog.MediaIdentity{nil, {nativeMovie, nativeMovie}, {{Kind: "series", ID: "tt1234567"}}, {{Kind: "movie", ID: "bad"}}, make([]catalog.MediaIdentity, 201)} {
		if _, err = s.NativeDownloadWatches(ctx, 1, input); err != ErrInvalid {
			t.Fatal("invalid batch accepted", err)
		}
	}
}

func TestNativeDownloadWatchUsesVerifiedStremioEpisodeRecency(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	episode := catalog.MediaIdentity{Kind: "episode", ID: "tt9999999:1:1"}
	seedPersonal(t, s, episode.ID, true, 0, time.Now())
	at := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('download-fixture','fixture@example.com','fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_episode_recency(user_id,media_id,stremio_account,watched_at)
 SELECT 1,id,'download-fixture',$1 FROM catalog_media WHERE cinemeta_id='tt9999999:1:1'`, at); err != nil {
		t.Fatal(err)
	}
	values, err := s.NativeDownloadWatches(ctx, 1, []catalog.MediaIdentity{episode})
	if err != nil || values[0].CompletedAt == nil || !values[0].CompletedAt.Equal(at) {
		t.Fatal(values, err)
	}
}
