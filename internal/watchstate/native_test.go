package watchstate

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
)

func nativeWatchFixture(t *testing.T) (*Store, *accounts.NativeAuth, accounts.NativeGrant) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	pw, err := accounts.NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	service := accounts.New(pool, pw)
	if _, err = service.Bootstrap(ctx, "viewer", ""); err != nil {
		t.Fatal(err)
	}
	auth, err := accounts.NewNativeAuth(pool, service, accounts.DefaultNativePolicy())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := auth.Login(ctx, "viewer", "", "127.0.0.1", "device-1", "Player")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := auth.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES($1,'tt1234567','{"name":"Example"}')`, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 VALUES($1,'fixture','T1',repeat('a',40),1,'/movie.mkv',1000,'{}',1)`, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	return New(pool), auth, grant
}

var nativeMovie = catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}

func nativeWatchSession(t *testing.T, s *Store, g accounts.NativeGrant) string {
	t.Helper()
	var id string
	err := s.pool.QueryRow(context.Background(), `INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,mapping_id,duration_ms,applied_revision)
 SELECT $1,$2,$3,m.movie_id,m.id,100000,COALESCE((SELECT revision FROM watch_current WHERE user_id=$1 AND media_id=m.movie_id),0)
 FROM media_mappings m WHERE m.movie_id=$4 RETURNING id::text`, g.User.ID, g.FamilyID, g.DeviceID, mediaID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func nativeWatchEvent(session string, id, seq int, kind string, position int64) NativeEvent {
	return NativeEvent{ID: fmt.Sprintf("99999999-9999-4999-8999-%012d", id), SessionID: session, Sequence: int64(seq), Media: nativeMovie, Kind: kind, PositionMS: position, DurationMS: 100000, ObservedAt: time.Now().UTC().Truncate(time.Millisecond), Origin: "live"}
}

func ingestNativeEvents(t *testing.T, s *Store, a *accounts.NativeAuth, g accounts.NativeGrant, events ...NativeEvent) []NativeEventResult {
	t.Helper()
	out, err := s.IngestNative(context.Background(), a, g, events)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNativeEarlyEOFIsAnInterruptionAndRetainsResume(t *testing.T) {
	for _, alreadyWatched := range []bool{false, true} {
		t.Run(fmt.Sprint("previously_watched=", alreadyWatched), func(t *testing.T) {
			s, a, g := nativeWatchFixture(t)
			ctx := context.Background()
			seedOutgoing(t, s, g.User.ID)
			at := time.Now().UTC().Add(time.Second)
			if alreadyWatched {
				if _, err := s.EditNative(ctx, a, g, NativeEdit{Media: nativeMovie, Action: "watched"}); err != nil {
					t.Fatal(err)
				}
			}
			outgoing := outgoingCount(t, s)
			session := nativeWatchSession(t, s, g)
			start := nativeWatchEvent(session, 1, 1, "start", 0)
			stop := nativeWatchEvent(session, 2, 2, "stop", 34000)
			start.ObservedAt, stop.ObservedAt = at, at
			stop.NaturalEOF = true
			stop.DurationMS = 1 // client cannot replace the mapped duration
			ingestNativeEvents(t, s, a, g, start, stop)
			ingestNativeEvents(t, s, a, g, start, stop)
			state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
			wantCount := 0
			if alreadyWatched {
				wantCount = 1
			}
			if err != nil || state.Played != alreadyWatched || state.PlayCount != wantCount || state.PositionMS != 34000 || outgoingCount(t, s) != outgoing {
				t.Fatal("early EOF changed completion or lost position", state, err)
			}
			page, err := s.PersonalRows(ctx, g.User.ID, "continue-watching", "", "2026-10-10")
			if err != nil || len(page.Items) != 1 || page.Items[0].State.PositionMS != 34000 {
				t.Fatal("interruption removed Continue Watching", page, err)
			}
			history, err := s.NativeHistory(ctx, g.User.ID, 0)
			if err != nil || history.Entries[0].Kind != "playback_stopped" {
				t.Fatal("interruption fabricated completion history", history, err)
			}
			resume := nativeWatchSession(t, s, g)
			finish := nativeWatchEvent(resume, 3, 1, "stop", 100000)
			finish.ObservedAt, finish.NaturalEOF = at, true
			ingestNativeEvents(t, s, a, g, finish)
			state, err = s.NativeState(ctx, g.User.ID, nativeMovie)
			if err != nil || !state.Played || state.PlayCount != wantCount+1 || state.PositionMS != 0 {
				t.Fatal("real completion failed", state, err)
			}
		})
	}
}

func TestNativeViewingResumeCompletionReplayAndStremio(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	seedOutgoing(t, s, g.User.ID)
	session := nativeWatchSession(t, s, g)
	start := nativeWatchEvent(session, 1, 1, "start", 0)
	seek := nativeWatchEvent(session, 2, 2, "progress", 85000)
	seek.ObservedAt = start.ObservedAt // ties and backwards seeking are legitimate
	seek.DurationMS = 1                // mapped duration remains authoritative
	back := nativeWatchEvent(session, 3, 3, "stop", 60000)
	back.ObservedAt = start.ObservedAt
	ingestNativeEvents(t, s, a, g, start, seek, back)
	state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.Played || state.PositionMS != 60000 || state.DurationMS == nil || *state.DurationMS != 100000 || state.PlayCount != 0 {
		t.Fatal(state, err)
	}
	if outgoingCount(t, s) != 0 {
		t.Fatal("progress queued Stremio flags")
	}
	second := nativeWatchSession(t, s, g)
	begin := nativeWatchEvent(second, 4, 1, "start", 60000)
	finish := nativeWatchEvent(second, 5, 2, "progress", 95000)
	stop := nativeWatchEvent(second, 6, 3, "stop", 100000)
	ingestNativeEvents(t, s, a, g, begin, finish, stop)
	for _, r := range ingestNativeEvents(t, s, a, g, begin, finish, stop) {
		if !r.Duplicate {
			t.Fatal("exact report replay not recognized")
		}
	}
	state, err = s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || !state.Played || state.PositionMS != 0 || state.PlayCount != 1 || outgoingCount(t, s) != 1 {
		t.Fatal(state, err)
	}
	history, err := s.NativeHistory(ctx, g.User.ID, 0)
	if err != nil || len(history.Entries) != 5 {
		t.Fatal("unexpected viewing history", len(history.Entries), err)
	}
	completed := 0
	for _, e := range history.Entries {
		if e.Kind == "playback_completed" {
			completed++
		}
		if e.DeviceID == nil || *e.DeviceID != "device-1" || e.Origin != "live" || e.Media != nativeMovie {
			t.Fatal("missing native provenance")
		}
	}
	if completed != 1 {
		t.Fatal("completion history duplicated")
	}
	tampered := finish
	tampered.PositionMS++
	if _, err = s.IngestNative(ctx, a, g, []NativeEvent{tampered}); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting event reuse accepted", err)
	}
	late := nativeWatchEvent(second, 7, 4, "stop", 100000)
	if _, err = s.IngestNative(ctx, a, g, []NativeEvent{late}); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal session fabricated another stop", err)
	}
}

func TestNativeDelayedOfflineHistoryDoesNotUndoManualOrOtherDeviceState(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	seedOutgoing(t, s, g.User.ID)
	session := nativeWatchSession(t, s, g)
	start := nativeWatchEvent(session, 1, 1, "start", 15000)
	finish := nativeWatchEvent(session, 2, 2, "stop", 100000)
	start.Origin, finish.Origin = "offline", "offline"
	start.ObservedAt = time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	finish.ObservedAt = start.ObservedAt.Add(time.Minute)
	manual, err := s.EditNative(ctx, a, g, NativeEdit{Media: nativeMovie, Revision: 0, Action: "unwatched"})
	if err != nil {
		t.Fatal(err)
	}
	results := ingestNativeEvents(t, s, a, g, start, finish)
	for _, r := range results {
		if r.Outcome != "stale" {
			t.Fatal("delayed offline state overwrote manual decision")
		}
	}
	state, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.Revision != manual.Revision || state.Played || state.PositionMS != 0 || state.LastPlayed != nil || outgoingCount(t, s) != 1 {
		t.Fatal(state, err)
	}
	history, err := s.NativeHistory(ctx, g.User.ID, 0)
	if err != nil || len(history.Entries) != 3 || history.Entries[0].Kind != "playback_completed" || history.Entries[0].Origin != "offline" || !history.Entries[0].ObservedAt.Equal(finish.ObservedAt) {
		t.Fatal("offline viewing evidence lost", history, err)
	}
	ingestNativeEvents(t, s, a, g, start, finish)
	if outgoingCount(t, s) != 1 {
		t.Fatal("stale viewing sent a watched flag")
	}
	oldSession := nativeWatchSession(t, s, g)
	otherSession := nativeWatchSession(t, s, g)
	newer := nativeWatchEvent(otherSession, 3, 1, "progress", 40000)
	// Control ordering relative to the database's microsecond manual timestamp;
	// client timestamps rounded to milliseconds can otherwise precede that edit.
	if err = s.pool.QueryRow(ctx, `SELECT observed_at FROM watch_current WHERE user_id=$1 AND media_id=(SELECT id FROM catalog_media WHERE kind=$2 AND cinemeta_id=$3)`, g.User.ID, nativeMovie.Kind, nativeMovie.ID).Scan(&newer.ObservedAt); err != nil {
		t.Fatal(err)
	}
	newer.ObservedAt = newer.ObservedAt.Add(time.Second)
	if ingestNativeEvents(t, s, a, g, newer)[0].Outcome != "applied" {
		t.Fatal("new session did not establish the revision barrier")
	}
	older := nativeWatchEvent(oldSession, 4, 1, "progress", 20000)
	// Even a later original timestamp cannot undo another session's revision.
	older.ObservedAt = newer.ObservedAt.Add(time.Second)
	if ingestNativeEvents(t, s, a, g, older)[0].Outcome != "stale" {
		t.Fatal("superseded session updated current state")
	}
	state, err = s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || state.PositionMS != 40000 {
		t.Fatal(state, err)
	}
}

func TestNativeManualUnmappedEditsWithoutProfilesAndRevisionConflicts(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	episode := catalog.MediaIdentity{Kind: "episode", ID: "tt7654321:0:1"}
	state, err := s.EditNative(ctx, a, g, NativeEdit{Media: episode, Action: "watched", DateMode: "undated"})
	if err != nil || !state.Played || state.LastPlayed != nil || state.Revision != 1 {
		t.Fatal(state, err)
	}
	if _, err = s.EditNative(ctx, a, g, NativeEdit{Media: episode, Action: "unwatched", Revision: 0}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	state, err = s.EditNative(ctx, a, g, NativeEdit{Media: episode, Action: "unwatched", Revision: 1})
	if err != nil || state.Played || state.PlayCount != 0 || state.Revision != 2 {
		t.Fatal(state, err)
	}
	var mappings int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM media_mappings`).Scan(&mappings); err != nil || mappings != 1 {
		t.Fatal("native edit changed library membership", mappings, err)
	}
	if err = a.Logout(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditNative(ctx, a, g, NativeEdit{Media: episode, Action: "watched", Revision: 2}); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal("revoked family wrote state", err)
	}
}

func TestNativeReportAtomicFailureOrderingAndUserDeviceIsolation(t *testing.T) {
	s, a, g := nativeWatchFixture(t)
	ctx := context.Background()
	session := nativeWatchSession(t, s, g)
	start := nativeWatchEvent(session, 1, 1, "start", 15000)
	gap := nativeWatchEvent(session, 2, 3, "progress", 50000)
	if _, err := s.IngestNative(ctx, a, g, []NativeEvent{start, gap}); !errors.Is(err, ErrConflict) {
		t.Fatal("out-of-order batch accepted", err)
	}
	var events, rows int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM native_watch_events),(SELECT count(*) FROM watch_current)`).Scan(&events, &rows); err != nil || events != 0 || rows != 0 {
		t.Fatal("partial batch committed", events, rows, err)
	}
	_, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_native_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
 CREATE CONSTRAINT TRIGGER fail_native_history AFTER INSERT ON watch_history DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_native_history()`)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.IngestNative(ctx, a, g, []NativeEvent{start}); err == nil || out != nil {
		t.Fatal("failed commit published reports")
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER fail_native_history ON watch_history`); err != nil {
		t.Fatal(err)
	}
	ingestNativeEvents(t, s, a, g, start)
	// A grant on another device cannot report against this session.
	pair, err := a.Login(ctx, "viewer", "", "127.0.0.1", "other-device", "Other player")
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestNative(ctx, a, other, []NativeEvent{start}); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatal("another device replayed this receipt", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM native_watch_events),(SELECT count(*) FROM watch_history)`).Scan(&events, &rows); err != nil || events != 1 || rows != 1 {
		t.Fatal("commit recovery duplicated records", events, rows, err)
	}
}
