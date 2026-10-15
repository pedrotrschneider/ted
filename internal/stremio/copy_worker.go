package stremio

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

type copyArgs struct {
	ID string `json:"id"`
}

func (copyArgs) Kind() string { return "temporary_stremio_copy" }

type copyWorker struct {
	river.WorkerDefaults[copyArgs]
	store *Store
}

func (w *copyWorker) Work(ctx context.Context, j *river.Job[copyArgs]) error {
	for n := 0; n < 10; n++ {
		more, err := w.store.CopyStep(ctx, j.Args.ID)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
		var phase string
		if err = w.store.pool.QueryRow(ctx, `SELECT phase FROM temporary_stremio_copies WHERE id=$1`, j.Args.ID).Scan(&phase); err != nil {
			return err
		}
		if phase == "retrying" {
			return river.JobSnooze(3 * time.Second)
		}
	}
	return river.JobSnooze(10 * time.Millisecond)
}
