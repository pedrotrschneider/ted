package catalog

import (
	"context"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"time"
)

type Rating struct {
	ID      string   `json:"id"`
	Value   *float64 `json:"rating"`
	Pending bool     `json:"pending"`
}
type ratingArgs struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (ratingArgs) Kind() string { return "catalog_rating" }

type ratingWorker struct {
	river.WorkerDefaults[ratingArgs]
	metadata *metadataWorker
}

func (w *ratingWorker) Work(ctx context.Context, j *river.Job[ratingArgs]) error {
	return w.metadata.Work(ctx, &river.Job[metadataArgs]{Args: metadataArgs{ID: j.Args.ID, Type: j.Args.Type}})
}

// Ratings serves cached values while durable jobs refresh absent/weekly-stale
// entries. A successful unrated lookup is cached too; failures keep old scores.
func (s *Store) Ratings(ctx context.Context, kind string, ids []string, enqueue bool) ([]Rating, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id,r.rating,COALESCE(r.checked_at<now()-interval '7 days',true) FROM unnest($1::text[]) WITH ORDINALITY t(id,n) LEFT JOIN catalog_ratings r ON r.title_id=t.id AND r.kind=$2 ORDER BY t.n`, ids, kind)
	if err != nil {
		return nil, err
	}
	result := []Rating{}
	for rows.Next() {
		var r Rating
		if err = rows.Scan(&r.ID, &r.Value, &r.Pending); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !enqueue {
		return result, nil
	}
	var schema string
	if err = s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return nil, err
	}
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema})
	if err != nil {
		return nil, err
	}
	for _, r := range result {
		if r.Pending {
			if _, err = q.Insert(ctx, ratingArgs{ID: r.ID, Type: kind}, &river.InsertOpts{Queue: "catalog_metadata", MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 24 * time.Hour}}); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
