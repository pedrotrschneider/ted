package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/watchstate"
	"errors"
	"fmt"
	"testing"
	"time"
)

func pollMovie(t *testing.T, watched bool, version int) Record {
	t.Helper()
	count := 0
	if watched {
		count = 1
	}
	raw, _ := json.Marshal(map[string]any{"_id": "tt1234567", "type": "movie", "name": "Test movie", "_mtime": time.Date(2026, 10, 9, 12, 0, version, 0, time.UTC).Format(time.RFC3339), "state": map[string]any{"timesWatched": count, "timeOffset": 999999}})
	r, err := parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func linkedPollFixture(t *testing.T, populated bool) (*Store, accounts.Identity, *remoteStub) {
	t.Helper()
	s, who, remote := storeFixture(t)
	remote.records = []Record{pollMovie(t, true, 1)}
	connectSync(t, s, who)
	if populated {
		pollAndDeliver(t, s)
	}
	return s, who, remote
}
func connectSync(t *testing.T, s *Store, who accounts.Identity) {
	t.Helper()
	ctx := context.Background()
	if err := s.Connect(ctx, who, "test@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
}
func pollAndDeliver(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE stremio_poll_state SET next_poll=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if wait, err := s.Poll(ctx, "remote"); err != nil || wait != time.Minute {
		t.Fatal(wait, err)
	}
	rows, err := s.pool.Query(ctx, `SELECT snapshot_id,link_id::text FROM stremio_poll_deliveries WHERE NOT complete ORDER BY snapshot_id,link_id`)
	if err != nil {
		t.Fatal(err)
	}
	args := []deliveryArgs{}
	for rows.Next() {
		var a deliveryArgs
		if err = rows.Scan(&a.Snapshot, &a.Link); err != nil {
			t.Fatal(err)
		}
		args = append(args, a)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, a := range args {
		for i := 0; i < 10; i++ {
			more, err := s.Deliver(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				break
			}
			if i == 9 {
				t.Fatal("delivery did not finish")
			}
		}
	}
}
func TestStremioWholeAccountEnrollmentUnwatchAndReplay(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	pollAndDeliver(t, s)
	h, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 1 || !h.Current[0].State.Played || h.Current[0].State.Position != 0 || h.Current[0].State.LastPlayed != nil {
		t.Fatal(h, err)
	}
	for _, table := range []string{"media_mappings", "stremio_previews", "stremio_imports"} {
		var count int
		if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	s = NewStore(s.pool, remote, catalogStub{})
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 1 {
		t.Fatal("replay", h, err)
	}
	remote.records = []Record{pollMovie(t, false, 2)}
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 2 || h.Current[0].State.Played {
		t.Fatal(h, err)
	}
}
func TestStremioPollingFailureMissingRecordsAndLocalDecision(t *testing.T) {
	s, who, remote := linkedPollFixture(t, true)
	ctx := context.Background()
	pollAndDeliver(t, s)
	remote.err = ErrIncomplete
	if _, err := s.pool.Exec(ctx, `UPDATE stremio_poll_state SET next_poll=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Poll(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, who)
	if err != nil || status.Sync.Error != "incomplete" {
		t.Fatal(status, err)
	}
	remote.err = nil
	remote.records = []Record{}
	pollAndDeliver(t, s)
	h, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || !h.Current[0].State.Played || len(h.Entries) != 1 {
		t.Fatal("absence reset state", h, err)
	}
	// A newer explicit action in the same polling window wins, including its position.
	if _, err = s.pool.Exec(ctx, `UPDATE watch_current SET source='TEDWrite',state='{"played":true,"position_ticks":123456,"play_count":3,"last_played":null}',updated_at=now() WHERE user_id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	remote.records = []Record{pollMovie(t, false, 2)}
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || !h.Current[0].State.Played || h.Current[0].State.Position != 123456 || len(h.Entries) != 1 {
		t.Fatal(h, err)
	}
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || !h.Current[0].State.Played || len(h.Entries) != 1 {
		t.Fatal("unchanged remote overwrote local", h, err)
	}
}
func TestStremioPollingAccountLockAndIndependentSharedUsers(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	var other int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,role) VALUES('other','','Other','user') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	second := accounts.Identity{ID: other, Version: 1}
	connectSync(t, s, second)
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id='remote' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err = s.Poll(bounded, "remote"); err == nil {
		t.Fatal("account lock bypassed")
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	pollAndDeliver(t, s)
	a, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := watchstate.New(s.pool).History(ctx, second, 0)
	if err != nil || len(a.Entries) != 1 || len(b.Entries) != 1 {
		t.Fatal(a, b, err)
	}
	remote.records = []Record{pollMovie(t, false, 2)}
	pollAndDeliver(t, s)
	a, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err = watchstate.New(s.pool).History(ctx, second, 0)
	if err != nil || len(a.Entries) != 2 || len(b.Entries) != 2 || b.Current[0].State.Played {
		t.Fatal(a, b, err)
	}
	if err = s.Disconnect(ctx, who); err != nil {
		t.Fatal(err)
	}
	remote.records = []Record{pollMovie(t, true, 3)}
	pollAndDeliver(t, s)
	a, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(a.Entries) != 2 {
		t.Fatal("retired user's history changed", a, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET disabled=true WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if delay, err := s.Poll(ctx, "remote"); err != nil || delay != 0 {
		t.Fatal(delay, err)
	}
	if _, err := s.Status(ctx, second); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestStremioEpisodePollingAndIncompleteFlags(t *testing.T) {
	s, who, remote := storeFixture(t)
	ctx := context.Background()
	episodeRecord := func(flags map[string]bool, version int) Record {
		bits, err := EncodeWatched([]string{"tt2934286:1:1", "tt2934286:1:2"}, flags)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"_id": "tt2934286", "type": "series", "name": "Show", "_mtime": time.Date(2026, 10, 9, 12, 0, version, 0, time.UTC).Format(time.RFC3339), "state": map[string]any{"watched": bits, "timeOffset": 99999}})
		r, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	remote.records = []Record{episodeRecord(map[string]bool{"tt2934286:1:1": true}, 1)}
	connectSync(t, s, who)
	pollAndDeliver(t, s)
	s.catalog = unavailablePollCatalog{}
	pollAndDeliver(t, s) // exact accepted snapshot versions need no repeated catalog read
	s.catalog = catalogStub{}
	remote.records = []Record{episodeRecord(map[string]bool{"tt2934286:1:2": true}, 2)}
	pollAndDeliver(t, s)
	h, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 3 || len(h.Current) != 2 {
		t.Fatal(h, err)
	}
	var watched int
	for _, c := range h.Current {
		if c.State.Played {
			watched++
		}
		if c.State.Position != 0 {
			t.Fatal("Stremio position imported")
		}
	}
	if watched != 1 {
		t.Fatal(watched)
	}
	bad := episodeRecord(map[string]bool{}, 3)
	var raw map[string]any
	json.Unmarshal(bad.Raw, &raw)
	raw["state"] = map[string]any{"watched": 42}
	bad.Raw, _ = json.Marshal(raw)
	bad, err = parseRecord(bad.Raw)
	if err != nil {
		t.Fatal(err)
	}
	remote.records = []Record{bad}
	if _, err = s.pool.Exec(ctx, `UPDATE stremio_poll_state SET next_poll=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, who)
	if err != nil || status.Sync.Error != "incomplete" {
		t.Fatal(status.Sync, err)
	}
	h, err = watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 3 {
		t.Fatal(h, err)
	}
}

func TestStremioDeliveryResumesLargeTitleWithoutDuplicateHistory(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	raw := json.RawMessage(`{"_id":"tt2934286","type":"series","name":"Large show","_mtime":"2026-10-09T12:00:00Z","state":{}}`)
	entries := []Entry{}
	for n := 1; n <= 205; n++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("tt2934286:1:%d", n), Name: "Episode", Season: 1, Episode: n})
	}
	titles, _ := json.Marshal(map[string]pollTitle{"tt2934286": {Record: raw, Entries: entries}})
	var args deliveryArgs
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM stremio_links WHERE user_id=$1 AND retired_at IS NULL`, who.ID).Scan(&args.Link); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `INSERT INTO stremio_poll_snapshots(account_id,titles) VALUES('remote',$1) RETURNING id`, titles).Scan(&args.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_poll_deliveries(snapshot_id,link_id) VALUES($1,$2)`, args.Snapshot, args.Link); err != nil {
		t.Fatal(err)
	}
	if more, err := s.Deliver(ctx, args); err != nil || !more {
		t.Fatal(more, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, who.ID).Scan(&n); err != nil || n != 100 {
		t.Fatal(n, err)
	}
	s = NewStore(s.pool, remote, catalogStub{})
	for i := 0; i < 4; i++ {
		more, err := s.Deliver(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if _, err := s.Deliver(ctx, args); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history WHERE user_id=$1`, who.ID).Scan(&n); err != nil || n != 205 {
		t.Fatal("restart/replay duplicated history", n, err)
	}
	var after string
	var complete bool
	if err := s.pool.QueryRow(ctx, `SELECT after_entry,complete FROM stremio_poll_deliveries WHERE snapshot_id=$1 AND link_id=$2`, args.Snapshot, args.Link).Scan(&after, &complete); err != nil || after != "" || !complete {
		t.Fatal(after, complete, err)
	}
}

func TestStremioDispatchReschedulesCompletedJobsWithoutActiveDuplicates(t *testing.T) {
	s, _, _ := linkedPollFixture(t, false)
	ctx := context.Background()
	if err := s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='stremio_poll'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE river_job SET state='completed',finalized_at=now() WHERE kind='stremio_poll'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='stremio_poll'`).Scan(&n); err != nil || n != 2 {
		t.Fatal("completed job suppressed recurring work", n, err)
	}
}

func TestStremioDeliveryFailuresAreVisibleAndResumeWithoutLosingBaseline(t *testing.T) {
	s, who, _ := linkedPollFixture(t, false)
	ctx := context.Background()
	if _, err := s.Poll(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE river_job SET state='discarded',finalized_at=now() WHERE kind='stremio_poll_delivery'`); err != nil {
		t.Fatal(err)
	}
	v, err := s.Status(ctx, who)
	if err != nil || v.Sync.Error != "delivery" || !v.Sync.Pending {
		t.Fatal(v.Sync, err)
	}
	if err = s.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	pollAndDeliver(t, s)
	v, err = s.Status(ctx, who)
	if err != nil || v.Sync.Error != "" || v.Sync.Pending {
		t.Fatal(v.Sync, err)
	}
}

type unavailablePollCatalog struct{}

func (unavailablePollCatalog) Series(context.Context, string) (cinemeta.Series, error) {
	return cinemeta.Series{}, ErrUnavailable
}

func TestStremioInitialEnrollmentPreservesTEDAndQueuesDifferenceOnce(t *testing.T) {
	s, who, remote := storeFixture(t)
	ctx := context.Background()
	remote.records = []Record{pollMovie(t, true, 1)}
	var media string
	if err := s.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata) VALUES('tt1234567','{"name":"Existing"}') RETURNING id::text`).Scan(&media); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,source,state,observed_at) VALUES($1,$2,'TEDLocal','{"played":false,"position_ticks":4321,"play_count":2,"last_played":null}',now())`, who.ID, media); err != nil {
		t.Fatal(err)
	}
	connectSync(t, s, who)
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_outgoing`).Scan(&n); err != nil || n != 0 {
		t.Fatal("wrote before capturing source history", n, err)
	}
	pollAndDeliver(t, s)
	h, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 1 || h.Entries[0].Kind != "stremio_import" || h.Current[0].State.Played || h.Current[0].State.Position != 4321 {
		t.Fatal(h, err)
	}
	pollAndDeliver(t, s)
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_outgoing WHERE NOT played`).Scan(&n); err != nil || n != 1 {
		t.Fatal("bootstrap difference lost or duplicated", n, err)
	}
}

type stagedResolutionCatalog struct {
	calls int
	fail  bool
}

func (c *stagedResolutionCatalog) Series(ctx context.Context, id string) (cinemeta.Series, error) {
	c.calls++
	if id == "tt2934287" && c.fail {
		return cinemeta.Series{}, ErrUnavailable
	}
	return cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: "Show"}, Episodes: []cinemeta.Episode{{ID: id + ":1:1", Season: 1, Number: 1, Name: "One"}}}, nil
}
func TestStremioInitialResolutionResumesWithoutPartialHistory(t *testing.T) {
	s, who, remote := storeFixture(t)
	ctx := context.Background()
	remote.records = nil
	for _, id := range []string{"tt2934286", "tt2934287"} {
		bits, err := EncodeWatched([]string{id + ":1:1"}, map[string]bool{id + ":1:1": true})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"_id": id, "type": "series", "name": "Show", "_mtime": "2026-10-09T12:00:00Z", "state": map[string]any{"watched": bits}})
		r, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		remote.records = append(remote.records, r)
	}
	catalog := &stagedResolutionCatalog{fail: true}
	s.catalog = catalog
	connectSync(t, s, who)
	if _, err := s.Poll(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_poll_snapshots`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial snapshot published", n, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_resolved_titles`).Scan(&n); err != nil || n != 1 {
		t.Fatal("resolution progress lost", n, err)
	}
	catalog = &stagedResolutionCatalog{}
	s = NewStore(s.pool, remote, catalog)
	pollAndDeliver(t, s)
	if catalog.calls != 1 {
		t.Fatal("resolved title fetched again", catalog.calls)
	}
	h, err := watchstate.New(s.pool).History(ctx, who, 0)
	if err != nil || len(h.Entries) != 2 {
		t.Fatal(h, err)
	}
}
