package catalog

import (
	"context"
	"encoding/json"
	"time"

	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
)

// SaveNativeTitle caches a successful detail read without creating a catalog
// media identity, shared membership, source mapping or personal watch state.
func (s *Store) SaveNativeTitle(ctx context.Context, title cinemeta.Movie, started time.Time) error {
	if !cinemeta.ValidID(title.ID) || (title.Type != "movie" && title.Type != "series") {
		return cinemeta.ErrIncomplete
	}
	raw, err := json.Marshal(title)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, nativeMetadataSQL, title.Type, title.ID, raw, started)
	return err
}

const nativeMetadataSQL = `INSERT INTO native_catalog_titles(kind,title_id,metadata,observed_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(kind,title_id) DO UPDATE SET metadata=EXCLUDED.metadata,observed_at=EXCLUDED.observed_at
 WHERE native_catalog_titles.observed_at<EXCLUDED.observed_at`

func (s *Store) NativeTitle(ctx context.Context, kind, id string) (cinemeta.Movie, error) {
	var title cinemeta.Movie
	err := s.pool.QueryRow(ctx, `SELECT metadata FROM native_catalog_titles WHERE kind=$1 AND title_id=$2`, kind, id).Scan(&title)
	return title, err
}

func (s *Store) NativeSeries(ctx context.Context, id string) (cinemeta.Series, error) {
	var show cinemeta.Series
	// One statement gives metadata and schedule from the same snapshot.
	err := s.pool.QueryRow(ctx, `SELECT n.metadata,sh.episodes,sh.identity_version FROM native_catalog_titles n JOIN catalog_episode_schedules sh ON sh.title_id=n.title_id WHERE n.kind='series' AND n.title_id=$1`, id).Scan(&show.Movie, &show.Episodes, &show.IdentityVersion)
	return show, err
}

// CanonicalSeries retains source identities independently of provider ordering.
func (s *Store) CanonicalSeries(ctx context.Context, id string) (cinemeta.Series, error) {
	var series cinemeta.Series
	err := s.pool.QueryRow(ctx, `SELECT n.metadata,COALESCE(sh.canonical_episodes,sh.episodes) FROM native_catalog_titles n JOIN catalog_episode_schedules sh ON sh.title_id=n.title_id WHERE n.kind='series' AND n.title_id=$1`, id).Scan(&series.Movie, &series.Episodes)
	return series, err
}

func saveNativeTitleTx(ctx context.Context, tx pgx.Tx, title cinemeta.Movie, started time.Time) error {
	raw, err := json.Marshal(title)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, nativeMetadataSQL, title.Type, title.ID, raw, started)
	return err
}
