package watchstate

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"errors"
	"fmt"
	"testing"
	"time"
)

func newReleaseShow(t *testing.T, s *Store, id, release string) {
	t.Helper()
	if err := catalog.New(s.pool).SaveSchedule(context.Background(), cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: id}, Episodes: []cinemeta.Episode{
		{ID: id + ":1:1", Season: 1, Number: 1, ReleaseDate: "2020-01-01"},
		{ID: id + ":1:2", Season: 1, Number: 2, ReleaseDate: release},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
func TestNewReleasesUsesNextUpAnchorAndCalendarWatchDate(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 2, 30, 0, 0, time.UTC)
	newReleaseShow(t, s, "tt9100001", "2026-10-10")
	seedPersonal(t, s, "tt9100001:1:1", true, 0, at)
	if _, err := s.pool.Exec(ctx, `UPDATE watch_current SET state=jsonb_set(state,'{last_played}',to_jsonb($1::timestamptz)) WHERE media_id IN(SELECT id FROM catalog_media WHERE cinemeta_id='tt9100001:1:1')`, at); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-10", loc)
	if err != nil || len(local.Items) != 1 || local.Items[0].Media.ID != "tt9100001:1:2" {
		t.Fatal(local, err)
	}
	utc, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-10")
	if err != nil || len(utc.Items) != 0 {
		t.Fatal("same-day release qualified", utc, err)
	}
	future, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-09", loc)
	if err != nil || len(future.Items) != 0 {
		t.Fatal("future episode qualified", future, err)
	}
	// An unfinished episode still excludes the entire show from both shelves.
	seedPersonal(t, s, "tt9100001:1:2", false, 1000, at.Add(time.Minute))
	for _, row := range []string{"next-up", "new-releases"} {
		page, err := s.PersonalRows(ctx, 1, row, "", "2026-10-10", loc)
		if err != nil || len(page.Items) != 0 {
			t.Fatal(row, page, err)
		}
	}
}

func TestNewReleasesUndatedFlagsUseTEDAccountCreationNotTitleIntake(t *testing.T) {
	s, auth, grant := nativeWatchFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(-1, 0, 0)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET created_at=$1 WHERE id=1`, old); err != nil {
		t.Fatal(err)
	}
	newReleaseShow(t, s, "tt9100002", "2026-10-10")
	seedPersonal(t, s, "tt9100002:1:1", true, 0, now)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = ObserveAccountTitleAdded(ctx, tx, 1, "tt9100002", nil, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		page, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-13")
		if err != nil || len(page.Items) != want {
			t.Fatal(page, err)
		}
	}
	check(1)
	// Provider title creation is retained evidence, but no longer the comparator.
	created := old.AddDate(-1, 0, 0)
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = ObserveAccountTitleAdded(ctx, tx, 1, "tt9100002", &created, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var saved time.Time
	if err = s.pool.QueryRow(ctx, `SELECT added_at FROM watch_account_title_dates WHERE user_id=1 AND title_id='tt9100002'`).Scan(&saved); err != nil || !saved.Equal(created) {
		t.Fatal(saved, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET created_at=$1 WHERE id=1`, now); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err = s.pool.Exec(ctx, `UPDATE users SET created_at=$1 WHERE id=1`, old); err != nil {
		t.Fatal(err)
	}
	check(1)
	// An actual viewing date takes precedence over TED account creation.
	if _, err = s.pool.Exec(ctx, `UPDATE watch_current SET state=jsonb_set(state,'{last_played}',to_jsonb($1::timestamptz)) WHERE media_id IN(SELECT id FROM catalog_media WHERE cinemeta_id='tt9100002:1:1')`, now); err != nil {
		t.Fatal(err)
	}
	check(0)
	// Explicitly marking undated must not resurrect an older dated completion.
	state, err := s.NativeState(ctx, 1, catalog.MediaIdentity{Kind: "episode", ID: "tt9100002:1:1"})
	if err != nil {
		t.Fatal(err)
	}
	dated, err := s.EditNative(ctx, auth, grant, NativeEdit{Media: state.Media, Revision: state.Revision, Action: "watched", DateMode: "now"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditNative(ctx, auth, grant, NativeEdit{Media: state.Media, Revision: dated.Revision, Action: "watched", DateMode: "undated"}); err != nil {
		t.Fatal(err)
	}
	check(1)
	other, err := s.PersonalRows(ctx, 99, "new-releases", "", "2026-10-13")
	if err != nil || len(other.Items) != 0 {
		t.Fatal(other, err)
	}
}

func TestNewReleasesFiltersBeforePaginationAndBindsItsCursor(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET created_at=$1 WHERE id=1`, at); err != nil {
		t.Fatal(err)
	}
	for i := range 120 {
		id := fmt.Sprintf("tt%07d", 9200000+i)
		release := "2020-01-01"
		if i < 60 {
			release = "2026-10-10"
		}
		newReleaseShow(t, s, id, release)
		seedPersonal(t, s, id+":1:1", true, 0, at.Add(time.Duration(i)*time.Minute))
	}
	first, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-13")
	if err != nil || len(first.Items) != 50 || first.Next == "" {
		t.Fatal(first, err)
	}
	next, err := s.PersonalRows(ctx, 1, "new-releases", first.Next, "2026-10-13")
	if err != nil || len(next.Items) != 10 || next.Next != "" {
		t.Fatal(next, err)
	}
	seen := map[string]bool{}
	for _, page := range []NativeRowPage{first, next} {
		for _, item := range page.Items {
			if seen[item.Media.ID] {
				t.Fatal("repeated entry", item.Media)
			}
			seen[item.Media.ID] = true
		}
	}
	if _, err = s.PersonalRows(ctx, 1, "next-up", first.Next, "2026-10-13"); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-row cursor accepted", err)
	}
}

func TestNewReleasesAccountCreationCalendarFallbackAndDatedStremioRecency(t *testing.T) {
	s, _, _ := nativeWatchFixture(t)
	ctx := context.Background()
	created := time.Date(2026, 10, 10, 2, 30, 0, 0, time.UTC)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET created_at=$1 WHERE id=1`, created); err != nil {
		t.Fatal(err)
	}
	newReleaseShow(t, s, "tt9100003", "2026-10-10")
	seedPersonal(t, s, "tt9100003:1:1", true, 0, created.Add(48*time.Hour))
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-13", loc)
	if err != nil || len(local.Items) != 1 {
		t.Fatal("undated viewing without title intake ignored account creation", local, err)
	}
	utc, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-13")
	if err != nil || len(utc.Items) != 0 {
		t.Fatal("same-day account creation qualified", utc, err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('dated-import','fixture@example.com','fixture');
 UPDATE watch_current SET source='StremioPoll' WHERE user_id=1;
 INSERT INTO stremio_episode_recency(user_id,media_id,stremio_account,watched_at)
 SELECT 1,media_id,'dated-import','2026-10-11T12:00:00Z' FROM watch_current WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	dated, err := s.PersonalRows(ctx, 1, "new-releases", "", "2026-10-13", loc)
	if err != nil || len(dated.Items) != 0 {
		t.Fatal("validated watch date lost precedence over account creation", dated, err)
	}
}
