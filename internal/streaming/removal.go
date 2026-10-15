package streaming

import (
	"context"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type Removal struct {
	PublicationMode string `json:"publication_mode"`
	ID              string `json:"id"`
	Phase           string `json:"phase"`
	Error           string `json:"error"`
}
type RemovalItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}
type removalArgs struct {
	ID string `json:"id"`
}

func (removalArgs) Kind() string { return "library_removal" }

type removalWorker struct {
	river.WorkerDefaults[removalArgs]
	s *Service
}

func titleLock(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,84051009))", id)
	return err
}
func (s *Service) RemovalStatus(ctx context.Context, id, kind string) (Removal, error) {
	result := Removal{PublicationMode: s.publicationMode}
	err := s.pool.QueryRow(ctx, "SELECT id::text,phase,error_code,publication_mode FROM library_removals WHERE title_id=$1 AND kind=$2 AND NOT retired", id, kind).Scan(&result.ID, &result.Phase, &result.Error, &result.PublicationMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	return result, err
}
func (s *Service) RemoveTitle(ctx context.Context, who accounts.Identity, id, kind string) (Removal, error) {
	if !cinemeta.ValidID(id) || (kind != "movie" && kind != "series") {
		return Removal{}, catalog.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Removal{}, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return Removal{}, err
	}
	if err = s.lockLifecycle(ctx, tx); err != nil {
		return Removal{}, err
	}
	if err = titleLock(ctx, tx, id); err != nil {
		return Removal{}, err
	}
	var existing Removal
	err = tx.QueryRow(ctx, "SELECT id::text,phase,error_code,publication_mode FROM library_removals WHERE title_id=$1 AND kind=$2 AND NOT retired", id, kind).Scan(&existing.ID, &existing.Phase, &existing.Error, &existing.PublicationMode)
	if err == nil {
		if existing.Phase != "complete" {
			var running bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM river_job j JOIN library_removals r ON r.job_id=j.id WHERE r.id=$1 AND j.state NOT IN ('completed','cancelled','discarded'))", existing.ID).Scan(&running); err != nil {
				return Removal{}, err
			}
			if !running {
				job, e := s.queue.InsertTx(ctx, tx, removalArgs{existing.ID}, &river.InsertOpts{MaxAttempts: 10})
				if e != nil {
					return Removal{}, e
				}
				if _, err = tx.Exec(ctx, "UPDATE library_removals SET job_id=$2,error_code='' WHERE id=$1", existing.ID, job.Job.ID); err != nil {
					return Removal{}, err
				}
			}
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Removal{}, err
	}
	var pending bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mapping_operations WHERE title_id=$1 AND kind=$2 AND phase NOT IN ('complete','failed'))", id, kind).Scan(&pending); err != nil {
		return Removal{}, err
	}
	if pending {
		return Removal{}, catalog.ErrConflict
	}
	var root string
	query := "SELECT id::text FROM catalog_media WHERE cinemeta_id=$1 AND kind='movie' AND EXISTS(SELECT 1 FROM media_mappings WHERE movie_id=catalog_media.id)"
	if kind == "series" {
		query = "SELECT id::text FROM catalog_series WHERE cinemeta_id=$1 AND EXISTS(SELECT 1 FROM catalog_seasons se JOIN catalog_episodes e ON e.season_id=se.id JOIN media_mappings m ON m.movie_id=e.media_id WHERE se.series_id=catalog_series.id)"
	}
	if err = tx.QueryRow(ctx, query, id).Scan(&root); errors.Is(err, pgx.ErrNoRows) {
		return Removal{}, ErrNotReady
	} else if err != nil {
		return Removal{}, err
	}
	var op string
	if err = tx.QueryRow(ctx, "INSERT INTO library_removals(title_id,kind,publication_mode) VALUES($1,$2,$3) RETURNING id::text", id, kind, s.publicationMode).Scan(&op); err != nil {
		return Removal{}, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO library_removal_items VALUES($1,$2,$3)", op, root, kind); err != nil {
		return Removal{}, err
	}
	if kind == "series" {
		if _, err = tx.Exec(ctx, `INSERT INTO library_removal_items SELECT $1::uuid,id,'season' FROM catalog_seasons WHERE series_id=$2 UNION ALL SELECT $1::uuid,e.media_id,'episode' FROM catalog_episodes e JOIN catalog_seasons se ON se.id=e.season_id WHERE se.series_id=$2`, op, root); err != nil {
			return Removal{}, err
		}
	}
	job, err := s.queue.InsertTx(ctx, tx, removalArgs{op}, &river.InsertOpts{MaxAttempts: 10})
	if err != nil {
		return Removal{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE library_removals SET job_id=$2 WHERE id=$1", op, job.Job.ID); err != nil {
		return Removal{}, err
	}
	if err = s.detachRemovalTx(ctx, tx, op); err != nil {
		return Removal{}, err
	}
	return Removal{ID: op, Phase: "removing", PublicationMode: s.publicationMode}, tx.Commit(ctx)
}

