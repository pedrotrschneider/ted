package metadata

import (
	"context"
	"errors"

	"github.com/riverqueue/river"
)

type worker struct {
	river.WorkerDefaults[Args]
	s *Service
}

func (w *worker) Work(ctx context.Context, job *river.Job[Args]) error {
	v, e := w.s.readEntity(ctx, job.Args.ID)
	if e != nil {
		return e
	}
	if v.Generation != job.Args.Generation || v.Status == "ready" && v.AppliedGeneration == v.Generation {
		return nil
	}
	r := v.Request
	r.ProviderID = v.ProviderID
	if r.Kind == "season" || r.Kind == "episode" {
		if r.ParentProviderID == "" {
			root, e := w.s.readEntity(ctx, guid("series:"+r.TitleID))
			if e != nil {
				return e
			}
			r.ParentProviderID = root.ProviderID
		}
	}
	doc, fetchErr := w.s.reader.Fetch(ctx, v.Provider, r)
	if fetchErr == nil && (doc.Item.Name == "" || doc.ProviderID == "") {
		fetchErr = ErrPartial
	}
	if doc.Item.Name != "" && doc.ProviderID != "" {
		if e = w.s.apply(ctx, v, doc, fetchErr); e != nil {
			if ctx.Err() == nil {
				if failure := w.s.failure(ctx, v.ID, v.Generation, e); failure != nil {
					return failure
				}
			}
			return e
		}
	} else if fetchErr != nil {
		if e = w.s.failure(ctx, v.ID, v.Generation, fetchErr); e != nil {
			return e
		}
	}
	if fetchErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(fetchErr, ErrMissing) || errors.Is(fetchErr, ErrAmbiguous) || errors.Is(fetchErr, ErrInvalid) || errors.Is(fetchErr, ErrUnconfigured) {
			return river.JobCancel(fetchErr)
		}
		var rejected unauthorized
		if errors.As(fetchErr, &rejected) {
			return river.JobCancel(fetchErr)
		}
		return fetchErr
	}
	return nil
}
