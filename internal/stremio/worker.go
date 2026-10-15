package stremio

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"time"
)

func RegisterWorkers(workers *river.Workers, pool *pgxpool.Pool) {
	river.AddWorker(workers, &copyWorker{store: NewStore(pool, New(), cinemeta.New())})
	river.AddWorker(workers, &outgoingWorker{store: NewStore(pool, New(), cinemeta.New())})
	river.AddWorker(workers, &pollWorker{store: NewStore(pool, New(), cinemeta.New())})
	river.AddWorker(workers, &deliveryWorker{store: NewStore(pool, New(), cinemeta.New())})
	river.AddWorker(workers, &dispatchWorker{store: NewStore(pool, New(), cinemeta.New())})
}
func failureCode(err error) string {
	switch {
	case errors.Is(err, ErrCredentials):
		return "credentials"
	case errors.Is(err, ErrChanged):
		return "changed"
	case errors.Is(err, ErrNotFound):
		return "missing"
	case errors.Is(err, ErrIncomplete):
		return "incomplete"
	case errors.Is(err, ErrAmbiguous), errors.Is(err, cinemeta.ErrNotFound):
		return "identity_review"
	default:
		return "unavailable"
	}
}

type pollWorker struct {
	river.WorkerDefaults[pollArgs]
	store *Store
}

func (w *pollWorker) Work(ctx context.Context, j *river.Job[pollArgs]) error {
	wait, err := w.store.Poll(ctx, j.Args.Account)
	if err != nil {
		return err
	}
	if wait > 0 {
		return river.JobSnooze(wait)
	}
	return nil
}

type deliveryWorker struct {
	river.WorkerDefaults[deliveryArgs]
	store *Store
}

func (w *deliveryWorker) Work(ctx context.Context, j *river.Job[deliveryArgs]) error {
	more, err := w.store.Deliver(ctx, j.Args)
	if err != nil {
		return err
	}
	if more {
		return river.JobSnooze(time.Second)
	}
	return nil
}

type dispatchWorker struct {
	river.WorkerDefaults[dispatchArgs]
	store *Store
}

func (w *dispatchWorker) Work(ctx context.Context, _ *river.Job[dispatchArgs]) error {
	return w.store.Dispatch(ctx)
}
