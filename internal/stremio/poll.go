package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/watchstate"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

type pollTitle struct {
	Record     json.RawMessage    `json:"record"`
	Entries    []Entry            `json:"entries"`
	Issue      string             `json:"issue"`
	Completion *episodeCompletion `json:"completion"`
}
type pollArgs struct {
	Account string `json:"account"`
}

func (pollArgs) Kind() string { return "stremio_poll" }

type deliveryArgs struct {
	Snapshot int64  `json:"snapshot"`
	Link     string `json:"link"`
}

func (deliveryArgs) Kind() string { return "stremio_poll_delivery" }

type dispatchArgs struct{}

func (dispatchArgs) Kind() string { return "stremio_dispatch" }
func PeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return dispatchArgs{}, pollOptions()
	}, &river.PeriodicJobOpts{RunOnStart: true})
}
func (s *Store) enqueue(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema})
	if err != nil {
		return err
	}
	_, err = q.InsertTx(ctx, tx, args, pollOptions())
	return err
}

// Dispatch repairs missing jobs after restarts/exhausted retries and initializes
// every active account link without a separate import/selection gate.
func (s *Store) Dispatch(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT l.id::text FROM stremio_links l JOIN users u ON u.id=l.user_id WHERE l.retired_at IS NULL AND NOT u.disabled`)
	if err != nil {
		return err
	}
	links := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		links = append(links, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, link := range links {
		if err = s.seedSync(ctx, link); err != nil {
			return err
		}
	}
	return s.dispatchCopies(ctx)
}
func (s *Store) seedSync(ctx context.Context, link string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accounts.MutationLockID); err != nil {
		return err
	}
	var account string
	err = tx.QueryRow(ctx, `SELECT l.account_id FROM stremio_links l JOIN users u ON u.id=l.user_id WHERE l.id=$1 AND l.retired_at IS NULL AND NOT u.disabled FOR SHARE OF u`, link).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE`, account); err != nil {
		return err
	}
	// Disconnect uses the same user-before-account ordering.
	var active bool
	if err = tx.QueryRow(ctx, `SELECT retired_at IS NULL FROM stremio_links WHERE id=$1`, link).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stremio_sync_links(link_id) VALUES($1) ON CONFLICT DO NOTHING`, link); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stremio_poll_state(account_id) VALUES($1) ON CONFLICT DO NOTHING`, account); err != nil {
		return err
	}
	if err = s.enqueue(ctx, tx, pollArgs{account}); err != nil {
		return err
	}
	if err = s.enqueue(ctx, tx, outgoingArgs{account}); err != nil {
		return err
	}
	// Rescue unfinished deliveries without creating duplicate active jobs.
	if err = s.enqueueOldestDelivery(ctx, tx, link); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Later snapshots remain durable without jobs repeatedly waking to wait for
// their predecessor. Completion chains the next job; dispatch repairs gaps.
func (s *Store) enqueueOldestDelivery(ctx context.Context, tx pgx.Tx, link string) error {
	var snapshot int64
	err := tx.QueryRow(ctx, `SELECT snapshot_id FROM stremio_poll_deliveries WHERE link_id=$1 AND NOT complete ORDER BY snapshot_id LIMIT 1`, link).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.enqueue(ctx, tx, deliveryArgs{snapshot, link})
}

