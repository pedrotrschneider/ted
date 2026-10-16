package accounts

import (
	"context"

	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
)

const UserPageSize = 50

type UserPage struct {
	Users       []User `json:"users"`
	NextAfterID *int64 `json:"next_after_id,omitempty"`
}

// List reads authorization and accounts from one database snapshot. It returns no
// successful partial page if reading rows or committing the transaction fails.
func (s *Service) List(ctx context.Context, who Identity, after int64) (UserPage, error) {
	if after < 0 {
		return UserPage{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return UserPage{}, err
	}
	defer database.Rollback(tx)
	actor, err := userFrom(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1", who.ID))
	if err != nil {
		return UserPage{}, err
	}
	if actor.Disabled || actor.Version != who.Version {
		return UserPage{}, ErrUnauthorized
	}
	if actor.Role != "admin" {
		return UserPage{}, ErrForbidden
	}
	rows, err := tx.Query(ctx, "SELECT "+userColumns+" FROM users WHERE id>$1 ORDER BY id LIMIT $2", after, UserPageSize+1)
	if err != nil {
		return UserPage{}, err
	}
	defer rows.Close()
	page := UserPage{Users: make([]User, 0, UserPageSize)}
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Version, &u.DisplayName, &u.AvatarVersion); err != nil {
			return UserPage{}, err
		}
		if len(page.Users) == UserPageSize {
			last := page.Users[len(page.Users)-1].ID
			page.NextAfterID = &last
		} else {
			page.Users = append(page.Users, u)
		}
	}
	if err = rows.Err(); err != nil {
		return UserPage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return UserPage{}, err
	}
	return page, nil
}
