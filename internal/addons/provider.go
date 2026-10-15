package addons

import (
	"context"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/stremio"
	"github.com/jackc/pgx/v5"
)

type ProviderStatus struct {
	Connected bool       `json:"connected"`
	Email     string     `json:"email,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func (s *Service) ProviderStatus(ctx context.Context) (ProviderStatus, error) {
	var v ProviderStatus
	err := s.pool.QueryRow(ctx, `SELECT true,email,updated_at FROM server_addon_accounts WHERE active`).Scan(&v.Connected, &v.Email, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	return v, err
}

// ConfigureProvider changes discovery only. It never creates a personal link or
// enrolls this connection in watch-history polling. Old source provenance stays.
func (s *Service) ConfigureProvider(ctx context.Context, actor accounts.Identity, session *stremio.Session) error {
	if session != nil && (session.ID == "" || session.Key == "" || session.Email == "") {
		return ErrUnsupported
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, actor, true); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('server-addon-account',0))`); err != nil {
		return err
	}
	// UPDATE waits for preparations publishing under FOR SHARE. Publication after
	// this transaction must recheck the active generation and will be rejected.
	if _, err = tx.Exec(ctx, `UPDATE server_addon_accounts SET active=false,auth_key='' WHERE active`); err != nil {
		return err
	}
	if session != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES($1,$2,$3)`, session.ID, session.Email, session.Key); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