// Poll performs only remote reads and durable snapshot capture under the account
// lock. Per-user effects happen in separate bounded delivery transactions.
func (s *Store) Poll(ctx context.Context, account string) (time.Duration, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer database.Rollback(tx)
	var key string
	if err = tx.QueryRow(ctx, `SELECT auth_key FROM stremio_accounts WHERE id=$1 FOR NO KEY UPDATE`, account).Scan(&key); err != nil {
		return 0, err
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stremio_sync_links s JOIN stremio_links l ON l.id=s.link_id JOIN users u ON u.id=l.user_id WHERE l.account_id=$1 AND l.retired_at IS NULL AND NOT u.disabled)`, account).Scan(&enabled); err != nil {
		return 0, err
	}
	if !enabled || key == "" {
		return 0, nil
	}
	var waitSeconds float64
	var failures int
	if err = tx.QueryRow(ctx, `SELECT GREATEST(0,EXTRACT(EPOCH FROM next_poll-clock_timestamp()))::float8,failures FROM stremio_poll_state WHERE account_id=$1`, account).Scan(&waitSeconds, &failures); err != nil {
		return 0, err
	}
	if waitSeconds > 0 {
		return time.Duration(waitSeconds * float64(time.Second)), nil
	}
	fail := func(e error) (time.Duration, error) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		delay := time.Minute * time.Duration(1<<min(failures, 5))
		if _, err = tx.Exec(ctx, `UPDATE stremio_poll_state SET error_code=$2,failures=failures+1,next_poll=now()+$3*interval '1 second' WHERE account_id=$1`, account, failureCode(e), delay.Seconds()); err != nil {
			return 0, err
		}
		return delay, tx.Commit(ctx)
	}
	var prior map[string]pollTitle
	var previous []byte
	var snapshot int64
	err = tx.QueryRow(ctx, `SELECT id,titles FROM stremio_poll_snapshots WHERE account_id=$1 ORDER BY id DESC LIMIT 1`, account).Scan(&snapshot, &previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if previous != nil {
		if err = json.Unmarshal(previous, &prior); err != nil {
			return 0, err
		}
	}
	if prior == nil {
		prior = map[string]pollTitle{}
	}
	known := []Record{}
	for _, t := range prior {
		r, e := parseRecord(t.Record)
		if e != nil {
			return 0, e
		}
		known = append(known, r)
	}
	var records []Record
	if client, ok := s.remote.(interface {
		SnapshotSince(context.Context, string, []Record) ([]Record, error)
	}); ok && previous != nil {
		records, err = client.SnapshotSince(ctx, key, known)
	} else {
		records, err = s.remote.Snapshot(ctx, key)
	}
	if err != nil {
		return fail(err)
	}
	if records == nil {
		return fail(ErrIncomplete)
	}
	// Unconfirmed remote effects are not external viewing observations. Keep
	// their last accepted title snapshot until restart recovery verifies them.
	pendingRows, e := tx.Query(ctx, `SELECT DISTINCT title_id FROM stremio_outgoing WHERE account_id=$1 AND phase='prepared'`, account)
	if e != nil {
		return 0, e
	}
	pendingTitles := map[string]bool{}
	for pendingRows.Next() {
		var id string
		if e = pendingRows.Scan(&id); e != nil {
			pendingRows.Close()
			return 0, e
		}
		pendingTitles[id] = true
	}
	e = pendingRows.Err()
	pendingRows.Close()
	if e != nil {
		return 0, e
	}
	titles := map[string]pollTitle{}
	for _, r := range records {
		if pendingTitles[r.ID] {
			if old, ok := prior[r.ID]; ok {
				titles[r.ID] = old
			}
			continue
		}
		if old, ok := prior[r.ID]; ok {
			last, e := parseRecord(old.Record)
			if e != nil {
				return 0, e
			}
			if last.Modified == r.Modified && old.Issue == "" {
				old.Completion = completedEpisode(last, old.Entries, time.Now(), &old)
				titles[r.ID] = old
				continue
			}
		}
		// Keep resolution work across failed or interrupted full-account reads.
		// This cache is never delivered until the complete snapshot is accepted.
		var cached pollTitle
		var cachedJSON []byte
		cacheErr := tx.QueryRow(ctx, `SELECT value FROM stremio_resolved_titles WHERE account_id=$1 AND title_id=$2`, account, r.ID).Scan(&cachedJSON)
		if cacheErr != nil && !errors.Is(cacheErr, pgx.ErrNoRows) {
			return 0, cacheErr
		}
		if cacheErr == nil {
			if e = json.Unmarshal(cachedJSON, &cached); e != nil {
				return 0, e
			}
			last, e := parseRecord(cached.Record)
			if e != nil {
				return 0, e
			}
			if last.Modified == r.Modified && cached.Issue == "" {
				cached.Completion = completedEpisode(last, cached.Entries, time.Now(), &cached)
				titles[r.ID] = cached
				continue
			}
		}
		entries, _, e := Resolve(ctx, r, s.catalog)
		issue := ""
		if e != nil {
			if errors.Is(e, ErrAmbiguous) || errors.Is(e, cinemeta.ErrNotFound) {
				issue = "identity_review"
				entries = []Entry{}
			} else {
				return fail(e)
			}
		}
		old, seen := prior[r.ID]
		var before *pollTitle
		if seen {
			before = &old
		}
		titles[r.ID] = pollTitle{Record: r.Raw, Entries: entries, Issue: issue, Completion: completedEpisode(r, entries, time.Now(), before)}
		if issue == "" {
			encoded, _ := json.Marshal(titles[r.ID])
			// A separate commit retains progress if the snapshot transaction is
			// cancelled. NO KEY UPDATE above permits this FK's key-share lock,
			// while continuing to serialize account reads and outgoing writes.
			if _, e = s.pool.Exec(ctx, `INSERT INTO stremio_resolved_titles(account_id,title_id,value) VALUES($1,$2,$3) ON CONFLICT(account_id,title_id) DO UPDATE SET value=EXCLUDED.value`, account, r.ID, encoded); e != nil {
				return 0, e
			}
		}
	}
	raw, err := json.Marshal(titles)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM stremio_resolved_titles WHERE account_id=$1`, account); err != nil {
		return 0, err
	}
	var reuseSnapshot bool
	if previous != nil {
		if err = tx.QueryRow(ctx, `SELECT titles=$2::jsonb AND EXISTS(SELECT 1 FROM stremio_poll_deliveries WHERE snapshot_id=$1 AND NOT complete) AND NOT EXISTS(SELECT 1 FROM stremio_poll_deliveries d JOIN stremio_links l ON l.id=d.link_id JOIN users u ON u.id=l.user_id WHERE d.snapshot_id=$1 AND d.complete AND l.retired_at IS NULL AND NOT u.disabled) FROM stremio_poll_snapshots WHERE id=$1`, snapshot, raw).Scan(&reuseSnapshot); err != nil {
			return 0, err
		}
	}
	// An unchanged read cannot add work while every active link is still waiting
	// on the identical snapshot. A slower link must not freeze other users' dates.
	// After delivery, retain a fresh observation so explicit-local-change windows
	// continue advancing even when Stremio's watched flags stay unchanged.
	if !reuseSnapshot {
		if err = tx.QueryRow(ctx, `INSERT INTO stremio_poll_snapshots(account_id,titles,captured_at) VALUES($1,$2,clock_timestamp()) RETURNING id`, account, raw).Scan(&snapshot); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stremio_poll_deliveries(snapshot_id,link_id) SELECT $2,l.id FROM stremio_sync_links s JOIN stremio_links l ON l.id=s.link_id JOIN users u ON u.id=l.user_id WHERE l.account_id=$1 AND l.retired_at IS NULL AND NOT u.disabled ON CONFLICT DO NOTHING`, account, snapshot); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT l.id::text FROM stremio_sync_links s JOIN stremio_links l ON l.id=s.link_id JOIN users u ON u.id=l.user_id WHERE l.account_id=$1 AND l.retired_at IS NULL AND NOT u.disabled`, account)
	if err != nil {
		return 0, err
	}
	links := []string{}
	for rows.Next() {
		var link string
		if err = rows.Scan(&link); err != nil {
			break
		}
		links = append(links, link)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, link := range links {
		if err = s.enqueueOldestDelivery(ctx, tx, link); err != nil {
			return 0, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE stremio_poll_state SET last_success=now(),error_code='',failures=0,next_poll=now()+interval '60 seconds' WHERE account_id=$1`, account); err != nil {
		return 0, err
	}
	// Keep the latest full snapshot and any undelivered work. Per-user baselines,
	// watch events are independently durable after delivery.
	if _, err = tx.Exec(ctx, `DELETE FROM stremio_poll_deliveries d USING stremio_poll_snapshots p WHERE p.id=d.snapshot_id AND p.account_id=$1 AND p.id<$2 AND d.complete`, account, snapshot); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM stremio_poll_snapshots p WHERE p.account_id=$1 AND p.id<$2 AND NOT EXISTS(SELECT 1 FROM stremio_poll_deliveries d WHERE d.snapshot_id=p.id)`, account, snapshot); err != nil {
		return 0, err
	}
	return time.Minute, tx.Commit(ctx)
}

// Deliver advances unchanged titles and at most one changed title in snapshot
// order, with baselines and per-user watch effects in the same transaction.
func (s *Store) Deliver(ctx context.Context, args deliveryArgs) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accounts.MutationLockID); err != nil {
		return false, err
	}
	var user int64
	var account string
	var active bool
	err = tx.QueryRow(ctx, `SELECT l.user_id,l.account_id,NOT u.disabled AND l.retired_at IS NULL FROM stremio_links l JOIN users u ON u.id=l.user_id WHERE l.id=$1 FOR SHARE OF u`, args.Link).Scan(&user, &account, &active)
	if err != nil {
		return false, err
	}
	var done, earlier bool
	var after, afterEntry string
	err = tx.QueryRow(ctx, `SELECT complete,after_title,after_entry,EXISTS(SELECT 1 FROM stremio_poll_deliveries WHERE link_id=$2 AND snapshot_id<$1 AND NOT complete) FROM stremio_poll_deliveries WHERE snapshot_id=$1 AND link_id=$2 FOR UPDATE`, args.Snapshot, args.Link).Scan(&done, &after, &afterEntry, &earlier)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if done {
		return false, nil
	}
	if earlier {
		if err = s.enqueueOldestDelivery(ctx, tx, args.Link); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if !active {
		_, err = tx.Exec(ctx, `UPDATE stremio_poll_deliveries SET complete=true WHERE snapshot_id=$1 AND link_id=$2`, args.Snapshot, args.Link)
		if err != nil {
			return false, err
		}
		if err = s.enqueueOldestDelivery(ctx, tx, args.Link); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	var title string
	var value []byte
	var at time.Time
	err = tx.QueryRow(ctx, `SELECT e.key,e.value,p.captured_at FROM stremio_poll_snapshots p CROSS JOIN LATERAL jsonb_each(p.titles) e LEFT JOIN stremio_sync_baselines b ON b.link_id=$3 AND b.title_id=e.key WHERE p.id=$1 AND e.key>$2 AND NOT COALESCE(`+unchangedPollTitleSQL+`,false) ORDER BY e.key LIMIT 1`, args.Snapshot, after, args.Link).Scan(&title, &value, &at)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if e := advanceUnchangedTitles(ctx, tx, args, after, title); e != nil {
		return false, e
	}
	if errors.Is(err, pgx.ErrNoRows) {
		result, e := tx.Exec(ctx, `UPDATE stremio_sync_links SET enrolled_at=now() WHERE link_id=$1 AND enrolled_at IS NULL`, args.Link)
		if e != nil {
			return false, e
		}
		if result.RowsAffected() > 0 {
			// The first complete snapshot has recorded remote history. Now send
			// retained TED decisions that differ, including TED-only watched titles.
			if _, err = tx.Exec(ctx, `INSERT INTO stremio_outgoing(link_id,account_id,user_id,media_id,identity,title_id,played)
              SELECT $1,$2,w.user_id,c.id,c.cinemeta_id,split_part(c.cinemeta_id,':',1),COALESCE((w.state->>'played')::boolean,false)
              FROM watch_current w JOIN catalog_media c ON c.id=w.media_id
              WHERE w.user_id=$3 AND NOT w.needs_review
              AND COALESCE((w.state->>'played')::boolean,false) != EXISTS(SELECT 1 FROM stremio_sync_baselines b CROSS JOIN LATERAL jsonb_array_elements(b.entries) e WHERE b.link_id=$1 AND e->>'id'=c.cinemeta_id)
              AND NOT EXISTS(SELECT 1 FROM stremio_sync_baselines b WHERE b.link_id=$1 AND b.title_id=split_part(c.cinemeta_id,':',1) AND b.issue!='')
              AND NOT EXISTS(SELECT 1 FROM stremio_outgoing o WHERE o.link_id=$1 AND o.media_id=c.id AND o.phase IN('pending','prepared'))`, args.Link, account, user); err != nil {
				return false, err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE stremio_poll_deliveries SET complete=true WHERE snapshot_id=$1 AND link_id=$2`, args.Snapshot, args.Link)
		if err != nil {
			return false, err
		}
		if err = s.enqueueOldestDelivery(ctx, tx, args.Link); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	var current pollTitle
	if err = json.Unmarshal(value, &current); err != nil {
		return false, err
	}
	if !current.validCompletion(title) {
		return false, ErrIncomplete
	}
	var old []Entry
	var oldIssue string
	var since time.Time
	err = tx.QueryRow(ctx, `SELECT entries,issue,observed_at FROM stremio_sync_baselines WHERE link_id=$1 AND title_id=$2`, args.Link, title).Scan(&old, &oldIssue, &since)
	initial := errors.Is(err, pgx.ErrNoRows)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil // No baseline: enroll all watched entries, preserving existing TED decisions.
	}
	if err != nil {
		return false, err
	}
	// Unsupported records preserve their old baseline. The first resolvable state
	// after an identity issue enrolls watched evidence, never a guessed unwatch.
	if current.Issue == "" && at.After(since) {
		r, e := parseRecord(current.Record)
		if e != nil {
			return false, e
		}
		if err = watchstate.ObserveAccountTitleAdded(ctx, tx, user, title, r.AddedAt, at); err != nil {
			return false, err
		}
		before := map[string]Entry{}
		now := map[string]Entry{}
		for _, e := range old {
			before[e.ID] = e
		}
		for _, e := range current.Entries {
			now[e.ID] = e
		}
		{
			changes := map[string]Entry{}
			for id, e := range before {
				if _, ok := now[id]; !ok && oldIssue == "" {
					changes[id] = e
				}
			}
			for id, e := range now {
				if _, ok := before[id]; !ok {
					changes[id] = e
				}
			}
			ids := []string{}
			for id := range changes {
				if id > afterEntry {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			moreEntries := len(ids) > 100
			if moreEntries {
				ids = ids[:100]
			}
			for _, id := range ids {
				e := changes[id]
				_, watched := now[id]
				var own, ownPlayed bool
				echoErr := tx.QueryRow(ctx, `SELECT o.played FROM stremio_outgoing_echoes e JOIN stremio_outgoing o ON o.id=e.outgoing_id WHERE e.link_id=$1 AND NOT e.consumed AND o.identity=$2 AND o.updated_at<=$3 ORDER BY o.id DESC LIMIT 1`, args.Link, id, at).Scan(&ownPlayed)
				if echoErr != nil && !errors.Is(echoErr, pgx.ErrNoRows) {
					return false, echoErr
				}
				own = echoErr == nil && ownPlayed == watched
				if (initial || oldIssue != "") && watched && !own {
					if err = watchstate.EnrollStremio(ctx, tx, user, account, id, entryMetadata(r, e), at); err != nil {
						return false, err
					}
					continue
				}
				observe := watchstate.ObserveStremio
				if own {
					observe = watchstate.ObserveStremioEcho
				}
				if err = observe(ctx, tx, user, account, id, entryMetadata(r, e), watched, since, at); err != nil {
					return false, err
				}
			}
			if moreEntries {
				if _, err = tx.Exec(ctx, `UPDATE stremio_poll_deliveries SET after_entry=$3 WHERE snapshot_id=$1 AND link_id=$2`, args.Snapshot, args.Link, ids[len(ids)-1]); err != nil {
					return false, err
				}
				return true, tx.Commit(ctx)
			}
		}
		// Consume acknowledgments even when multiple local changes returned to
		// the previous baseline, so a later genuine external watch is not hidden.
		if _, err = tx.Exec(ctx, `UPDATE stremio_outgoing_echoes e SET consumed=true FROM stremio_outgoing o WHERE o.id=e.outgoing_id AND e.link_id=$1 AND o.title_id=$2 AND o.updated_at<=$3`, args.Link, title, at); err != nil {
			return false, err
		}
		entries, _ := json.Marshal(current.Entries)
		if current.Completion != nil {
			if err = watchstate.ObserveStremioEpisodeCompletion(ctx, tx, user, account, current.Completion.ID, current.Completion.At); err != nil {
				return false, err
			}
		}
		completion, e := json.Marshal(current.Completion)
		if e != nil {
			return false, e
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stremio_sync_baselines(link_id,title_id,entries,issue,observed_at,completion) VALUES($1,$2,$3,'',$4,$5) ON CONFLICT(link_id,title_id) DO UPDATE SET entries=EXCLUDED.entries,issue='',observed_at=EXCLUDED.observed_at,completion=EXCLUDED.completion`, args.Link, title, entries, at, completion); err != nil {
			return false, err
		}
	} else if current.Issue != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO stremio_sync_baselines(link_id,title_id,entries,issue,observed_at) VALUES($1,$2,'[]',$3,$4) ON CONFLICT(link_id,title_id) DO UPDATE SET issue=EXCLUDED.issue`, args.Link, title, current.Issue, at); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE stremio_poll_deliveries SET after_title=$3,after_entry='' WHERE snapshot_id=$1 AND link_id=$2`, args.Snapshot, args.Link, title); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Advance only the already-enrolled, unchanged prefix before the next changed
// title. Preserve observation dates and echo consumption in the same snapshot
// transaction, without expanding the hundred-entry watch-mutation boundary.
const unchangedPollTitleSQL = `(e.value->>'issue'='' AND b.issue='' AND b.entries=e.value->'entries' AND b.completion=COALESCE(e.value->'completion','null'::jsonb) OR e.value->>'issue'!='' AND b.issue=e.value->>'issue')`

func advanceUnchangedTitles(ctx context.Context, tx pgx.Tx, args deliveryArgs, after, next string) error {
	const prefix = ` FROM stremio_poll_snapshots p CROSS JOIN LATERAL jsonb_each(p.titles) e JOIN stremio_sync_baselines b ON b.link_id=$3 AND b.title_id=e.key WHERE p.id=$1 AND e.key>$2 AND ($4='' OR e.key<$4) AND ` + unchangedPollTitleSQL
	rows, err := tx.Query(ctx, `SELECT e.key,e.value`+prefix, args.Snapshot, after, args.Link, next)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var title pollTitle
		if err = rows.Scan(&key, &title); err != nil {
			break
		}
		if title.Issue == "" {
			if _, err = parseRecord(title.Record); err != nil {
				break
			}
			if !title.validCompletion(key) {
				err = ErrIncomplete
				break
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// An unchanged identity issue keeps its entries and original observation
	// date, exactly as the per-title issue branch does.
	_, err = tx.Exec(ctx, `WITH unchanged AS MATERIALIZED (SELECT e.key title_id,p.captured_at,b.observed_at`+prefix+` AND e.value->>'issue'=''), consumed AS (
 UPDATE stremio_outgoing_echoes echo SET consumed=true FROM stremio_outgoing o,unchanged u WHERE o.id=echo.outgoing_id AND echo.link_id=$3 AND o.title_id=u.title_id AND o.updated_at<=u.captured_at AND u.captured_at>u.observed_at)
 UPDATE stremio_sync_baselines b SET observed_at=u.captured_at FROM unchanged u WHERE b.link_id=$3 AND b.title_id=u.title_id AND u.captured_at>b.observed_at`, args.Snapshot, after, args.Link, next)
	return err
}

func pollOptions() *river.InsertOpts {
	return &river.InsertOpts{Queue: "stremio", MaxAttempts: 10, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}
