package catalog

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"time"
)

type metadataArgs struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (metadataArgs) Kind() string { return "catalog_metadata" }

type metadataDispatchArgs struct{}

func (metadataDispatchArgs) Kind() string { return "catalog_metadata_dispatch" }

type metadataReader interface {
	Movie(context.Context, string) (cinemeta.Movie, error)
	Series(context.Context, string) (cinemeta.Series, error)
}
type metadataWorker struct {
	river.WorkerDefaults[metadataArgs]
	store  *Store
	reader metadataReader
}

func (w *metadataWorker) Work(ctx context.Context, j *river.Job[metadataArgs]) error {
	var title cinemeta.Movie
	var err error
	if j.Args.Type == "movie" {
		title, err = w.reader.Movie(ctx, j.Args.ID)
	} else if j.Args.Type == "series" {
		var show cinemeta.Series
		show, err = w.reader.Series(ctx, j.Args.ID)
		title = show.Movie
	} else {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if title.ID != j.Args.ID || title.Type != j.Args.Type {
		return cinemeta.ErrIncomplete
	}
	return w.store.EnrichTitle(ctx, title)
}

type metadataDispatchWorker struct {
	river.WorkerDefaults[metadataDispatchArgs]
	pool *pgxpool.Pool
}

func (w *metadataDispatchWorker) Work(ctx context.Context, _ *river.Job[metadataDispatchArgs]) error {
	rows, err := w.pool.Query(ctx, `SELECT DISTINCT id,kind FROM (
 SELECT cinemeta_id AS id,'movie' AS kind FROM catalog_media WHERE kind='movie' AND COALESCE(metadata->>'year','')=''
 UNION ALL SELECT cinemeta_id,'series' FROM catalog_series WHERE COALESCE(metadata->>'year','')=''
 UNION ALL SELECT split_part(cinemeta_id,':',1),'series' FROM catalog_media WHERE kind='episode' AND metadata ? 'series' AND COALESCE(metadata->'series'->>'year','')=''
 ) titles`)
	if err != nil {
		return err
	}
	var titles []metadataArgs
	for rows.Next() {
		var a metadataArgs
		if err = rows.Scan(&a.ID, &a.Type); err != nil {
			rows.Close()
			return err
		}
		titles = append(titles, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var schema string
	if err = w.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return err
	}
	q, err := river.NewClient(riverpgxv5.New(w.pool), &river.Config{Schema: schema})
	if err != nil {
		return err
	}
	for _, a := range titles {
		if _, err = q.Insert(ctx, a, &river.InsertOpts{Queue: "catalog_metadata", MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 24 * time.Hour}}); err != nil {
			return err
		}
	}
	return New(w.pool).DispatchSchedules(ctx)
}
func RegisterMetadataWorkers(workers *river.Workers, pool *pgxpool.Pool) {
	worker := &metadataWorker{store: New(pool), reader: cinemeta.New()}
	river.AddWorker(workers, worker)
	river.AddWorker(workers, &scheduleWorker{store: New(pool), reader: cinemeta.New()})
	river.AddWorker(workers, &ratingWorker{metadata: worker})
	river.AddWorker(workers, &metadataDispatchWorker{pool: pool})
}
func MetadataPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return metadataDispatchArgs{}, &river.InsertOpts{Queue: "catalog_metadata", UniqueOpts: river.UniqueOpts{ByPeriod: 5 * time.Minute}}
	}, &river.PeriodicJobOpts{RunOnStart: true})
}
