package watchstate

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
)

func seedPersonal(t *testing.T, s *Store, id string, played bool, position int64, at time.Time) {
	t.Helper()
	kind := "movie"
	if cinemeta.ValidVideoID(id) {
		kind = "episode"
	}
	_, err := s.pool.Exec(context.Background(), `WITH c AS (INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,'{}') ON CONFLICT(cinemeta_id) DO UPDATE SET cinemeta_id=EXCLUDED.cinemeta_id RETURNING id)
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at,updated_at,duration_ms) SELECT 1,id,jsonb_build_object('played',$3::boolean,'position_ticks',$4::bigint,'play_count',CASE WHEN $3::boolean THEN 1 ELSE 0 END,'last_played',null),'TEDNativePlayback',$5,$5,100000 FROM c
 ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state,observed_at=EXCLUDED.observed_at,updated_at=EXCLUDED.updated_at`, id, kind, played, position*10000, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativePersonalRowsSpecialsGapsSeasonsReleaseDaysAndCompletedShows(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	now := time.Now()
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt9999999", Type: "series", Name: "Show"}, Episodes: []cinemeta.Episode{
		{ID: "tt9999999:0:1", Season: 0, Number: 1, Name: "Special", ReleaseDate: "2020-01-01"},
		{ID: "tt9999999:1:1", Season: 1, Number: 1, ReleaseDate: "2020-01-01"},
		{ID: "tt9999999:1:3", Season: 1, Number: 3, ReleaseDate: "2026-10-10"},
		{ID: "tt9999999:1:4", Season: 1, Number: 4, ReleaseDate: "2026-10-10"},
		{ID: "tt9999999:1:5", Season: 1, Number: 5},
		{ID: "tt9999999:2:1", Season: 2, Number: 1, ReleaseDate: "2026-10-11"},
	}}
	cache := catalog.New(s.pool)
	if err := cache.SaveSchedule(ctx, show, now); err != nil {
		t.Fatal(err)
	}
	empty, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(empty.Items) != 0 {
		t.Fatal("discovery became personal activity", empty, err)
	}
	seedPersonal(t, s, "tt9999999:1:1", true, 0, now)
	seedPersonal(t, s, "tt1234567", false, 6000, now)
	before, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-09")
	if err != nil || len(before.Items) != 0 {
		t.Fatal("future, unknown date or special became Next Up", before, err)
	}
	next, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(next.Items) != 1 || next.Items[0].Media.ID != "tt9999999:1:3" {
		t.Fatal("next released catalog episode across missing numbers was not selected", next, err)
	}
	seedPersonal(t, s, "tt9999999:1:4", true, 0, now)
	next, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(next.Items) != 0 {
		t.Fatal("an earlier gap or unknown/future episode became Next Up", next, err)
	}
	seedPersonal(t, s, "tt9999999:1:3", false, 8000, now)
	next, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(next.Items) != 0 {
		t.Fatal("unfinished gap duplicated Continue Watching in Next Up", next, err)
	}
	seedPersonal(t, s, "tt9999999:0:1", false, 3000, now)
	cw, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-11")
	if err != nil || len(cw.Items) != 2 {
		t.Fatal("unmapped movie/episode/special progress lost", cw, err)
	}
	if _, err = s.pool.Exec(ctx, `DELETE FROM media_mappings WHERE movie_id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-11")
	if err != nil || len(removed.Items) != 2 {
		t.Fatal("source removal discarded personal progress", removed, err)
	}
	seedPersonal(t, s, "tt9999999:1:3", true, 0, now)
	seedPersonal(t, s, "tt9999999:0:1", true, 0, now)
	next, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(next.Items) != 0 {
		t.Fatal("completed show still eligible", next, err)
	}
	next, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(next.Items) != 1 || next.Items[0].Media.ID != "tt9999999:2:1" {
		t.Fatal("new season release missing", next, err)
	}
	other, err := s.PersonalRows(ctx, 99, "continue-watching", "", "2026-10-11")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("personal state leaked", other, err)
	}
	// A new store against the same persistence must retain rows without Cinemeta or mappings.
	restart, err := New(s.pool).PersonalRows(ctx, 1, "continue-watching", "", "2026-10-11")
	if err != nil || len(restart.Items) != 1 {
		t.Fatal("restart/outage lost retained progress", restart, err)
	}
}

func TestNativeNextUpFollowsLatestWatchedWithNumericTimestampTies(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt9253284", Type: "series", Name: "Andor"}}
	for season := 1; season <= 2; season++ {
		for number := 1; number <= 12; number++ {
			show.Episodes = append(show.Episodes, cinemeta.Episode{ID: fmt.Sprintf("%s:%d:%d", show.ID, season, number), Season: season, Number: number, ReleaseDate: "2020-01-01"})
		}
	}
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: show.ID + ":0:99", Season: 0, Number: 99, ReleaseDate: "2020-01-01"})
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	// Imported flags share one observation date; it is not a viewing order.
	for _, number := range []int{2, 7, 8, 9} {
		seedPersonal(t, s, fmt.Sprintf("%s:2:%d", show.ID, number), true, 0, at)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE watch_current SET source='StremioImport' WHERE media_id IN(SELECT id FROM catalog_media WHERE cinemeta_id LIKE 'tt9253284:%')`); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		page, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
		if err != nil || len(page.Items) != 1 || page.Items[0].Media.ID != want {
			t.Fatal("Next Up did not follow latest watched episode", page, err)
		}
	}
	check(show.ID + ":2:10")
	// A later viewing of an earlier episode deliberately moves the anchor back.
	seedPersonal(t, s, show.ID+":1:1", true, 0, at.Add(time.Minute))
	seedPersonal(t, s, show.ID+":0:99", true, 0, at.Add(2*time.Minute))
	check(show.ID + ":1:2")
	seedPersonal(t, s, show.ID+":2:10", true, 0, at)
	check(show.ID + ":1:2")
	// Removing the latest watched decision selects the remaining latest flags.
	seedPersonal(t, s, show.ID+":1:1", false, 0, at)
	check(show.ID + ":2:11")
	seedPersonal(t, s, show.ID+":2:10", false, 0, at)
	seedPersonal(t, s, show.ID+":2:9", false, 0, at)
	check(show.ID + ":2:9")
	var untouched bool
	if err := s.pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=1 AND c.cinemeta_id IN('tt9253284:1:2','tt9253284:2:1','tt9253284:2:3')) AND NOT EXISTS(SELECT 1 FROM watch_history)`).Scan(&untouched); err != nil || !untouched {
		t.Fatal("row reads changed earlier gaps or history", untouched, err)
	}
	other, err := s.PersonalRows(ctx, 99, "next-up", "", "2026-10-11")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("another user's watched anchor leaked", other, err)
	}
}

func TestNativeNextUpPaginationAndConfiguredCalendarBoundary(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	cache := catalog.New(s.pool)
	for n := 1; n <= 51; n++ {
		id := fmt.Sprintf("tt%07d", n)
		show := cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: id}, Episodes: []cinemeta.Episode{{ID: id + ":1:1", Season: 1, Number: 1, ReleaseDate: "2026-10-10"}, {ID: id + ":1:2", Season: 1, Number: 2, ReleaseDate: "2026-10-11"}}}
		if err := cache.SaveSchedule(ctx, show, at); err != nil {
			t.Fatal(err)
		}
		seedPersonal(t, s, id+":1:1", true, 0, at)
	}
	// The same instant is a different configured calendar day; UTC must not
	// release tomorrow's episode early for the server in America/Sao_Paulo.
	instant := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	prior, err := s.PersonalRows(ctx, 1, "next-up", "", instant.In(loc).Format(time.DateOnly))
	if err != nil || len(prior.Items) != 0 {
		t.Fatal(prior, err)
	}
	first, err := s.PersonalRows(ctx, 1, "next-up", "", instant.Format(time.DateOnly))
	if err != nil || len(first.Items) != 50 || first.Next == "" {
		t.Fatal(first, err)
	}
	second, err := s.PersonalRows(ctx, 1, "next-up", first.Next, "2026-10-11")
	if err != nil || len(second.Items) != 1 || second.Next != "" {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.Media.ID] {
			t.Fatal("Next Up cursor tie duplicated a show")
		}
		seen[item.Media.ID] = true
	}
}

func TestNativeNextUpUnwatchedSuccessorUsesAppliedCompletionOnly(t *testing.T) {
	s, auth, g := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt8888888", Type: "series", Name: "Rewatch"}}
	for n := 1; n <= 3; n++ {
		id := fmt.Sprintf("%s:1:%d", show.ID, n)
		show.Episodes = append(show.Episodes, cinemeta.Episode{ID: id, Season: 1, Number: n, ReleaseDate: "2000-01-01"})
		seedPersonal(t, s, id, true, 0, at.Add(-time.Hour))
	}
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		up, err := s.PersonalRows(ctx, g.User.ID, "next-up", "", "2026-10-11")
		if err != nil || (want == "" && len(up.Items) != 0) || (want != "" && (len(up.Items) != 1 || up.Items[0].Media.ID != want || up.Items[0].State.Played)) {
			t.Fatal("rewatch successor", up, err)
		}
	}
	check("") // Final episode of the initial fully watched state.
	// A rewatch must not recommend another completed episode.
	seedPersonal(t, s, show.Episodes[0].ID, true, 0, at)
	check("")
	seedPersonal(t, s, show.Episodes[1].ID, false, 0, at.Add(-time.Hour))
	first := show.Episodes[0].ID
	session := personalEpisodeSession(t, s, g.FamilyID, g.User.ID, g.DeviceID, first)
	start := nativeWatchEvent(session, 1, 1, "start", 15000)
	start.Media, start.ObservedAt = catalog.MediaIdentity{Kind: "episode", ID: first}, at
	ingestNativeEvents(t, s, auth, g, start)
	check("")
	finish := nativeWatchEvent(session, 2, 2, "stop", 90000)
	finish.Media, finish.ObservedAt = start.Media, at.Add(time.Second)
	ingestNativeEvents(t, s, auth, g, finish)
	check(show.Episodes[1].ID)
	// Resetting an unfinished later rewatch must not make it a completion.
	third := show.Episodes[2].ID
	later := personalEpisodeSession(t, s, g.FamilyID, g.User.ID, g.DeviceID, third)
	begin := nativeWatchEvent(later, 3, 1, "start", 15000)
	begin.Media, begin.ObservedAt = catalog.MediaIdentity{Kind: "episode", ID: third}, at.Add(2*time.Second)
	ingestNativeEvents(t, s, auth, g, begin)
	check("")
	current, err := s.NativeState(ctx, g.User.ID, begin.Media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditNative(ctx, auth, g, NativeEdit{Media: begin.Media, Revision: current.Revision, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	check(show.Episodes[1].ID)
	// Retain late completion in history, without reverting the newer reset.
	late := nativeWatchEvent(later, 4, 2, "stop", 90000)
	late.Media, late.ObservedAt = begin.Media, at.Add(3*time.Second)
	result := ingestNativeEvents(t, s, auth, g, late)
	if result[0].Outcome != "stale" {
		t.Fatal(result)
	}
	check(show.Episodes[1].ID)
	ingestNativeEvents(t, s, auth, g, start, finish, late)
	check(show.Episodes[1].ID)
}

func personalEpisodeSession(t *testing.T, s *Store, family string, user int64, device, episode string) string {
	t.Helper()
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 SELECT id,'fixture','T2',repeat('b',40),1,'/episode.mkv',1000,'{}',$2 FROM catalog_media WHERE cinemeta_id=$1`, episode, user)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = s.pool.QueryRow(ctx, `INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,mapping_id,duration_ms,applied_revision)
 SELECT $1,$2,$3,m.movie_id,m.id,100000,w.revision FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id
 JOIN watch_current w ON w.media_id=c.id AND w.user_id=$1 WHERE c.cinemeta_id=$4 RETURNING id::text`, user, family, device, episode).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNativeNextUpWaitsForCurrentEpisodeCompletion(t *testing.T) {
	s, auth, g := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt8888888", Type: "series", Name: "Show"}}
	for n := 1; n <= 3; n++ {
		show.Episodes = append(show.Episodes, cinemeta.Episode{ID: fmt.Sprintf("%s:1:%d", show.ID, n), Season: 1, Number: n, ReleaseDate: "2000-01-01"})
	}
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	first, current, last := show.Episodes[0].ID, show.Episodes[1].ID, show.Episodes[2].ID
	seedPersonal(t, s, first, true, 0, at)
	seedPersonal(t, s, current, false, 0, at)
	check := func(cw int, next string) {
		t.Helper()
		unfinished, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-11")
		if err != nil || len(unfinished.Items) != cw {
			t.Fatal("Continue Watching visibility", unfinished, err)
		}
		up, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
		if err != nil || (next == "" && len(up.Items) != 0) || (next != "" && (len(up.Items) != 1 || up.Items[0].Media.ID != next)) {
			t.Fatal("Next Up completion gate", up, err)
		}
	}
	check(0, current)
	session := personalEpisodeSession(t, s, g.FamilyID, g.User.ID, g.DeviceID, current)
	start := nativeWatchEvent(session, 1, 1, "start", 15000)
	start.Media = catalog.MediaIdentity{Kind: "episode", ID: current}
	start.ObservedAt = at.Add(time.Second)
	ingestNativeEvents(t, s, auth, g, start)
	check(1, "")
	finish := nativeWatchEvent(session, 2, 2, "stop", 90000)
	finish.Media, finish.ObservedAt = start.Media, start.ObservedAt
	ingestNativeEvents(t, s, auth, g, finish)
	check(0, last)
	ingestNativeEvents(t, s, auth, g, start, finish)
	check(0, last)
}

