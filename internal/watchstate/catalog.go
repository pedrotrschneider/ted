package watchstate

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
)

type CatalogEntry struct {
	ID       string
	Metadata []byte
}

// RememberCatalog records discovery identity only; it does not create a source,
// library entry, download or viewing event, or replace existing item metadata.
func (s *Store) RememberCatalog(ctx context.Context, who accounts.Identity, title, kind string, entries []CatalogEntry, regular int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = authorize(ctx, tx, who, false); err != nil {
		return err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, err = importIdentity(ctx, tx, e.ID, e.Metadata); err != nil {
			return err
		}
		ids = append(ids, e.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO watch_title_catalog(title_id,kind,entries,regular_episode_count) VALUES($1,$2,$3,$4) ON CONFLICT(title_id,kind) DO UPDATE SET entries=EXCLUDED.entries,regular_episode_count=EXCLUDED.regular_episode_count`, title, kind, raw, regular); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
