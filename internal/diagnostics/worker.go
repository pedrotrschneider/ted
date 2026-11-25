package diagnostics

import (
	"context"
	"github.com/riverqueue/river"
	"time"
)

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "client_diagnostics_cleanup" }

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	Store *Store
}

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	return w.Store.Cleanup(ctx)
}
func PeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
		return CleanupArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
	}, &river.PeriodicJobOpts{RunOnStart: true})
}
