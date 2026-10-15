package catalog

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"time"
)

type scheduleArgs struct {
	ID string `json:"id"`
}

func (scheduleArgs) Kind() string { return "catalog_schedule" }

type scheduleWorker struct {
	river.WorkerDefaults[scheduleArgs]
	store  *Store
	reader metadataReader
}

func (w *scheduleWorker) Work(ctx context.Context, j *river.Job[scheduleArgs]) error {
	started := time.Now()
	show, err := w.reader.Series(ctx, j.Args.ID)
	if err != nil {
		return err
	}
	if show.ID != j.Args.ID {
		return cinemeta.ErrIncomplete
	}
	return w.store.SaveSchedule(ctx, show, started)
}
func (s *Store) DispatchSchedules(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `WITH titles AS (
 SELECT title_id FROM watch_title_catalog WHERE kind='series'
 UNION SELECT cinemeta_id FROM catalog_series
 UNION SELECT split_part(cinemeta_id,':',1) FROM catalog_media WHERE kind='episode'
 UNION SELECT title_id FROM catalog_episode_schedules)
 SELECT t.title_id FROM titles t LEFT JOIN catalog_episode_schedules sh USING(title_id) WHERE sh.fetched_at IS NULL OR sh.fetched_at<=now()-interval '24 hours'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var schema string
	if err = s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return err
	}
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = q.Insert(ctx, scheduleArgs{ID: id}, &river.InsertOpts{Queue: "catalog_metadata", MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}); err != nil {
			return err
		}
	}
	return nil
}
