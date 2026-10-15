package stremio

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Remote interface {
	Login(context.Context, string, string) (Session, error)
	Snapshot(context.Context, string) ([]Record, error)
}
type Store struct {
	pool    *pgxpool.Pool
	remote  Remote
	catalog Catalog
}

func NewStore(pool *pgxpool.Pool, remote Remote, catalog Catalog) *Store {
	return &Store{pool, remote, catalog}
}
func (s *Store) Connect(ctx context.Context, who accounts.Identity, email, password string) error {
	session, err := s.remote.Login(ctx, email, password)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, false); err != nil {
		return err
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT account_id FROM stremio_links WHERE user_id=$1 AND retired_at IS NULL`, who.ID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current != "" && current != session.ID {
		return ErrChanged
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET email=EXCLUDED.email,auth_key=EXCLUDED.auth_key,updated_at=now()`, session.ID, session.Email, session.Key); err != nil {
		return err
	}
	if current == "" {
		if _, err = tx.Exec(ctx, `INSERT INTO stremio_links(user_id,account_id) VALUES($1,$2)`, who.ID, session.ID); err != nil {
			return err
		}
	}
	if err = s.enqueue(ctx, tx, dispatchArgs{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Disconnect(ctx context.Context, who accounts.Identity) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, false); err != nil {
		return err
	}
	var account string
	err = tx.QueryRow(ctx, `SELECT account_id FROM stremio_links WHERE user_id=$1 AND retired_at IS NULL`, who.ID).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE`, account); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stremio_links SET retired_at=now() WHERE user_id=$1 AND retired_at IS NULL`, who.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE stremio_accounts SET auth_key='' WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM stremio_links WHERE account_id=$1 AND retired_at IS NULL)`, account); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Status struct {
	Linked bool        `json:"linked"`
	Email  string      `json:"email"`
	Sync   *SyncStatus `json:"sync"`
}

func (s *Store) Status(ctx context.Context, who accounts.Identity) (Status, error) {
	out := Status{}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, false); err != nil {
		return out, err
	}
	var link string
	err = tx.QueryRow(ctx, `SELECT l.id::text,a.email FROM stremio_links l JOIN stremio_accounts a ON a.id=l.account_id WHERE l.user_id=$1 AND l.retired_at IS NULL`, who.ID).Scan(&link, &out.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, tx.Commit(ctx)
	}
	if err != nil {
		return out, err
	}
	out.Linked = true
	if out.Sync, err = syncStatus(ctx, tx, link); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
