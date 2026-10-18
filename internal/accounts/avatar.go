package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/profileimage"
	"github.com/jackc/pgx/v5"
)

// SetAvatar processes outside the transaction, then rechecks account authority
// under its row lock before atomically publishing the replacement and version.
func (s *Service) SetAvatar(ctx context.Context, who Identity, upload []byte) (User, error) {
	image, err := profileimage.Normalize(ctx, upload)
	if err != nil {
		return User{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, false); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_avatars(user_id,image) VALUES($1,$2)
		ON CONFLICT(user_id) DO UPDATE SET image=excluded.image`, who.ID, image); err != nil {
		return User{}, err
	}
	digest := sha256.Sum256(image)
	u, err := userFrom(tx.QueryRow(ctx, "UPDATE users SET avatar_version=$2 WHERE id=$1 RETURNING "+userColumns, who.ID, hex.EncodeToString(digest[:])))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

// Avatar reads only the authenticated account; no caller-controlled user ID is
// accepted. Empty success means the account has no uploaded picture yet.
func (s *Service) Avatar(ctx context.Context, who Identity) ([]byte, error) {
	var image []byte
	err := s.pool.QueryRow(ctx, `SELECT a.image FROM users u LEFT JOIN account_avatars a ON a.user_id=u.id
		WHERE u.id=$1 AND u.session_version=$2 AND NOT u.disabled`, who.ID, who.Version).Scan(&image)
	if err == pgx.ErrNoRows {
		err = ErrUnauthorized
	}
	return image, err
}

// VisibleAvatar shares only the enabled person's normalized picture. It does
// not grant access to their credentials, settings, raw history or mutations.
func (s *Service) VisibleAvatar(ctx context.Context, who Identity, target int64) ([]byte, string, error) {
	if _, err := s.Current(ctx, who); err != nil {
		return nil, "", err
	}
	var image []byte
	var version string
	err := s.pool.QueryRow(ctx, `SELECT a.image,u.avatar_version FROM users u LEFT JOIN account_avatars a ON a.user_id=u.id
 WHERE u.id=$3 AND NOT u.disabled AND (u.id=$1 OR NOT EXISTS(SELECT 1 FROM joint_account_members j WHERE j.joint_id=u.id))
 AND EXISTS(SELECT 1 FROM users actor WHERE actor.id=$1 AND actor.session_version=$2 AND NOT actor.disabled)`, who.ID, who.Version, target).Scan(&image, &version)
	if err == pgx.ErrNoRows {
		err = ErrNotFound
	}
	return image, version, err
}
