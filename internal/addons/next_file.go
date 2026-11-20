package addons

import (
	"context"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type nextFileArgs struct {
	SourceID  string                `json:"source_id" river:"unique"`
	Operation string                `json:"operation_id" river:"unique"`
	Family    string                `json:"family_id" river:"unique"`
	Device    string                `json:"device_id" river:"unique"`
	User      int64                 `json:"user_id"`
	Version   int64                 `json:"account_version"`
	Media     catalog.MediaIdentity `json:"media"`
	Scope     string                `json:"rd_scope"`
	Expires   time.Time             `json:"expires"`
}

func (nextFileArgs) Kind() string { return "next_episode_rd_file" }

type nextFileWorker struct {
	river.WorkerDefaults[nextFileArgs]
	s *Service
}

// The streaming domain has already migrated River in this schema. This queue
// owns only explicit next-episode RD effects, not disposable in-memory bytes.
func (s *Service) StartNextPreparations(ctx context.Context) error {
	if err := s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&s.nextSchema); err != nil {
		return err
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &nextFileWorker{s: s})
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: s.nextSchema, Workers: workers, Queues: map[string]river.QueueConfig{"next_episode": {MaxWorkers: 2}}, JobTimeout: 40 * time.Second, RescueStuckJobsAfter: time.Minute, Logger: s.logger})
	if err != nil {
		return err
	}
	s.next = q
	return q.Start(ctx)
}
func (s *Service) StopNextPreparations(ctx context.Context) error {
	if s.next == nil {
		return nil
	}
	return s.next.StopAndCancel(ctx)
}

func (s *Service) requestNextFile(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, v selected, id, operation string) error {
	if s.next == nil || !nativeUUID.MatchString(operation) {
		return ErrUnavailable
	}
	rd, err := s.RD.Snapshot(ctx)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, g); err != nil {
		return err
	}
	if err = fence(ctx, tx, v.link, g.User.ID); err != nil {
		return err
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return err
	}
	if err = s.RD.ValidateScope(ctx, tx, rd.CredentialScope()); err != nil {
		return err
	}
	lease, err := tx.Exec(ctx, `INSERT INTO native_next_preparations(operation_id,source_id,user_id,family_id,device_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '150 seconds') ON CONFLICT(operation_id) DO UPDATE SET expires_at=excluded.expires_at WHERE native_next_preparations.cancelled_at IS NULL AND native_next_preparations.source_id=excluded.source_id AND native_next_preparations.user_id=excluded.user_id AND native_next_preparations.family_id=excluded.family_id AND native_next_preparations.device_id=excluded.device_id`, operation, id, g.User.ID, g.FamilyID, g.DeviceID)
	if err != nil {
		return err
	}
	if lease.RowsAffected() != 1 {
		return ErrSelection
	}
	result, err := s.next.InsertTx(ctx, tx, nextFileArgs{SourceID: id, Operation: operation, Family: g.FamilyID, Device: g.DeviceID, User: g.User.ID, Version: g.User.Version, Media: v.media, Scope: rd.CredentialScope(), Expires: time.Now().Add(warmLifetime)}, &river.InsertOpts{Queue: "next_episode", MaxAttempts: 60, UniqueOpts: river.UniqueOpts{ByArgs: true, ByQueue: true}})
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// A ready file can be removed externally after a completed request. Restart
	// only that completed intent; never pull a provider-delayed job forward.
	if result.UniqueSkippedAsDuplicate && result.Job.State == "completed" {
		_, err = s.next.JobRetry(ctx, result.Job.ID)
	}
	return err
}
func (s *Service) cancelNextFile(ctx context.Context, g accounts.NativeGrant, id, operation string) error {
	if s.next == nil {
		return nil
	}
	table := pgx.Identifier{s.nextSchema, "river_job"}.Sanitize()
	rows, err := s.pool.Query(ctx, `SELECT id FROM `+table+` WHERE kind='next_episode_rd_file' AND args->>'source_id'=$1 AND args->>'family_id'=$2 AND args->>'device_id'=$3 AND ($4='' OR args->>'operation_id'=$4) AND state IN ('available','pending','running','retryable','scheduled') LIMIT 8`, id, g.FamilyID, g.DeviceID, operation)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var job int64
		if err = rows.Scan(&job); err != nil {
			break
		}
		ids = append(ids, job)
	}
	rows.Close()
	if err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, job := range ids {
		if _, err = s.next.JobCancel(ctx, job); err != nil {
			return err
		}
	}
	_, err = s.pool.Exec(ctx, `UPDATE native_next_preparations SET cancelled_at=clock_timestamp() WHERE source_id=$1 AND family_id=$2 AND device_id=$3 AND ($4='' OR operation_id::text=$4)`, id, g.FamilyID, g.DeviceID, operation)
	return err
}
func (w *nextFileWorker) Work(ctx context.Context, j *river.Job[nextFileArgs]) error {
	a := j.Args
	if time.Now().After(a.Expires) {
		return river.JobCancel(ErrSelection)
	}
	tx, err := w.s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var live bool
	err = tx.QueryRow(ctx, `SELECT cancelled_at IS NULL AND clock_timestamp()<expires_at FROM native_next_preparations WHERE operation_id=$1 AND source_id=$2 AND user_id=$3 AND family_id=$4 AND device_id=$5 FOR SHARE`, a.Operation, a.SourceID, a.User, a.Family, a.Device).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !live {
		return river.JobCancel(ErrSelection)
	}
	if err != nil {
		return err
	}
	// Durable work is owned by the still-valid token family, not an access token
	// that normally rotates while the viewer watches the current episode.
	if err = accounts.AuthorizeMutation(ctx, tx, accounts.Identity{ID: a.User, Version: a.Version}, false); err != nil {
		return river.JobCancel(err)
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT revoked_at IS NULL AND device_id=$3 AND account_version=$4 AND clock_timestamp()<idle_expires_at AND clock_timestamp()<absolute_expires_at FROM native_token_families WHERE id=$1 AND user_id=$2 FOR SHARE`, a.Family, a.User, a.Device, a.Version).Scan(&valid)
	if err != nil || !valid {
		if err == nil {
			err = accounts.ErrUnauthorized
		}
		return river.JobCancel(err)
	}
	g := accounts.NativeGrant{User: accounts.User{ID: a.User, Version: a.Version}, FamilyID: a.Family, DeviceID: a.Device}
	v, err := w.s.selectedForPlayback(ctx, g, a.Media, a.SourceID)
	if err != nil {
		return river.JobCancel(err)
	}
	if err = fence(ctx, tx, v.link, a.User); err != nil {
		return river.JobCancel(err)
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return river.JobCancel(err)
	}
	if err = w.s.RD.ValidateScope(ctx, tx, a.Scope); err != nil {
		return river.JobCancel(err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, _, err = w.s.RD.ResolveSelection(ctx, torrentSelection(v.source))
	var delayed interface{ RetryAfter() time.Duration }
	if errors.As(err, &delayed) && delayed.RetryAfter() > time.Minute {
		return river.JobSnooze(delayed.RetryAfter())
	}
	if errors.Is(err, realdebrid.ErrPending) || errors.Is(err, realdebrid.ErrCachePending) || errors.Is(err, realdebrid.ErrUnavailable) || errors.Is(err, realdebrid.ErrLimited) || errors.Is(err, realdebrid.ErrIncomplete) {
		return river.JobSnooze(time.Minute)
	}
	if err != nil {
		return river.JobCancel(err)
	}
	return tx.Commit(ctx)
}
