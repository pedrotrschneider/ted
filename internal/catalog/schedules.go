package catalog

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"fmt"
	"time"
)

type ReleasedCount struct {
	Watched, Available, Added int
	Known                     bool
}

// SaveSchedule replaces only a successfully validated schedule. An older
// in-flight fetch cannot overwrite a newer detail or background observation.
func (s *Store) SaveSchedule(ctx context.Context, show cinemeta.Series, started time.Time) error {
	if !cinemeta.ValidID(show.ID) || show.Type != "series" || show.Episodes == nil {
		return cinemeta.ErrIncomplete
	}
	seen := map[string]bool{}
	for _, e := range show.Episodes {
		if e.Season < 0 || e.Number < 0 || e.ID != fmt.Sprintf("%s:%d:%d", show.ID, e.Season, e.Number) || seen[e.ID] {
			return cinemeta.ErrIncomplete
		}
		seen[e.ID] = true
		if e.ReleaseDate != "" {
			if _, err := time.Parse(time.DateOnly, e.ReleaseDate); err != nil {
				return cinemeta.ErrIncomplete
			}
		}
	}
	raw, err := json.Marshal(show.Episodes)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = saveNativeTitleTx(ctx, tx, show.Movie, started); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('metadata:series:'||$1,0))`, show.ID); err != nil {
		return err
	}
	if err = s.queueLayoutRefresh(ctx, tx, show.ID, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_episode_schedules(title_id,episodes,canonical_episodes,identity_version,observed_at,fetched_at) VALUES($1,COALESCE((SELECT episodes FROM metadata_series_layouts WHERE title_id=$1),$2),$2,1,$3,now()) ON CONFLICT(title_id) DO UPDATE SET episodes=EXCLUDED.episodes,canonical_episodes=EXCLUDED.canonical_episodes,identity_version=1,observed_at=EXCLUDED.observed_at,fetched_at=EXCLUDED.fetched_at WHERE catalog_episode_schedules.observed_at<EXCLUDED.observed_at`, show.ID, raw, started); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ReleasedCounts(ctx context.Context, ids []string, user int64, today string) (map[string]ReleasedCount, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id,count(e.id) FILTER(WHERE COALESCE((wc.state->>'played')::bool,false))::int,count(e.id)::int,
 count(e.id) FILTER(WHERE EXISTS(SELECT 1 FROM streaming_items si JOIN media_mappings mm ON mm.id=si.mapping_id WHERE si.movie_id=c.id AND si.published))::int,
 bool_or(sh.title_id IS NOT NULL)
 FROM unnest($1::text[]) t(id)
 LEFT JOIN catalog_episode_schedules sh ON sh.title_id=t.id
 LEFT JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(id text,season int,release_date date) ON e.season>0 AND e.release_date<=$3::date
 LEFT JOIN catalog_media c ON c.cinemeta_id=e.id AND c.kind='episode'
 LEFT JOIN watch_current wc ON wc.media_id=c.id AND wc.user_id=$2
 GROUP BY t.id`, ids, user, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]ReleasedCount{}
	for rows.Next() {
		var id string
		var v ReleasedCount
		if err = rows.Scan(&id, &v.Watched, &v.Available, &v.Added, &v.Known); err != nil {
			return nil, err
		}
		result[id] = v
	}
	return result, rows.Err()
}