func TestNativeNextUpExcludesUnfinishedRewatchesAndSpecialsPerUser(t *testing.T) {
	s, auth, g := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	first, next, special := "tt8888888:1:1", "tt8888888:1:2", "tt8888888:0:1"
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt8888888", Type: "series", Name: "Show"}, Episodes: []cinemeta.Episode{
		{ID: special, Season: 0, Number: 1, ReleaseDate: "2000-01-01"},
		{ID: first, Season: 1, Number: 1, ReleaseDate: "2000-01-01"},
		{ID: next, Season: 1, Number: 2, ReleaseDate: "2000-01-01"},
	}}
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	seedPersonal(t, s, special, true, 0, at)
	up, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(up.Items) != 0 {
		t.Fatal("a watched special alone fabricated regular progress", up, err)
	}
	seedPersonal(t, s, first, true, 0, at)
	session := personalEpisodeSession(t, s, g.FamilyID, g.User.ID, g.DeviceID, first)
	start := nativeWatchEvent(session, 1, 1, "start", 15000)
	start.Media = catalog.MediaIdentity{Kind: "episode", ID: first}
	start.ObservedAt = at.Add(time.Second)
	ingestNativeEvents(t, s, auth, g, start)
	up, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(up.Items) != 0 {
		t.Fatal("a watched flag hid an unfinished rewatch from the exclusion", up, err)
	}
	current, err := s.NativeState(ctx, 1, start.Media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditNative(ctx, auth, g, NativeEdit{Media: start.Media, Revision: current.Revision, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	up, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(up.Items) != 1 || up.Items[0].Media.ID != next {
		t.Fatal("reset rewatch prevented legitimate Next Up", up, err)
	}
	seedPersonal(t, s, special, false, 3000, at)
	up, err = s.PersonalRows(ctx, 1, "next-up", "", "2026-10-11")
	if err != nil || len(up.Items) != 0 {
		t.Fatal("same show appeared beside an unfinished special", up, err)
	}
	other, err := s.PersonalRows(ctx, 99, "next-up", "", "2026-10-11")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("another user's progress leaked", other, err)
	}
}

func TestNativePersonalRowsStartAtZeroAndExplicitProgressReset(t *testing.T) {
	s, auth, g := nativeWatchFixture(t)
	ctx := context.Background()
	session := nativeWatchSession(t, s, g)
	ingestNativeEvents(t, s, auth, g, nativeWatchEvent(session, 1, 1, "start", 0))
	first, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-10")
	if err != nil || len(first.Items) != 0 {
		t.Fatal("brief preview appeared in Continue Watching", first, err)
	}
	ingestNativeEvents(t, s, auth, g, nativeWatchEvent(session, 2, 2, "progress", 15000))
	first, err = s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-10")
	if err != nil || len(first.Items) != 1 || first.Items[0].State.PositionMS != 15000 {
		t.Fatal("qualified viewing missing", first, err)
	}
	if _, err = s.EditNative(ctx, auth, g, NativeEdit{Media: nativeMovie, Revision: 1, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	reset, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-10")
	if err != nil || len(reset.Items) != 0 {
		t.Fatal("explicit reset retained Continue Watching", reset, err)
	}
	id := "tt8888888"
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: "Show"}, Episodes: []cinemeta.Episode{{ID: id + ":1:1", Season: 1, Number: 1, ReleaseDate: "2000-01-01"}}}
	if err = catalog.New(s.pool).SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	seedPersonal(t, s, id+":1:1", false, 0, time.Now())
	if _, err = s.pool.Exec(ctx, `UPDATE watch_current SET state=jsonb_set(state,'{last_played}',to_jsonb(clock_timestamp())) WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, id+":1:1"); err != nil {
		t.Fatal(err)
	}
	episode := catalog.MediaIdentity{Kind: "episode", ID: id + ":1:1"}
	if _, err = s.EditNative(ctx, auth, g, NativeEdit{Media: episode, Revision: 1, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	next, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(next.Items) != 0 {
		t.Fatal("progress reset fabricated completion for Next Up", next, err)
	}
}

func TestNativeContinueWatchingKeepsUnfinishedRewatchAndFencesReset(t *testing.T) {
	s, auth, g := nativeWatchFixture(t)
	ctx := context.Background()
	prior, err := s.EditNative(ctx, auth, g, NativeEdit{Media: nativeMovie, Action: "watched"})
	if err != nil {
		t.Fatal(err)
	}
	session := nativeWatchSession(t, s, g)
	start := nativeWatchEvent(session, 1, 1, "start", 0)
	start.ObservedAt = prior.LastPlayed.Add(time.Second)
	ingestNativeEvents(t, s, auth, g, start)
	check := func(position int64, count int) {
		t.Helper()
		page, err := s.PersonalRows(ctx, g.User.ID, "continue-watching", "", "2026-10-10")
		if err != nil || len(page.Items) != count {
			t.Fatal("unfinished rewatch visibility", page, err)
		}
		if count == 1 && (!page.Items[0].State.Played || page.Items[0].State.PlayCount != 1 || page.Items[0].State.PositionMS != position) {
			t.Fatal("rewatch replaced past watched state or lost progress", page)
		}
	}
	check(0, 0) // A fresh rewatch below 15% preserves the old watched state.
	progress := nativeWatchEvent(session, 2, 2, "progress", 40000)
	progress.ObservedAt = start.ObservedAt
	ingestNativeEvents(t, s, auth, g, progress)
	check(40000, 1)
	current, err := s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditNative(ctx, auth, g, NativeEdit{Media: nativeMovie, Revision: current.Revision, Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	late := nativeWatchEvent(session, 3, 3, "stop", 50000)
	late.ObservedAt = start.ObservedAt
	if ingestNativeEvents(t, s, auth, g, late)[0].Outcome != "stale" {
		t.Fatal("old rewatch undid explicit progress reset")
	}
	check(0, 0)
	ingestNativeEvents(t, s, auth, g, start, progress, late)
	check(0, 0)
	second := nativeWatchSession(t, s, g)
	begin := nativeWatchEvent(second, 4, 1, "start", 0)
	begin.ObservedAt = start.ObservedAt.Add(time.Second)
	finish := nativeWatchEvent(second, 5, 2, "stop", 90000)
	finish.ObservedAt = begin.ObservedAt
	ingestNativeEvents(t, s, auth, g, begin, finish)
	check(0, 0)
	current, err = s.NativeState(ctx, g.User.ID, nativeMovie)
	if err != nil || !current.Played || current.PlayCount != 2 || current.PositionMS != 0 {
		t.Fatal("rewatch completion failed", current, err)
	}
}

func TestNativePersonalRowsTiedPagingAndUnknownSchedule(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	tie := time.Now().UTC().Truncate(time.Microsecond)
	for i := 1; i <= 51; i++ {
		seedPersonal(t, s, fmt.Sprintf("tt%07d", i), false, 1000, tie)
	}
	page, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-10")
	if err != nil || len(page.Items) != 50 || page.Next == "" {
		t.Fatal(page, err)
	}
	second, err := s.PersonalRows(ctx, 1, "continue-watching", page.Next, "2026-10-10")
	if err != nil || len(second.Items) != 1 || second.Next != "" {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(page.Items, second.Items...) {
		if seen[item.Media.ID] {
			t.Fatal("cursor tie repeated media")
		}
		seen[item.Media.ID] = true
	}
	for _, raw := range []string{"invalid", page.Next} {
		row := "continue-watching"
		if raw == page.Next {
			row = "next-up"
		}
		if _, err = s.PersonalRows(ctx, 1, row, raw, "2026-10-10"); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid/cross-row cursor accepted", err)
		}
	}
	seedPersonal(t, s, "tt8888888:1:1", true, 0, tie)
	missing, err := s.PersonalRows(ctx, 1, "next-up", "", "2026-10-10")
	if err != nil || len(missing.Items) != 1 || !missing.Items[0].MissingSchedule || missing.Items[0].Media.Kind != "series" {
		t.Fatal("missing schedule became successful empty or invented episode", missing, err)
	}
}

func TestNativeContinueWatchingSelectsLatestEpisodeWithoutChangingProgress(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	seedPersonal(t, s, "tt14976292:1:1", false, 20000, at)
	seedPersonal(t, s, "tt14976292:1:2", false, 40000, at.Add(time.Minute))
	seedPersonal(t, s, "tt1234567", false, 6000, at)
	check := func(want string) {
		t.Helper()
		page, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-12")
		if err != nil || len(page.Items) != 2 || page.Items[0].Media.ID != want {
			t.Fatal("latest unfinished title", page, err)
		}
	}
	check("tt14976292:1:2")
	// An offline receipt arriving later must not outrank more recent viewing.
	if _, err := s.pool.Exec(ctx, `UPDATE watch_current SET updated_at=$1 WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id='tt14976292:1:1')`, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check("tt14976292:1:2")
	// Rewatching an earlier episode, or starting a special, moves the card.
	seedPersonal(t, s, "tt14976292:1:1", false, 30000, at.Add(2*time.Minute))
	check("tt14976292:1:1")
	seedPersonal(t, s, "tt14976292:0:1", false, 5000, at.Add(3*time.Minute))
	check("tt14976292:0:1")
	var retained int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=1 AND c.cinemeta_id LIKE 'tt14976292:%' AND (w.state->>'position_ticks')::bigint>0`).Scan(&retained); err != nil || retained != 3 {
		t.Fatal("grouping discarded episode progress", retained, err)
	}
	other, err := s.PersonalRows(ctx, 99, "continue-watching", "", "2026-10-12")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("personal representative leaked", other, err)
	}
}

