package catalog

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// SeriesLayoutRefreshArgs is the durable notification from the canonical
// schedule owner to the provider metadata owner. It carries no provider secret.
type SeriesLayoutRefreshArgs struct {
	Title string `json:"title"`
}

func (SeriesLayoutRefreshArgs) Kind() string { return "metadata_series_structure_refresh" }

func (s *Store) queueLayoutRefresh(ctx context.Context, tx pgx.Tx, title string, canonical []byte) error {
	var needed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_series_providers p JOIN metadata_entities e ON e.kind='series' AND e.request->>'title_id'=p.title_id
 LEFT JOIN catalog_episode_schedules sh ON sh.title_id=p.title_id
 WHERE p.title_id=$1 AND e.status NOT IN ('new','queued') AND (
 e.fetched_at<now()-interval '24 hours' OR
 (SELECT jsonb_agg(jsonb_build_array(ep->>'id',ep->>'tvdb_id',ep->>'tmdb_id') ORDER BY ep->>'id') FROM jsonb_array_elements(COALESCE(sh.canonical_episodes,sh.episodes)) ep)
 IS DISTINCT FROM
 (SELECT jsonb_agg(jsonb_build_array(ep->>'id',ep->>'tvdb_id',ep->>'tmdb_id') ORDER BY ep->>'id') FROM jsonb_array_elements($2::jsonb) ep)))`, title, canonical).Scan(&needed)
	if err != nil || !needed {
		return err
	}
	var schema string
	if err = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema})
	if err != nil {
		return err
	}
	_, err = q.InsertTx(ctx, tx, SeriesLayoutRefreshArgs{Title: title}, &river.InsertOpts{Queue: "native_metadata", MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}})
	return err
}
