package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, unicode.IsControl)
}

func authorize(ctx context.Context, tx pgx.Tx, who accounts.Identity, admin bool) error {
	var role string
	var disabled bool
	var version int64
	err := tx.QueryRow(ctx, `SELECT role,disabled,session_version FROM users WHERE id=$1 FOR SHARE`, who.ID).Scan(&role, &disabled, &version)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (disabled || version != who.Version) {
		return accounts.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin && role != "admin" {
		return accounts.ErrForbidden
	}
	return nil
}