func TestNativeContinueWatchingNumericTiesAndCompletedRepresentatives(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"tt14976292:1:9", "tt14976292:1:10", "tt14976292:0:99"} {
		seedPersonal(t, s, id, false, 20000, at)
	}
	for _, want := range []string{"tt14976292:1:10", "tt14976292:1:9", "tt14976292:0:99"} {
		page, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-12")
		if err != nil || len(page.Items) != 1 || page.Items[0].Media.ID != want {
			t.Fatal("numeric tie or unfinished fallback", page, err)
		}
		seedPersonal(t, s, want, true, 0, at.Add(time.Minute))
	}
	page, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-12")
	if err != nil || len(page.Items) != 0 {
		t.Fatal("completed representatives remained", page, err)
	}
}

func TestNativeContinueWatchingGroupsBeforeCursorPaging(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := 1; i <= 51; i++ {
		seedPersonal(t, s, fmt.Sprintf("tt%07d:1:1", i), false, 1000, at.Add(-time.Hour))
		seedPersonal(t, s, fmt.Sprintf("tt%07d:1:2", i), false, 2000, at)
	}
	first, err := s.PersonalRows(ctx, 1, "continue-watching", "", "2026-10-12")
	if err != nil || len(first.Items) != 50 || first.Next == "" {
		t.Fatal(first, err)
	}
	second, err := s.PersonalRows(ctx, 1, "continue-watching", first.Next, "2026-10-12")
	if err != nil || len(second.Items) != 1 || second.Next != "" {
		t.Fatal("older episodes resurfaced beyond the cursor", second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		parent := NativeParentID(item.Media)
		if seen[parent] || item.Episode != nil || item.State.PositionMS != 2000 {
			t.Fatal("duplicate representative or lost unmapped progress", item)
		}
		seen[parent] = true
	}
}

func TestNativeNextUpSkipsWatchedSuccessorsWithoutFillingEarlierGaps(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt7654321", Type: "series", Name: "Unwatched successors"}}
	for n := 1; n <= 8; n++ {
		show.Episodes = append(show.Episodes, cinemeta.Episode{ID: fmt.Sprintf("%s:1:%d", show.ID, n), Season: 1, Number: n, ReleaseDate: "2020-01-01"})
	}
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: show.ID + ":2:1", Season: 2, Number: 1, ReleaseDate: "2026-10-15"})
	if err := catalog.New(s.pool).SaveSchedule(ctx, show, at); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 5, 6, 7} {
		seedPersonal(t, s, fmt.Sprintf("%s:1:%d", show.ID, n), true, 0, at.Add(-time.Hour))
	}
	seedPersonal(t, s, show.ID+":1:4", true, 0, at)
	check := func(day, want string) {
		t.Helper()
		page, err := s.PersonalRows(ctx, 1, "next-up", "", day)
		if err != nil || (want == "" && len(page.Items) != 0) || (want != "" && (len(page.Items) != 1 || page.Items[0].Media.ID != want || page.Items[0].State.Played)) {
			t.Fatal(page, err)
		}
	}
	check("2026-10-14", show.ID+":1:8")
	seedPersonal(t, s, show.ID+":1:8", true, 0, at.Add(-time.Hour))
	check("2026-10-14", "")
	check("2026-10-15", show.ID+":2:1")
	seedPersonal(t, s, show.ID+":1:6", false, 0, at.Add(-time.Hour))
	check("2026-10-15", show.ID+":1:6")
}
