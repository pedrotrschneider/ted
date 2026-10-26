package metadata

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"errors"
	"fmt"

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
	if r.Kind == "person" {
		if e = w.s.pool.QueryRow(ctx, `SELECT COALESCE(sum(revision),0)::bigint FROM metadata_series_providers`).Scan(&r.FilmographyRevision); e != nil {
			return e
		}
		r.FilmographyProviders = r.FilmographyRevision > 0
	}
	r.ProviderID = v.ProviderID
	if r.Kind == "season" || r.Kind == "episode" {
		var err error
		r.CanonicalEpisodes, r.ScopedCastSignature, err = castScope(ctx, w.s.pool, r)
		if err != nil {
			return err
		}
		if !r.SkipCast {
			updated, err := w.s.pool.Exec(ctx, `UPDATE metadata_entities SET request=request||jsonb_build_object('scoped_cast_signature',$3::text,'scoped_cast_requested',true) WHERE id=$1 AND generation=$2`, v.ID, v.Generation, r.ScopedCastSignature)
			if err != nil {
				return err
			}
			if updated.RowsAffected() == 0 {
				return nil
			}
		}
	}
	if v.Provider == "Tvdb" && r.Kind == "episode" && len(r.CanonicalEpisodes) == 1 && r.CanonicalEpisodes[0].TVDBID > 0 {
		r.ProviderID = fmt.Sprint(r.CanonicalEpisodes[0].TVDBID)
	}
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
	var chosen bool
	if fetchErr == nil && r.Kind == "series" {
		if e = w.s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_series_providers WHERE title_id=$1)`, r.TitleID).Scan(&chosen); e != nil {
			return e
		}
		if chosen {
			reader, ok := w.s.reader.(seriesStructureReader)
			if !ok {
				fetchErr = ErrUnconfigured
			} else {
				canonical, err := catalog.New(w.s.pool).CanonicalSeries(ctx, r.TitleID)
				if err != nil {
					return err
				}
				original := append([]cinemeta.Episode(nil), canonical.Episodes...)
				if err = w.s.addSeriesIdentityHints(ctx, &canonical); err != nil {
					return err
				}
				layout, err := reader.SeriesStructure(ctx, v.Provider, canonical, doc)
				if err != nil {
					fetchErr = err
				} else {
					doc.Structure = &layout
					doc.StructureCanonical = original
				}
			}
		}
	}
	// Do not publish a fresh title paired with an unverified provider structure.
	if r.Kind == "series" && fetchErr != nil && chosen {
		doc = Document{}
	}
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
