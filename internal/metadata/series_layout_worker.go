package metadata

import (
	"context"

	"entertainmentdistrict/internal/catalog"
	"github.com/riverqueue/river"
)

type seriesLayoutWorker struct {
	river.WorkerDefaults[catalog.SeriesLayoutRefreshArgs]
	s *Service
}

func (w *seriesLayoutWorker) Work(ctx context.Context, job *river.Job[catalog.SeriesLayoutRefreshArgs]) error {
	if !titleID.MatchString(job.Args.Title) {
		return river.JobCancel(ErrInvalid)
	}
	// A schedule refresh only needs the root and its identity index. Child
	// descriptions, artwork and cast remain owned by explicit screen access.
	tx, err := w.s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	provider, err := lockSeriesProvider(ctx, tx, job.Args.Title)
	if err != nil {
		return err
	}
	enabled, err := w.s.Configured(ctx, provider)
	if err != nil {
		return err
	}
	if err = w.s.queueEntity(ctx, tx, guid("series:"+job.Args.Title), true, enabled); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
