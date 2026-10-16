// Package accounts owns local TED credentials and permissions. Integration identities never log in here.
package accounts

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnauthorized = errors.New("authentication required or credentials invalid")
	ErrForbidden    = errors.New("administrator access required")
	ErrConflict     = errors.New("account operation conflicts with existing state")
	ErrLimited      = errors.New("too many login attempts; try again later")
	ErrNotFound     = errors.New("account not found")
)

// Serialize rare account mutations across processes, including bootstrap and last-admin protection.
const MutationLockID int64 = 84051002

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	Version     int64  `json:"-"`
}
type Identity struct{ ID, Version int64 }
type Service struct {
	pool      *pgxpool.Pool
	passwords *Passwords
}

func New(pool *pgxpool.Pool, passwords *Passwords) *Service {
	return &Service{pool: pool, passwords: passwords}
}
func normalize(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func userFrom(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Version, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrUnauthorized
	}
	return u, err
}

const userColumns = "id, username, role, disabled, session_version, display_name"

func (s *Service) Current(ctx context.Context, who Identity) (User, error) {
	u, err := userFrom(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1", who.ID))
	if err == nil && (u.Disabled || u.Version != who.Version) {
		err = ErrUnauthorized
	}
	return u, err
}

// SetupRequired reports an empty installation, never treating a database error as empty.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var empty bool
	err := s.pool.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM users)").Scan(&empty)
	return empty, err
}

func (s *Service) Bootstrap(ctx context.Context, name, password string) (User, error) {
	name = normalize(name)
	if !usernamePattern.MatchString(name) {
		return User{}, ErrInvalid
	}
	hash, err := s.passwords.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer database.Rollback(tx)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users)").Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrConflict
	}
	u, err := insert(ctx, tx, name, name, hash, "admin")
	if err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}

func (s *Service) beginMutation(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", MutationLockID); err != nil {
		database.Rollback(tx)
		return nil, err
	}
	return tx, nil
}
func authorize(ctx context.Context, tx pgx.Tx, who Identity, admin bool) (User, error) {
	u, err := userFrom(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", who.ID))
	if err != nil {
		return u, err
	}
	if u.Disabled || u.Version != who.Version {
		return u, ErrUnauthorized
	}
	if admin && u.Role != "admin" {
		return u, ErrForbidden
	}
	return u, nil
}

// AuthorizeMutation holds the actor's row until the caller commits, so revocation
// cannot race a durable write. Call before taking domain-specific locks.
func AuthorizeMutation(ctx context.Context, tx pgx.Tx, who Identity, admin bool) error {
	_, err := authorize(ctx, tx, who, admin)
	return err
}
func insert(ctx context.Context, tx pgx.Tx, name, displayName, hash, role string) (User, error) {
	u, err := userFrom(tx.QueryRow(ctx, "INSERT INTO users (username,password_hash,role,display_name) VALUES ($1,$2,$3,$4) RETURNING "+userColumns, name, hash, role, displayName))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		err = ErrConflict
	}
	return u, err
}

func (s *Service) Create(ctx context.Context, who Identity, name, displayName, password, role string) (User, error) {
	name = normalize(name)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = name
	}
	if !validDisplayName(displayName) {
		return User{}, ErrInvalid
	}
	if !usernamePattern.MatchString(name) || (role != "admin" && role != "user") {
		return User{}, ErrInvalid
	}
	hash, err := s.passwords.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return User{}, err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, true); err != nil {
		return User{}, err
	}
	u, err := insert(ctx, tx, name, displayName, hash, role)
	if err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}

// Login limits are durable and shared across processes. Source IP comes from the socket, never untrusted forwarding headers.
func (s *Service) limit(ctx context.Context, name, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	allowed := true
	for _, limit := range []struct {
		key   string
		count int
	}{{"user:" + name, 10}, {"ip:" + ip, 50}} {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(limit.key)))
		var attempts int
		err = tx.QueryRow(ctx, `INSERT INTO login_limits(key,attempts,expiry) VALUES ($1,1,now()+interval '10 minutes')
   ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN login_limits.expiry<=now() THEN 1 ELSE least(login_limits.attempts+1,1000) END,
    expiry=CASE WHEN login_limits.expiry<=now() THEN now()+interval '10 minutes' ELSE login_limits.expiry END RETURNING attempts`, key).Scan(&attempts)
		if err != nil {
			return err
		}
		if attempts > limit.count {
			allowed = false
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if !allowed {
		return ErrLimited
	}
	return nil
}
func (s *Service) Login(ctx context.Context, name, password, ip string) (User, error) {
	name = normalize(name)
	if len(name) > 64 || len(password) > 1024 {
		return User{}, ErrInvalid
	}
	if err := s.limit(ctx, name, ip); err != nil {
		return User{}, err
	}
	var u User
	var hash string
	err := s.pool.QueryRow(ctx, "SELECT "+userColumns+", password_hash FROM users WHERE username=$1", name).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Version, &u.DisplayName, &hash)
	absent := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !absent {
		return u, err
	}
	if absent {
		hash = s.passwords.dummy
	}
	ok, err := s.passwords.Check(ctx, password, hash)
	if err != nil {
		return User{}, err
	}
	if !ok || absent || u.Disabled {
		return User{}, ErrUnauthorized
	}
	// Recheck after hashing. A simultaneous password change/revocation invalidates the observed version.
	return s.Current(ctx, Identity{u.ID, u.Version})
}

// Revoke signs out every session for this local account, including stale concurrent session writes.
func (s *Service) Revoke(ctx context.Context, who Identity) error {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, false); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET session_version=session_version+1 WHERE id=$1", who.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) ChangePassword(ctx context.Context, who Identity, old, next string) error {
	var hash string
	if err := s.pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", who.ID).Scan(&hash); err != nil {
		return err
	}
	ok, err := s.passwords.Check(ctx, old, hash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnauthorized
	}
	replacement, err := s.passwords.Hash(ctx, next)
	if err != nil {
		return err
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, false); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET password_hash=$2,session_version=session_version+1 WHERE id=$1", who.ID, replacement); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) SetDisabled(ctx context.Context, who Identity, target int64, disabled bool) error {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, true); err != nil {
		return err
	}
	u, err := userFrom(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", target))
	if errors.Is(err, ErrUnauthorized) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if disabled && !u.Disabled && u.Role == "admin" {
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND NOT disabled").Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrConflict
		}
	}
	if u.Disabled != disabled {
		if _, err = tx.Exec(ctx, "UPDATE users SET disabled=$2,session_version=session_version+1 WHERE id=$1", target, disabled); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
