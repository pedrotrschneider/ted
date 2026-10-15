package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"strings"
	"time"
)

type outgoingArgs struct {
	Account string `json:"account"`
}

func (outgoingArgs) Kind() string { return "stremio_outgoing" }

type outgoingWorker struct {
	river.WorkerDefaults[outgoingArgs]
	store *Store
}

func (w *outgoingWorker) Work(ctx context.Context, j *river.Job[outgoingArgs]) error {
	for n := 0; n < 20; n++ {
		more, err := w.store.SendStep(ctx, j.Args.Account)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	return river.JobSnooze(time.Second)
}

type writeRemote interface {
	ReadRecord(context.Context, string, string) (Record, error)
	PutRecord(context.Context, string, json.RawMessage) error
}

// Each account row serializes writes with polling and credential changes. The
// prepared record commits BEFORE the remote effect. A restart compares actual
// state with that durable intent rather than replaying an old full-record write.
func (s *Store) SendStep(ctx context.Context, account string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accounts.MutationLockID); err != nil {
		return false, err
	}
	var id, user int64
	var link, media, identity, title, phase string
	var played, enabled bool
	err = tx.QueryRow(ctx, `SELECT o.id,o.user_id,o.link_id::text,o.media_id::text,o.identity,o.title_id,o.phase,o.played,NOT u.disabled FROM stremio_outgoing o JOIN users u ON u.id=o.user_id WHERE o.account_id=$1 AND o.phase IN ('pending','prepared') AND o.next_attempt<=now() AND (NOT u.disabled OR o.phase='prepared') ORDER BY o.id LIMIT 1 FOR SHARE OF u`, account).Scan(&id, &user, &link, &media, &identity, &title, &phase, &played, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var key string
	if err = tx.QueryRow(ctx, `SELECT auth_key FROM stremio_accounts WHERE id=$1 FOR UPDATE`, account).Scan(&key); err != nil {
		return false, err
	}
	var before, desired []byte
	if err = tx.QueryRow(ctx, `SELECT before_record,desired_record FROM stremio_outgoing WHERE id=$1 FOR UPDATE`, id).Scan(&before, &desired); err != nil {
		return false, err
	}
	finish := func(state, code string) (bool, error) {
		_, e := tx.Exec(ctx, `UPDATE stremio_outgoing SET phase=$2,error_code=$3,updated_at=clock_timestamp() WHERE id=$1`, id, state, code)
		if e != nil {
			return false, e
		}
		return true, tx.Commit(ctx)
	}
	var active, superseded, review bool
	if err = tx.QueryRow(ctx, `SELECT l.retired_at IS NULL,EXISTS(SELECT 1 FROM stremio_outgoing newer WHERE newer.account_id=$2 AND newer.identity=$3 AND newer.id>$4),COALESCE((SELECT needs_review FROM watch_current WHERE user_id=$5 AND media_id=$6),false) FROM stremio_links l WHERE l.id=$1`, link, account, identity, id, user, media).Scan(&active, &superseded, &review); err != nil {
		return false, err
	}
	if key == "" || (!active && phase == "pending") {
		return finish("cancelled", "disconnected")
	}
	if phase == "pending" && (superseded || review) {
		return finish("cancelled", "superseded")
	}
	remote, ok := s.remote.(writeRemote)
	if !ok {
		return false, ErrUnavailable
	}
	fail := func(e error) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		_, err := tx.Exec(ctx, `UPDATE stremio_outgoing SET attempts=attempts+1,error_code=$2,next_attempt=clock_timestamp()+make_interval(secs=>LEAST(1800,30*power(2,LEAST(attempts,6)))),updated_at=clock_timestamp() WHERE id=$1`, id, failureCode(e))
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	record, err := remote.ReadRecord(ctx, key, title)
	absent := errors.Is(err, ErrNotFound)
	if err != nil && !absent {
		return fail(err)
	}
	if phase == "pending" {
		if absent && !played {
			return finish("applied", "")
		}
		var initial []byte
		if absent {
			var meta cinemeta.Movie
			if strings.Contains(identity, ":") {
				show, e := s.catalog.Series(ctx, title)
				if e != nil {
					return fail(e)
				}
				meta = show.Movie
			} else {
				var raw []byte
				if e := tx.QueryRow(ctx, `SELECT metadata FROM catalog_media WHERE id=$1`, media).Scan(&raw); e != nil {
					return false, e
				}
				if json.Unmarshal(raw, &meta) != nil {
					return fail(ErrIncomplete)
				}
				meta.ID = title
				meta.Type = "movie"
			}
			record, err = newWatchedRecord(meta, time.Now())
			if err != nil {
				return fail(err)
			}
			initial = []byte("null") // Durable evidence that the pre-write record was absent.
		} else {
			initial = record.Raw
		}
		var ids []string
		if record.Type == "series" {
			show, e := s.catalog.Series(ctx, record.ID)
			if e != nil {
				return fail(e)
			}
			for _, episode := range show.Episodes {
				ids = append(ids, episode.ID)
			}
		}
		raw, e := WatchedPatch(record, identity, played, ids, time.Now())
		if e != nil {
			return fail(e)
		}
		if !absent && sameRecord(raw, record.Raw, false) {
			return finish("applied", "")
		}
		// Persist the exact patch. The next step always rereads immediately before put.
		_, err = tx.Exec(ctx, `UPDATE stremio_outgoing SET before_record=$2,desired_record=$3,phase='prepared',error_code='',updated_at=clock_timestamp() WHERE id=$1`, id, initial, []byte(raw))
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	acknowledge := func(record Record) (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE stremio_outgoing SET phase='applied',verified_record=$2,error_code='',updated_at=clock_timestamp() WHERE id=$1`, id, []byte(record.Raw))
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stremio_outgoing_echoes(outgoing_id,link_id) SELECT $1,l.id FROM stremio_links l WHERE l.account_id=$2 AND l.retired_at IS NULL ON CONFLICT DO NOTHING`, id, account)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if !absent && (sameRecord(record.Raw, desired, false) || bytes.Equal(bytes.TrimSpace(before), []byte("null")) && sameCreatedRecord(record.Raw, desired)) {
		return acknowledge(record)
	}
	if (absent && !bytes.Equal(bytes.TrimSpace(before), []byte("null"))) || (!absent && !sameRecord(record.Raw, before, false)) {
		return finish("conflict", "remote_changed")
	}
	if superseded || review {
		return finish("cancelled", "superseded")
	}
	if !active {
		return finish("cancelled", "disconnected")
	}
	if !enabled {
		// Resolve an uncertain effect read-only even for a disabled profile, so
		// shared-account polling is not held indefinitely. Unsent intent can pause.
		_, err = tx.Exec(ctx, `UPDATE stremio_outgoing SET phase='pending',before_record=NULL,desired_record=NULL,error_code='',updated_at=clock_timestamp() WHERE id=$1`, id)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	// No remote call or catalog lookup occurs between this fresh read and write.
	if err = remote.PutRecord(ctx, key, desired); err != nil {
		return fail(err)
	}
	verified, err := remote.ReadRecord(ctx, key, title)
	if err != nil {
		return fail(err)
	}
	if !sameRecord(verified.Raw, desired, false) && !(bytes.Equal(bytes.TrimSpace(before), []byte("null")) && sameCreatedRecord(verified.Raw, desired)) {
		return finish("conflict", "readback_changed")
	}
	return acknowledge(verified)
}

// Match Stremio's temporary watch-history records without pinning to the library.
// Only verified absence permits construction; existing records are patched intact.
func newWatchedRecord(meta cinemeta.Movie, at time.Time) (Record, error) {
	if !cinemeta.ValidID(meta.ID) || meta.Name == "" || (meta.Type != "movie" && meta.Type != "series") {
		return Record{}, ErrIncomplete
	}
	stamp := at.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
	raw, err := json.Marshal(map[string]any{"_id": meta.ID, "name": meta.Name, "type": meta.Type, "poster": meta.Poster, "posterShape": "poster", "removed": true, "temp": true, "_ctime": stamp, "_mtime": stamp, "background": "", "logo": "", "year": "", "state": map[string]any{"lastWatched": stamp, "timeWatched": 0, "timeOffset": 0, "overallTimeWatched": 0, "timesWatched": 0, "flaggedWatched": 0, "duration": 0, "video_id": "", "season": 0, "episode": 0, "watched": "", "noNotif": false}})
	if err != nil {
		return Record{}, err
	}
	return parseRecord(raw)
}

// The deployed datastore fills these empty fields and drops empty behaviorHints
// on insertion. Apply only proven defaults to the expected creation record;
// every actual field must still match, including the version and watched bits.
func sameCreatedRecord(actual, desired json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(desired, &value) != nil || value == nil {
		return false
	}
	for _, key := range []string{"background", "logo", "year"} {
		if _, ok := value[key]; !ok {
			value[key] = json.RawMessage(`""`)
		}
	}
	var hints map[string]json.RawMessage
	if json.Unmarshal(value["behaviorHints"], &hints) == nil && len(hints) == 0 {
		delete(value, "behaviorHints")
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(value["state"], &state) != nil || state == nil {
		return false
	}
	for _, key := range []string{"video_id", "watched"} {
		if raw, ok := state[key]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			state[key] = json.RawMessage(`""`)
		}
	}
	for _, key := range []string{"season", "episode"} {
		if _, ok := state[key]; !ok {
			state[key] = json.RawMessage("0")
		}
	}
	value["state"], _ = json.Marshal(state)
	raw, err := json.Marshal(value)
	return err == nil && sameRecord(actual, raw, false)
}