// TED state is already durable; removal only detaches owned library resources.
func (s *Service) detachRemovalTx(ctx context.Context, tx pgx.Tx, id string) error {
	rows, err := tx.Query(ctx, "SELECT item_id::text,kind FROM library_removal_items WHERE removal_id=$1 ORDER BY item_id", id)
	if err != nil {
		return err
	}
	items := []RemovalItem{}
	for rows.Next() {
		var item RemovalItem
		if err = rows.Scan(&item.ID, &item.Kind); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind != "movie" && item.Kind != "episode" {
			continue
		}
		if _, err = tx.Exec(ctx, "SELECT id FROM catalog_media WHERE id=$1 FOR UPDATE", item.ID); err != nil {
			return err
		}
		var oldJob *int64
		err = tx.QueryRow(ctx, "SELECT job_id FROM streaming_items WHERE movie_id=$1 FOR UPDATE", item.ID).Scan(&oldJob)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if oldJob != nil {
			if err = s.cancelRetainedJob(ctx, tx, *oldJob); err != nil {
				return err
			}
		}
		local, e := localState(ctx, tx, item.ID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil {
			if err = s.cancelRetainedJob(ctx, tx, local.JobID); err != nil {
				return err
			}
			if local.Generation > 0 {
				if _, err = tx.Exec(ctx, "INSERT INTO orphan_files(media_id,generation) VALUES($1,$2) ON CONFLICT DO NOTHING", item.ID, local.Generation); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, "UPDATE local_files SET phase='absent',available=false,job_id=0,completed_bytes=0,validated_bytes=0,transfer_bytes_per_second=0,transfer_sampled_at=NULL,error_code='' WHERE movie_id=$1", item.ID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "DELETE FROM local_chunks WHERE movie_id=$1", item.ID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE streaming_items SET published=false,phase='cancelled',job_id=NULL,error_code='' WHERE movie_id=$1", item.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE mapping_versions SET active=false WHERE movie_id=$1", item.ID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "UPDATE library_removals SET phase='removing',error_code='' WHERE id=$1", id)
	return err
}
func (s *Service) cancelRetainedJob(ctx context.Context, tx pgx.Tx, id int64) error {
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM river_job WHERE id=$1)", id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := s.queue.JobCancelTx(ctx, tx, id)
	return err
}
func (w *removalWorker) Work(ctx context.Context, job *river.Job[removalArgs]) error {
	var phase string
	if err := w.s.pool.QueryRow(ctx, "SELECT phase FROM library_removals WHERE id=$1", job.Args.ID).Scan(&phase); err != nil {
		return err
	}
	if phase == "complete" {
		return nil
	}
	rows, err := w.s.pool.Query(ctx, "SELECT o.media_id::text,o.generation FROM orphan_files o JOIN library_removal_items i ON i.item_id=o.media_id WHERE i.removal_id=$1 ORDER BY o.media_id,o.generation", job.Args.ID)
	if err != nil {
		return err
	}
	type file struct {
		id         string
		generation int64
	}
	files := []file{}
	for rows.Next() {
		var f file
		if err = rows.Scan(&f.id, &f.generation); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, f := range files {
		if w.s.storage == nil {
			err = ErrStorage
		} else {
			err = w.s.cleanOrphan(ctx, f.id, f.generation)
		}
		if err != nil {
			code := "cleanup_failed"
			if errors.Is(err, ErrLocalBusy) {
				code = "waiting_for_transfer"
			}
			if _, e := w.s.pool.Exec(ctx, "UPDATE library_removals SET error_code=$2 WHERE id=$1 AND error_code!=$2", job.Args.ID, code); e != nil {
				return e
			}
			return river.JobSnooze(15 * time.Second)
		}
	}
	_, err = w.s.pool.Exec(ctx, "UPDATE library_removals SET phase='complete',error_code='' WHERE id=$1", job.Args.ID)
	return err
}
