package watchstate

import (
	"context"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
	"time"
)

// Caller owns the account/user mutation locks. Keep receipt time distinct from
// a known Stremio creation date, and never replace it with a later poll receipt.
func ObserveAccountTitleAdded(ctx context.Context, tx pgx.Tx, user int64, title string, added *time.Time, received time.Time) error {
	if user <= 0 || !cinemeta.ValidID(title) || received.IsZero() {
		return ErrInvalid
	}
	at := received
	known := false
	if added != nil && added.Year() >= 1970 && added.Year() <= 2100 && !added.After(received) {
		at = *added
		known = true
	}
	_, err := tx.Exec(ctx, `INSERT INTO watch_account_title_dates(user_id,title_id,added_at,provider_dated) VALUES($1,$2,$3,$4)
 ON CONFLICT(user_id,title_id) DO UPDATE SET added_at=EXCLUDED.added_at,provider_dated=true
 WHERE NOT watch_account_title_dates.provider_dated AND EXCLUDED.provider_dated`, user, title, at, known)
	return err
}
