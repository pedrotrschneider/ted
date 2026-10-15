package accounts

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"entertainmentdistrict/internal/database"
)

func validDisplayName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Service) SetDisplayName(ctx context.Context, who Identity, name string) (User, error) {
	name = strings.TrimSpace(name)
	if !validDisplayName(name) {
		return User{}, ErrInvalid
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, false); err != nil {
		return User{}, err
	}
	u, err := userFrom(tx.QueryRow(ctx, "UPDATE users SET display_name=$2 WHERE id=$1 RETURNING "+userColumns, who.ID, name))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

func (s *Service) MinPasswordLength() int { return s.passwords.minimum }
