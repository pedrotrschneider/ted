package streaming

import (
	"context"
	"errors"
	"fmt"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type mappingFileArgs struct {
	Operation string `json:"operation"`
	Mapping   string `json:"mapping"`
}

func (mappingFileArgs) Kind() string { return "mapping_file_validation" }

type mappingFileWorker struct {
	river.WorkerDefaults[mappingFileArgs]
	s *Service
}

// Enqueue children in the same transaction as staging. Replayed coordinators
// also recover pre-upgrade validating operations without repeating saved probes.
func (s *Service) queueMappingFiles(ctx context.Context, tx pgx.Tx, operation string) error {
	var phase string
	if err := tx.QueryRow(ctx, "SELECT phase FROM mapping_operations WHERE id=$1 FOR UPDATE", operation).Scan(&phase); err != nil {
		return err
	}
	if phase != "validating" {
		return nil
	}
	rows, err := tx.Query(ctx, "SELECT mapping_id::text FROM mapping_candidates WHERE operation_id=$1 AND probe IS NULL AND validation_job_id IS NULL ORDER BY media_id", operation)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		job, e := s.queue.InsertTx(ctx, tx, mappingFileArgs{operation, id}, &river.InsertOpts{Queue: "mapping_validation", MaxAttempts: 3})
		if e != nil {
			return e
		}
		if _, err = tx.Exec(ctx, "UPDATE mapping_candidates SET validation_job_id=$2 WHERE mapping_id=$1", id, job.Job.ID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE mapping_operations SET validation_started_at=COALESCE(validation_started_at,now()),
 phase=CASE WHEN NOT EXISTS(SELECT 1 FROM mapping_candidates WHERE operation_id=$1 AND probe IS NULL) THEN 'publishing' ELSE phase END,
 validated_at=CASE WHEN NOT EXISTS(SELECT 1 FROM mapping_candidates WHERE operation_id=$1 AND probe IS NULL) THEN now() ELSE validated_at END,updated_at=now()
 WHERE id=$1`, operation)
	if err != nil {
		return err
	}
	return s.activateNativeMappingTx(ctx, tx, operation)
}

func (s *Service) activateNativeMappingTx(ctx context.Context, tx pgx.Tx, operation string) error {
	var ready bool
	if err := tx.QueryRow(ctx, "SELECT publication_mode='native' AND phase='publishing' FROM mapping_operations WHERE id=$1", operation).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return nil
	}
	return s.activateMappingTx(ctx, tx, operation, PublicationNative)
}

func (w *mappingFileWorker) Work(ctx context.Context, job *river.Job[mappingFileArgs]) error {
	if err := w.validate(ctx, job); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return w.s.failMapping(job.Args.Operation, job.ID, job.Attempt, job.MaxAttempts, err)
	}
	return nil
}

func (w *mappingFileWorker) validate(ctx context.Context, job *river.Job[mappingFileArgs]) error {
	s := w.s
	var m Media
	err := s.pool.QueryRow(ctx, `SELECT c.media_id::text,c.mapping_id::text,m.rd_account_id,m.torrent_id,m.info_hash,m.file_path,m.file_bytes,media.cinemeta_id
 FROM mapping_candidates c JOIN mapping_versions m ON m.id=c.mapping_id JOIN catalog_media media ON media.id=c.media_id JOIN mapping_operations o ON o.id=c.operation_id
 WHERE c.operation_id=$1 AND c.mapping_id=$2 AND c.validation_job_id=$3 AND c.probe IS NULL AND o.phase='validating'`, job.Args.Operation, job.Args.Mapping, job.ID).Scan(&m.ID, &m.MappingID, &m.AccountID, &m.TorrentID, &m.Hash, &m.Path, &m.Bytes, &m.IMDB)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	started := time.Now()
	probe, err := s.probe(ctx, m)
	elapsed := time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		err = validateProbe(probe, m.Bytes)
	}
	if err != nil {
		var diagnostic *probeFailure
		attrs := []any{"operation_id", job.Args.Operation, "media_id", m.ID, "episode_id", m.IMDB, "mapping_id", m.MappingID, "job_id", job.ID, "attempt", job.Attempt, "probe_ms", elapsed}
		if errors.As(err, &diagnostic) {
			attrs = append(attrs, "probe_details", diagnostic)
		}
		s.logger.Warn("mapping file validation failed", attrs...)
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var phase string
	if err = tx.QueryRow(ctx, "SELECT phase FROM mapping_operations WHERE id=$1 FOR UPDATE", job.Args.Operation).Scan(&phase); err != nil {
		return err
	}
	if phase != "validating" {
		return nil
	} // A failed/cancelled peer fences a late success.
	result, err := tx.Exec(ctx, `UPDATE mapping_candidates SET probe=$3,probe_ms=$4 WHERE mapping_id=$1 AND validation_job_id=$2 AND probe IS NULL`, m.MappingID, job.ID, probe, elapsed)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE mapping_operations SET phase='publishing',validated_at=now(),updated_at=now(),error_code=''
 WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM mapping_candidates WHERE operation_id=$1 AND probe IS NULL)`, job.Args.Operation)
	if err != nil {
		return err
	}
	if err = s.activateNativeMappingTx(ctx, tx, job.Args.Operation); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.logger.Info("mapping file metadata validated", "operation_id", job.Args.Operation, "media_id", m.ID, "attempt", job.Attempt, "probe_ms", elapsed)
	return nil
}

func (s *Service) failMapping(operation string, current int64, attempt, maxAttempts int, problem error) error {
	if errors.Is(problem, context.Canceled) {
		return problem
	}
	code := "source_unavailable"
	switch {
	case errors.Is(problem, ErrProbeInvalid):
		code = "probe_invalid"
	case errors.Is(problem, catalog.ErrChanged) || errors.Is(problem, realdebrid.ErrMismatch):
		code = "source_changed"
	case errors.Is(problem, catalog.ErrConflict):
		code = "mapping_conflict"
	case errors.Is(problem, accounts.ErrForbidden) || errors.Is(problem, accounts.ErrUnauthorized):
		code = "authorization_changed"
	}
	terminal := attempt >= maxAttempts || code != "source_unavailable"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var phase string
	if err = tx.QueryRow(ctx, "SELECT phase FROM mapping_operations WHERE id=$1 FOR UPDATE", operation).Scan(&phase); err != nil {
		return err
	}
	if phase != "queued" && phase != "validating" {
		return nil
	}
	if terminal {
		rows, e := tx.Query(ctx, "SELECT validation_job_id FROM mapping_candidates WHERE operation_id=$1 AND validation_job_id IS NOT NULL AND validation_job_id!=$2", operation, current)
		if e != nil {
			return e
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if err = s.cancelRetainedJob(ctx, tx, id); err != nil {
				return err
			}
		}
		phase = "failed"
	}
	if _, err = tx.Exec(ctx, "UPDATE mapping_operations SET phase=$2,error_code=$3,updated_at=now() WHERE id=$1", operation, phase, code); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.logger.Warn("mapping validation failed", "operation_id", operation, "code", code)
	var diagnostic *probeFailure
	failure := errors.New(code)
	if errors.As(problem, &diagnostic) {
		failure = fmt.Errorf("%s: %w", code, diagnostic)
	}
	if terminal {
		return river.JobCancel(failure)
	}
	return failure
}
