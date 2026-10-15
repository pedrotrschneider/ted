package streaming

import (
	"context"
	"errors"

	"entertainmentdistrict/internal/database"

	"github.com/jackc/pgx/v5"
)

const (
	PublicationNative = "native"
)

var ErrLifecycle = errors.New("library publication mode changed or other-mode work remains pending; finish pending work in its original mode before switching")

// Select the process's mode before starting workers. The singleton row serializes
// transitions with submission/removal transactions in other running processes.
func (s *Service) configureLifecycle(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "INSERT INTO library_lifecycle(publication_mode) VALUES($1) ON CONFLICT DO NOTHING", s.publicationMode); err != nil {
		return err
	}
	var current string
	if err = tx.QueryRow(ctx, "SELECT publication_mode FROM library_lifecycle FOR UPDATE").Scan(&current); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mapping_operations WHERE publication_mode!=$1 AND phase NOT IN ('complete','failed'))
 OR EXISTS(SELECT 1 FROM library_removals WHERE publication_mode!=$1 AND NOT retired AND phase!='complete')`, s.publicationMode).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrLifecycle
	}
	if _, err = tx.Exec(ctx, "UPDATE library_lifecycle SET publication_mode=$1", s.publicationMode); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Hold until commit: an old process cannot submit new work after another process
// has selected a different mode. Call before taking title/operation locks.
func (s *Service) lockLifecycle(ctx context.Context, tx pgx.Tx) error {
	var current string
	if err := tx.QueryRow(ctx, "SELECT publication_mode FROM library_lifecycle FOR SHARE").Scan(&current); err != nil {
		return err
	}
	if current != s.publicationMode {
		return ErrLifecycle
	}
	return nil
}

func (s *Service) PublicationMode() string { return s.publicationMode }
