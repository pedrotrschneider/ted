package catalog

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
)

// EnrichTitle fills absent descriptive fields only. It never creates library items
// or changes mappings, watch state, provider recovery data, or populated fields.
func (s *Store) EnrichTitle(ctx context.Context, title cinemeta.Movie) error {
	if !cinemeta.ValidID(title.ID) || (title.Type != "movie" && title.Type != "series") {
		return ErrInvalid
	}
	raw, err := json.Marshal(title)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	// Evaluate missing fields against the locked/current row, not an earlier read.
	patch := func(column string) string {
		return `COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each($2::jsonb) WHERE key IN ('name','year','description','poster','genres') AND value NOT IN ('null'::jsonb,'""'::jsonb,'[]'::jsonb) AND COALESCE(` + column + `->key,'null'::jsonb) IN ('null'::jsonb,'""'::jsonb,'[]'::jsonb)),'{}'::jsonb)`
	}
	if title.Type == "movie" {
		_, err = tx.Exec(ctx, `UPDATE catalog_media SET metadata=metadata || `+patch("metadata")+` WHERE cinemeta_id=$1 AND kind='movie'`, title.ID, raw)
	} else {
		_, err = tx.Exec(ctx, `UPDATE catalog_series SET metadata=metadata || `+patch("metadata")+` WHERE cinemeta_id=$1`, title.ID, raw)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE catalog_media SET metadata=jsonb_set(metadata,'{series}',(metadata->'series') || `+patch("(metadata->'series')")+`) WHERE kind='episode' AND split_part(cinemeta_id,':',1)=$1 AND jsonb_typeof(metadata->'series')='object'`, title.ID, raw)
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_ratings(title_id,kind,rating,checked_at) VALUES($1,$2,$3,now()) ON CONFLICT(title_id,kind) DO UPDATE SET rating=EXCLUDED.rating,checked_at=EXCLUDED.checked_at`, title.ID, title.Type, title.IMDbRating); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
