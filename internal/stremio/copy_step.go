package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
)

// CopyStep owns a durable phase, not a browser request. Source credentials are
// used only by Snapshot; every read/put below uses the destination's key.
func (s *Store) CopyStep(ctx context.Context, id string) (bool, error) {
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
	var user int64
	var destination string
	err = tx.QueryRow(ctx, `SELECT user_id,destination_id FROM temporary_stremio_copies WHERE id=$1`, id).Scan(&user, &destination)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT NOT disabled AND role='admin' FROM users WHERE id=$1 FOR SHARE`, user).Scan(&allowed); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE`, destination); err != nil {
		return false, err
	}
	var phase, resume, sourceKey, destinationKey string
	var next time.Time
	if err = tx.QueryRow(ctx, `SELECT phase,resume_phase,source_key,destination_key,next_attempt FROM temporary_stremio_copies WHERE id=$1 FOR UPDATE`, id).Scan(&phase, &resume, &sourceKey, &destinationKey, &next); err != nil {
		return false, err
	}
	if phase == "complete" || phase == "cancelled" {
		return false, tx.Commit(ctx)
	}
	if !allowed {
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copies SET phase='cancelled',source_key='',destination_key='',error_code='',error_title='',updated_at=now() WHERE id=$1`, id)
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if phase == "failed" {
		return false, tx.Commit(ctx)
	}
	if phase == "retrying" {
		if time.Now().Before(next) {
			return true, tx.Commit(ctx)
		}
		phase = resume
		if _, err = tx.Exec(ctx, `UPDATE temporary_stremio_copies SET phase=$2 WHERE id=$1`, id, phase); err != nil {
			return false, err
		}
	}
	fail := func(e error, title string) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		state := "retrying"
		if errors.Is(e, ErrCredentials) || errors.Is(e, ErrIncomplete) || errors.Is(e, ErrAmbiguous) {
			state = "failed"
		}
		_, err := tx.Exec(ctx, `UPDATE temporary_stremio_copies SET resume_phase=$2,phase=$3,error_code=$4,error_title=$5,attempts=attempts+1,next_attempt=clock_timestamp()+make_interval(secs=>LEAST(300,5*power(2,LEAST(attempts,6)))),updated_at=now() WHERE id=$1`, id, phase, state, failureCode(e), title)
		if err != nil {
			return false, err
		}
		return state == "retrying", tx.Commit(ctx)
	}
	advance := func(target string) (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE temporary_stremio_copies SET phase=$2,resume_phase=$2,error_code='',error_title='',attempts=0,next_attempt=now(),updated_at=now(),source_key=CASE WHEN $2='complete' THEN '' ELSE source_key END,destination_key=CASE WHEN $2='complete' THEN '' ELSE destination_key END WHERE id=$1`, id, target)
		if err != nil {
			return false, err
		}
		return target != "complete", tx.Commit(ctx)
	}
	if phase == "queued" {
		records, e := s.remote.Snapshot(ctx, sourceKey)
		if e != nil {
			return fail(e, "")
		}
		// Snapshot's contract is complete and consistent; no destination effects
		// occur before every source record is retained in the same transaction.
		for _, r := range records {
			if _, err = tx.Exec(ctx, `INSERT INTO temporary_stremio_copy_titles(copy_id,title_id,source_record) VALUES($1,$2,$3)`, id, r.ID, r.Raw); err != nil {
				return false, err
			}
		}
		return advance("resolving")
	}
	var title string
	var raw, before, desired, encodedEntries, encodedIDs []byte
	if phase == "resolving" {
		err = tx.QueryRow(ctx, `SELECT title_id,source_record FROM temporary_stremio_copy_titles WHERE copy_id=$1 AND NOT resolved ORDER BY title_id LIMIT 1 FOR UPDATE`, id).Scan(&title, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return advance("copying")
		}
		if err != nil {
			return false, err
		}
		r, e := parseRecord(raw)
		if e != nil {
			return fail(e, title)
		}
		entries, ids, e := Resolve(ctx, r, s.catalog)
		if e != nil {
			if errors.Is(e, ErrIncomplete) || errors.Is(e, ErrAmbiguous) || errors.Is(e, cinemeta.ErrNotFound) {
				_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET resolved=true,done=true,skip_reason=$3 WHERE copy_id=$1 AND title_id=$2`, id, title, failureCode(e))
				if err != nil {
					return false, err
				}
				return true, tx.Commit(ctx)
			}
			return fail(e, title)
		}
		entryJSON, _ := json.Marshal(entries)
		idsJSON, _ := json.Marshal(ids)
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET resolved=true,entries=$3,episode_ids=$4,done=$5 WHERE copy_id=$1 AND title_id=$2`, id, title, entryJSON, idsJSON, len(entries) == 0)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if phase != "copying" {
		return false, ErrIncomplete
	}
	err = tx.QueryRow(ctx, `SELECT title_id,source_record,entries,episode_ids,before_record,desired_record FROM temporary_stremio_copy_titles WHERE copy_id=$1 AND NOT done ORDER BY title_id LIMIT 1 FOR UPDATE`, id).Scan(&title, &raw, &encodedEntries, &encodedIDs, &before, &desired)
	if errors.Is(err, pgx.ErrNoRows) {
		return advance("complete")
	}
	if err != nil {
		return false, err
	}
	var entries []Entry
	var ids []string
	if json.Unmarshal(encodedEntries, &entries) != nil || json.Unmarshal(encodedIDs, &ids) != nil {
		return fail(ErrIncomplete, title)
	}
	source, e := parseRecord(raw)
	if e != nil {
		return fail(e, title)
	}
	remote, ok := s.remote.(writeRemote)
	if !ok {
		return fail(ErrUnavailable, title)
	}
	current, e := remote.ReadRecord(ctx, destinationKey, title)
	absent := errors.Is(e, ErrNotFound)
	if e != nil && !absent {
		return fail(e, title)
	}
	finish := func() (bool, error) {
		_, err := tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET done=true WHERE copy_id=$1 AND title_id=$2`, id, title)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copies SET error_code='',error_title='',attempts=0,next_attempt=now(),updated_at=now() WHERE id=$1`, id)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if len(desired) > 0 && !absent && (sameRecord(current.Raw, desired, false) || (bytes.Equal(before, []byte("null")) && sameCreatedRecord(current.Raw, desired))) {
		return finish()
	}
	if absent {
		var poster struct {
			Poster string `json:"poster"`
		}
		_ = json.Unmarshal(source.Raw, &poster)
		name := source.Name
		if name == "" {
			name = source.ID
		}
		current, e = newWatchedRecord(cinemeta.Movie{ID: source.ID, Name: name, Type: source.Type, Poster: cinemeta.PosterURL(poster.Poster)}, time.Now())
		if e != nil {
			return fail(e, title)
		}
	} else if current.Type != source.Type {
		if len(desired) > 0 {
			return fail(ErrAmbiguous, title)
		}
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET done=true,skip_reason='destination_identity' WHERE copy_id=$1 AND title_id=$2`, id, title)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	merged, e := copyUnion(current, entries, ids, desired, time.Now())
	if e != nil {
		if len(desired) == 0 && (errors.Is(e, ErrIncomplete) || errors.Is(e, ErrAmbiguous)) {
			_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET done=true,skip_reason='destination_incomplete' WHERE copy_id=$1 AND title_id=$2`, id, title)
			if err != nil {
				return false, err
			}
			return true, tx.Commit(ctx)
		}
		return fail(e, title)
	}
	if !absent && sameRecord(current.Raw, merged, false) {
		return finish()
	}
	unchangedBefore := absent && bytes.Equal(before, []byte("null")) || !absent && len(before) > 0 && sameRecord(current.Raw, before, false)
	if len(desired) == 0 || !unchangedBefore {
		initial := current.Raw
		if absent {
			initial = []byte("null")
		}
		// A changed record is rebased additively, retaining current fields and all
		// watched flags in the saved intent. Commit intent before any remote put.
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copy_titles SET before_record=$3,desired_record=$4 WHERE copy_id=$1 AND title_id=$2`, id, title, initial, merged)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	// This current destination read is immediately before the write: no catalog
	// request or source-account call intervenes. The account lock is still held.
	if e = remote.PutRecord(ctx, destinationKey, desired); e != nil {
		return fail(e, title)
	}
	verified, e := remote.ReadRecord(ctx, destinationKey, title)
	if e != nil {
		return fail(e, title)
	}
	if !sameRecord(verified.Raw, desired, false) && !(absent && sameCreatedRecord(verified.Raw, desired)) {
		return fail(ErrChanged, title) // Next attempt reads/rebases; never stale-replays.
	}
	return finish()
}
