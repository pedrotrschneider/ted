package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"fmt"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

func captureDelivery(t *testing.T, s *Store, user int64) deliveryArgs {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE stremio_poll_state SET next_poll=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Poll(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	var args deliveryArgs
	if err := s.pool.QueryRow(ctx, `SELECT d.snapshot_id,d.link_id::text FROM stremio_poll_deliveries d JOIN stremio_links l ON l.id=d.link_id WHERE l.user_id=$1 ORDER BY d.snapshot_id DESC LIMIT 1`, user).Scan(&args.Snapshot, &args.Link); err != nil {
		t.Fatal(err)
	}
	return args
}

func finishDelivery(t *testing.T, s *Store, args deliveryArgs, maxCalls int) {
	t.Helper()
	for i := 0; i < maxCalls; i++ {
		more, err := s.Deliver(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("delivery failed to advance within the expected work bound")
}

func TestStremioPendingUnchangedPollCoalescesAndEnrollsNewSharedLink(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	unresolved, err := parseRecord([]byte(`{"_id":"kitsu:42","type":"series","name":"Unresolved","_mtime":"2026-10-09T12:00:01Z","state":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	remote.records = append(remote.records, unresolved)
	first := captureDelivery(t, s, who.ID)
	if again := captureDelivery(t, s, who.ID); again.Snapshot != first.Snapshot {
		t.Fatal("unchanged pending read created another snapshot", first, again)
	}
	var other int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('other','Other','','user') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	connectSync(t, s, accounts.Identity{ID: other, Version: 1})
	second := captureDelivery(t, s, other)
	if second.Snapshot != first.Snapshot {
		t.Fatal("shared link did not reuse accepted pending snapshot", second)
	}
	finishDelivery(t, s, first, 3)
	// Successful subsequent reads must still advance the observation window,
	// even when Stremio's flags do not change and another user is still behind.
	fresh := captureDelivery(t, s, who.ID)
	if fresh.Snapshot <= first.Snapshot {
		t.Fatal("completed snapshot froze later observations", fresh)
	}
	finishDelivery(t, s, fresh, 1)
	finishDelivery(t, s, second, 3)
	var history int
	var advanced bool
	if err := s.pool.QueryRow(ctx, `SELECT b.observed_at=p.captured_at FROM stremio_sync_baselines b JOIN stremio_poll_snapshots p ON p.id=$2 WHERE b.link_id=$1 AND b.title_id='tt1234567'`, fresh.Link, fresh.Snapshot).Scan(&advanced); err != nil || !advanced {
		t.Fatal("unchanged baseline timestamp did not advance", advanced, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&history); err != nil || history != 2 {
		t.Fatal("unchanged observation fabricated history", history, err)
	}
}

func TestStremioDeliveryQueuesOnlyOldestAndChainsEveryTransition(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	first := captureDelivery(t, s, who.ID)
	remote.records = []Record{pollMovie(t, false, 2)}
	second := captureDelivery(t, s, who.ID)
	var newerJobs int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='stremio_poll_delivery' AND args->>'snapshot'=$1`, fmt.Sprint(second.Snapshot)).Scan(&newerJobs); err != nil || newerJobs != 0 {
		t.Fatal("newer delivery competes with oldest", newerJobs, err)
	}
	// A pre-upgrade newer job must finish instead of snoozing forever, and
	// repair a missing oldest job without changing the persisted snapshots.
	if _, err := s.pool.Exec(ctx, `UPDATE river_job SET state='discarded',finalized_at=now() WHERE kind='stremio_poll_delivery'`); err != nil {
		t.Fatal(err)
	}
	worker := deliveryWorker{store: s}
	if err := worker.Work(ctx, &river.Job[deliveryArgs]{Args: second}); err != nil {
		t.Fatal("blocked newer worker did not release its slot", err)
	}
	var pendingJobs int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='stremio_poll_delivery' AND args->>'snapshot'=$1 AND state='available'`, fmt.Sprint(first.Snapshot)).Scan(&pendingJobs); err != nil || pendingJobs != 1 {
		t.Fatal("oldest job was not repaired exactly once", pendingJobs, err)
	}
	finishDelivery(t, s, first, 2)
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='stremio_poll_delivery' AND args->>'snapshot'=$1 AND state='available'`, fmt.Sprint(second.Snapshot)).Scan(&newerJobs); err != nil || newerJobs != 1 {
		t.Fatal("completion did not chain next snapshot", newerJobs, err)
	}
	finishDelivery(t, s, second, 2)
	var transitions int
	var played bool
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM watch_history),(state->>'played')::bool FROM watch_current WHERE user_id=$1`, who.ID).Scan(&transitions, &played); err != nil || transitions != 2 || played {
		t.Fatal("ordered watch/unwatch transition was lost", transitions, played, err)
	}
}

func TestStremioUnchangedPrefixAdvancesTogetherAndRollsBackWithChangedTitle(t *testing.T) {
	s, who, remote := linkedPollFixture(t, false)
	ctx := context.Background()
	remote.records = nil
	for n := 0; n < 25; n++ {
		raw, _ := json.Marshal(map[string]any{"_id": fmt.Sprintf("tt%07d", 2000000+n), "type": "movie", "name": "Movie", "_mtime": "2026-10-09T12:00:01Z", "state": map[string]any{"timesWatched": 1}})
		r, err := parseRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		remote.records = append(remote.records, r)
	}
	issueRecord, err := parseRecord([]byte(`{"_id":"kitsu:42","type":"series","name":"Unresolved","_mtime":"2026-10-09T12:00:01Z","state":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	remote.records = append(remote.records, issueRecord)
	first := captureDelivery(t, s, who.ID)
	finishDelivery(t, s, first, 27)
	var prior time.Time
	if err := s.pool.QueryRow(ctx, `SELECT observed_at FROM stremio_sync_baselines WHERE link_id=$1 AND title_id=$2`, first.Link, remote.records[0].ID).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	var outgoing int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO stremio_outgoing(link_id,account_id,user_id,media_id,identity,title_id,played,phase) SELECT $1,'remote',$2,id,cinemeta_id,cinemeta_id,true,'applied' FROM catalog_media WHERE cinemeta_id=$3 RETURNING id`, first.Link, who.ID, remote.records[0].ID).Scan(&outgoing); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_outgoing_echoes(outgoing_id,link_id) VALUES($1,$2)`, outgoing, first.Link); err != nil {
		t.Fatal(err)
	}
	last := len(remote.records) - 2
	raw, _ := json.Marshal(map[string]any{"_id": remote.records[last].ID, "type": "movie", "name": "Movie", "_mtime": "2026-10-09T12:00:02Z", "state": map[string]any{"timesWatched": 0}})
	remote.records[last], _ = parseRecord(raw)
	second := captureDelivery(t, s, who.ID)
	for _, bad := range []int{0, last} {
		if _, err := s.pool.Exec(ctx, `UPDATE stremio_poll_snapshots SET titles=jsonb_set(titles,ARRAY[$2,'record'],'{}'::jsonb) WHERE id=$1`, second.Snapshot, remote.records[bad].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Deliver(ctx, second); !errors.Is(err, ErrIncomplete) {
			t.Fatal("malformed persisted title skipped", err)
		}
		var at time.Time
		var consumed bool
		if err := s.pool.QueryRow(ctx, `SELECT b.observed_at,e.consumed FROM stremio_sync_baselines b JOIN stremio_outgoing_echoes e ON e.link_id=b.link_id AND e.outgoing_id=$3 WHERE b.link_id=$1 AND b.title_id=$2`, first.Link, remote.records[0].ID, outgoing).Scan(&at, &consumed); err != nil || !at.Equal(prior) || consumed {
			t.Fatal("failed transaction committed prefix/echo changes", at, consumed, err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE stremio_poll_snapshots SET titles=jsonb_set(titles,ARRAY[$2,'record'],$3::jsonb) WHERE id=$1`, second.Snapshot, remote.records[bad].ID, remote.records[bad].Raw); err != nil {
			t.Fatal(err)
		}
	}
	// The first invocation reaches the changed last title across the unchanged
	// prefix; a second invocation completes, regardless of the prefix's size.
	finishDelivery(t, s, second, 2)
	var refreshed, history int
	var consumed bool
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_sync_baselines b JOIN stremio_poll_snapshots p ON p.id=$2 WHERE b.link_id=$1 AND b.observed_at=p.captured_at`, second.Link, second.Snapshot).Scan(&refreshed); err != nil || refreshed != 25 {
		t.Fatal("prefix observations did not advance together", refreshed, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT consumed FROM stremio_outgoing_echoes WHERE outgoing_id=$1`, outgoing).Scan(&consumed); err != nil || !consumed {
		t.Fatal("unchanged prefix did not consume its acknowledgment", consumed, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&history); err != nil || history != 26 {
		t.Fatal("prefix fabricated/dropped viewing history", history, err)
	}
	var issue string
	var issueDate time.Time
	if err := s.pool.QueryRow(ctx, `SELECT issue,observed_at FROM stremio_sync_baselines WHERE link_id=$1 AND title_id=$2`, first.Link, issueRecord.ID).Scan(&issue, &issueDate); err != nil || issue != "identity_review" || !issueDate.Equal(prior) {
		t.Fatal("unchanged identity issue was cleared or advanced", issue, issueDate, err)
	}
}
