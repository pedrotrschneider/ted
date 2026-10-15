package streaming

import (
	"context"
	"entertainmentdistrict/internal/database"
	"errors"
	"github.com/riverqueue/river"
	"os"
	"time"
)

type orphanArgs struct{}

func (orphanArgs) Kind() string { return "orphan_cleanup" }

type orphanWorker struct {
	river.WorkerDefaults[orphanArgs]
	s *Service
}

func (w *orphanWorker) Work(ctx context.Context, job *river.Job[orphanArgs]) error {
	s := w.s
	if s.storage == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT media_id::text,generation FROM orphan_files ORDER BY created_at LIMIT 100`)
	if err != nil {
		return err
	}
	type orphan struct {
		id         string
		generation int64
	}
	items := []orphan{}
	for rows.Next() {
		var item orphan
		if err = rows.Scan(&item.id, &item.generation); err != nil {
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
		if err = s.cleanOrphan(ctx, item.id, item.generation); err != nil {
			return err
		}
	}
	if len(items) == 100 {
		return river.JobSnooze(time.Second)
	}
	return nil
}
func (s *Service) cleanOrphan(ctx context.Context, id string, generation int64) error {
	// Coordinate with stale writers and removers across processes. Active readers
	// can delay cleanup; remapping itself does not wait for them.
	_, release, err := s.fileLock(ctx, id+":download", false)
	if err != nil {
		return err
	}
	defer release()
	conn, releaseRead, err := s.fileLock(ctx, id, false)
	if err != nil {
		return err
	}
	defer releaseRead()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orphan_files WHERE media_id=$1 AND generation=$2)`, id, generation).Scan(&owned); err != nil || !owned {
		return err
	}
	// Lock the media row used by Download/activation, preventing new associations
	// between the reference check and unlink. Generation names are never reused.
	if _, err = tx.Exec(ctx, `SELECT id FROM catalog_media WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	var referenced bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_files WHERE movie_id=$1 AND generation=$2 AND (available OR phase!='absent'))`, id, generation).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return ErrLocalBusy
	}
	stage, final := localNames(id, generation)
	for _, name := range []string{stage, final} {
		if err = s.storage.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrStorage
		}
	}
	if err = s.syncDir(id); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStorage
	}
	if _, err = tx.Exec(ctx, `DELETE FROM orphan_files WHERE media_id=$1 AND generation=$2`, id, generation); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
