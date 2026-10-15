package streaming

import (
	"context"
	"encoding/json"
	"errors"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ConfigureMappingBatchSize is called before workers and HTTP start. Each
// submission snapshots its size so later configuration changes cannot repartition it.
func (s *Service) ConfigureMappingBatchSize(size int) error {
	if size < 1 || size > 500 {
		return ErrInvalid
	}
	s.mappingBatchSize = size
	return nil
}

func (s *Service) insertMappingBatch(ctx context.Context, tx pgx.Tx, submission string, number int, first bool) error {
	var title, kind, mode string
	var actor json.RawMessage
	var request catalog.SeriesSelection
	var size int
	err := tx.QueryRow(ctx, "SELECT title_id,kind,actor,request,batch_size,publication_mode FROM mapping_submissions WHERE id=$1", submission).Scan(&title, &kind, &actor, &request, &size, &mode)
	if err != nil {
		return err
	}
	start := (number - 1) * size
	if number < 1 || start >= len(request.Files) {
		return ErrInvalid
	}
	request.Files = request.Files[start:min(start+size, len(request.Files))]
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO mapping_operations(id,title_id,kind,actor,request,submission_id,batch_number,publication_mode)
 VALUES(CASE WHEN $7 THEN $5::uuid ELSE gen_random_uuid() END,$1,$2,$3,$4,$5,$6,$8) RETURNING id::text`, title, kind, actor, raw, submission, number, first, mode).Scan(&id)
	if err != nil {
		return err
	}
	job, err := s.queue.InsertTx(ctx, tx, mappingArgs{id}, &river.InsertOpts{MaxAttempts: 3, Queue: "mapping_control"})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE mapping_operations SET job_id=$2 WHERE id=$1", id, job.Job.ID)
	return err
}

// Completion and successor insertion share the activation transaction. A crash
// cannot leave a completed batch with its next batch missing, and a replayed
// acknowledgment returns before reaching this function.
func (s *Service) advanceMappingSubmission(ctx context.Context, tx pgx.Tx, operation string) error {
	var submission *string
	var number, size, total int
	err := tx.QueryRow(ctx, `SELECT o.submission_id::text,o.batch_number,COALESCE(sub.batch_size,0),COALESCE(jsonb_array_length(sub.request->'files'),0)
 FROM mapping_operations o LEFT JOIN mapping_submissions sub ON sub.id=o.submission_id WHERE o.id=$1`, operation).Scan(&submission, &number, &size, &total)
	if err != nil {
		return err
	}
	if submission == nil || number*size >= total {
		return nil
	}
	return s.insertMappingBatch(ctx, tx, *submission, number+1, false)
}

func (s *Service) MappingStatus(ctx context.Context, id, kind string) (MappingOperation, error) {
	op := MappingOperation{PublicationMode: s.publicationMode}
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(o.submission_id,o.id)::text,o.phase,o.error_code,
 jsonb_array_length(COALESCE(sub.request,o.request)->'files'),
 COALESCE(done.files,0)+(SELECT count(*) FROM mapping_candidates c WHERE c.operation_id=o.id AND c.probe IS NOT NULL),
 o.batch_number,CASE WHEN sub.id IS NULL THEN 1 ELSE (jsonb_array_length(sub.request->'files')+sub.batch_size-1)/sub.batch_size END,
 COALESCE(done.batches,0)+CASE WHEN o.phase='complete' THEN 1 ELSE 0 END,
 COALESCE(done.files,0)+CASE WHEN o.phase='complete' THEN jsonb_array_length(o.request->'files') ELSE 0 END,
 o.submission_id IS NOT NULL AND o.phase='failed' AND o.publication_mode=$3 AND NOT EXISTS(SELECT 1 FROM library_removals r WHERE r.title_id=o.title_id AND r.kind=o.kind AND NOT r.retired),o.publication_mode
 FROM mapping_operations o LEFT JOIN mapping_submissions sub ON sub.id=o.submission_id
 LEFT JOIN LATERAL (SELECT count(*) AS batches,sum(jsonb_array_length(prev.request->'files')) AS files
 FROM mapping_operations prev WHERE prev.submission_id=o.submission_id AND prev.phase='complete' AND prev.id!=o.id) done ON true
 WHERE o.title_id=$1 AND o.kind=$2 ORDER BY o.sequence DESC LIMIT 1`, id, kind, s.publicationMode).Scan(
		&op.ID, &op.Phase, &op.Error, &op.Total, &op.Validated, &op.Batch, &op.Batches, &op.CompletedBatches, &op.Committed, &op.Retryable, &op.PublicationMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return op, nil
	}
	return op, err
}

// Retrying creates a fresh batch operation, leaving failed candidates and job IDs
// fenced forever. Completed batches are never restaged or reprobed.
func (s *Service) RetryMapping(ctx context.Context, who accounts.Identity, title, kind, submission string) (MappingOperation, error) {
	var result MappingOperation
	if !uuidPattern.MatchString(submission) {
		return result, catalog.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return result, err
	}
	if err = s.lockLifecycle(ctx, tx); err != nil {
		return result, err
	}
	if err = titleLock(ctx, tx, title); err != nil {
		return result, err
	}
	var current *string
	var phase, mode string
	var number int
	err = tx.QueryRow(ctx, `SELECT submission_id::text,phase,batch_number,publication_mode FROM mapping_operations WHERE title_id=$1 AND kind=$2 ORDER BY sequence DESC LIMIT 1 FOR UPDATE`, title, kind).Scan(&current, &phase, &number, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, catalog.ErrInvalid
	}
	if err != nil {
		return result, err
	}
	if current == nil || *current != submission {
		return result, catalog.ErrConflict
	}
	if mode != s.publicationMode {
		return result, ErrLifecycle
	}
	var removing bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_removals WHERE title_id=$1 AND kind=$2 AND NOT retired)", title, kind).Scan(&removing); err != nil {
		return result, err
	}
	if removing {
		return result, catalog.ErrConflict
	}
	if phase == "failed" {
		actor, _ := json.Marshal(who)
		if _, err = tx.Exec(ctx, "UPDATE mapping_submissions SET actor=$2 WHERE id=$1", submission, actor); err != nil {
			return result, err
		}
		if err = s.insertMappingBatch(ctx, tx, submission, number, false); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return s.MappingStatus(ctx, title, kind)
}
