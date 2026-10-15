package stremio

import (
	"context"
	"errors"
	"sync"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
)

var ErrCopySameAccount = errors.New("source and destination must be different Stremio accounts")

type CopyCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type CopyRequest struct {
	Source      CopyCredentials `json:"source"`
	Destination CopyCredentials `json:"destination"`
}
type CopySkip struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type CopyStatus struct {
	ID               string     `json:"id"`
	SourceEmail      string     `json:"source_email"`
	DestinationEmail string     `json:"destination_email"`
	Phase            string     `json:"phase"`
	Error            string     `json:"error"`
	ErrorTitle       string     `json:"error_title"`
	Total            int        `json:"total"`
	Resolved         int        `json:"resolved"`
	Finished         int        `json:"finished"`
	WatchedTitles    int        `json:"watched_titles"`
	WatchedItems     int        `json:"watched_items"`
	Skipped          []CopySkip `json:"skipped"`
	NextAttempt      time.Time  `json:"next_attempt"`
}

func (s *Store) StartCopy(ctx context.Context, who accounts.Identity, input CopyRequest) (string, error) {
	// Recheck role/session before credentials leave TED and again before commit.
	check, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	err = accounts.AuthorizeMutation(ctx, check, who, true)
	database.Rollback(check)
	if err != nil {
		return "", err
	}
	var source, destination Session
	var sourceErr, destinationErr error
	var login sync.WaitGroup
	login.Go(func() { source, sourceErr = s.remote.Login(ctx, input.Source.Email, input.Source.Password) })
	login.Go(func() {
		destination, destinationErr = s.remote.Login(ctx, input.Destination.Email, input.Destination.Password)
	})
	login.Wait()
	if sourceErr != nil {
		return "", sourceErr
	}
	if destinationErr != nil {
		return "", destinationErr
	}
	if source.ID == destination.ID || source.Key == destination.Key {
		return "", ErrCopySameAccount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return "", err
	}
	var conflict bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM temporary_stremio_copies WHERE phase NOT IN ('complete','cancelled') AND (user_id=$1 OR destination_id=$2 OR destination_id=$3 OR source_id=$2))`, who.ID, destination.ID, source.ID).Scan(&conflict); err != nil {
		return "", err
	}
	if conflict {
		return "", ErrChanged
	}
	// A row shared with normal sync supplies its existing cross-process lock.
	// Copy keys are separate: no profile is linked and no existing key is replaced.
	if _, err = tx.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES($1,$2,'') ON CONFLICT(id) DO NOTHING`, destination.ID, destination.Email); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE`, destination.ID); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO temporary_stremio_copies(user_id,source_id,source_email,destination_id,destination_email,source_key,destination_key) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, who.ID, source.ID, source.Email, destination.ID, destination.Email, source.Key, destination.Key).Scan(&id)
	if err != nil {
		return "", err
	}
	if err = s.enqueue(ctx, tx, copyArgs{ID: id}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) CopyStatus(ctx context.Context, who accounts.Identity) (*CopyStatus, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return nil, err
	}
	v := &CopyStatus{Skipped: []CopySkip{}}
	err = tx.QueryRow(ctx, `SELECT id::text,source_email,destination_email,phase,error_code,error_title,next_attempt FROM temporary_stremio_copies WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, who.ID).Scan(&v.ID, &v.SourceEmail, &v.DestinationEmail, &v.Phase, &v.Error, &v.ErrorTitle, &v.NextAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	var stopped bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT state IN ('discarded','cancelled') FROM river_job WHERE kind='temporary_stremio_copy' AND args->>'id'=$1 ORDER BY id DESC LIMIT 1),false)`, v.ID).Scan(&stopped); err != nil {
		return nil, err
	}
	if stopped && v.Phase != "complete" && v.Phase != "cancelled" && v.Phase != "failed" {
		v.Phase = "failed"
		if v.Error == "" {
			v.Error = "unavailable"
		}
	}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE resolved),count(*) FILTER(WHERE done),count(*) FILTER(WHERE jsonb_array_length(entries)>0),COALESCE(sum(jsonb_array_length(entries)),0) FROM temporary_stremio_copy_titles WHERE copy_id=$1`, v.ID).Scan(&v.Total, &v.Resolved, &v.Finished, &v.WatchedTitles, &v.WatchedItems)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT title_id,COALESCE(NULLIF(left(source_record->>'name',200),''),title_id),skip_reason FROM temporary_stremio_copy_titles WHERE copy_id=$1 AND skip_reason<>'' ORDER BY title_id`, v.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item CopySkip
		if err = rows.Scan(&item.ID, &item.Name, &item.Reason); err != nil {
			break
		}
		v.Skipped = append(v.Skipped, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) ControlCopy(ctx context.Context, who accounts.Identity, id string, cancel bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	var destination string
	if err = tx.QueryRow(ctx, `SELECT destination_id FROM temporary_stremio_copies WHERE id=$1 AND user_id=$2`, id, who.ID).Scan(&destination); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE`, destination); err != nil {
		return err
	}
	var phase string
	if err = tx.QueryRow(ctx, `SELECT phase FROM temporary_stremio_copies WHERE id=$1 FOR UPDATE`, id).Scan(&phase); err != nil {
		return err
	}
	if phase == "complete" || phase == "cancelled" {
		return ErrChanged
	}
	if cancel {
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copies SET phase='cancelled',source_key='',destination_key='',error_code='',error_title='',updated_at=now() WHERE id=$1`, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE temporary_stremio_copies SET phase=CASE WHEN phase IN ('failed','retrying') THEN resume_phase ELSE phase END,error_code='',error_title='',attempts=0,next_attempt=now(),updated_at=now() WHERE id=$1`, id)
		if err == nil {
			err = s.enqueue(ctx, tx, copyArgs{ID: id})
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
