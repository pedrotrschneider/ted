package stremio

import (
	"context"
	"entertainmentdistrict/internal/database"
)

// Repair exhausted/missing jobs and clear keys for revoked owners even when a
// copy was paused waiting for input. This never logs in or changes normal links.
func (s *Store) dispatchCopies(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT c.id::text FROM temporary_stremio_copies c JOIN users u ON u.id=c.user_id WHERE c.phase IN ('queued','resolving','copying','retrying') OR (c.phase='failed' AND (u.disabled OR u.role<>'admin'))`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		err = s.enqueue(ctx, tx, copyArgs{ID: id})
		if err == nil {
			err = tx.Commit(ctx)
		}
		database.Rollback(tx)
		if err != nil {
			return err
		}
	}
	return nil
}
