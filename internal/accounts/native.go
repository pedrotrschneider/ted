package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativePolicy applies to new grants. Existing families retain their original
// absolute deadline even if configuration changes. No replay lineage is purged.
type NativePolicy struct {
	AccessTTL, RefreshIdleTTL, FamilyTTL time.Duration
}

func DefaultNativePolicy() NativePolicy {
	return NativePolicy{10 * time.Minute, 30 * 24 * time.Hour, 90 * 24 * time.Hour}
}

func (p NativePolicy) Validate() error {
	if p.AccessTTL < time.Second || p.RefreshIdleTTL <= p.AccessTTL || p.FamilyTTL < p.RefreshIdleTTL {
		return errors.New("native token lifetimes require access >= 1s, access < refresh inactivity <= absolute family lifetime")
	}
	return nil
}

type NativeAuth struct {
	pool     *pgxpool.Pool
	accounts *Service
	policy   NativePolicy
}

func NewNativeAuth(pool *pgxpool.Pool, service *Service, policy NativePolicy) (*NativeAuth, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &NativeAuth{pool: pool, accounts: service, policy: policy}, nil
}

type NativeTokens struct {
	TokenType        string    `json:"token_type"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresIn        int64     `json:"expires_in"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	FamilyExpiresAt  time.Time `json:"family_expires_at"`
	FamilyID         string    `json:"family_id"`
	User             User      `json:"user"`
}

type NativeGrant struct {
	User      User
	FamilyID  string
	DeviceID  string
	ExpiresAt time.Time
	// Keep the credential digest private; callers cannot construct a grant from
	// public family/device identifiers to bypass access-token validation.
	digest []byte
}

func opaqueToken(purpose string) (string, []byte) {
	b := make([]byte, 32)
	// Go's crypto/rand terminates the process on unavailable secure randomness.
	_, _ = rand.Read(b)
	raw := purpose + base64.RawURLEncoding.EncodeToString(b)
	d := sha256.Sum256([]byte(raw))
	return raw, d[:]
}

func tokenDigest(raw, purpose string) ([]byte, error) {
	if len(raw) != len(purpose)+43 || !strings.HasPrefix(raw, purpose) {
		return nil, ErrUnauthorized
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw[len(purpose):])
	if err != nil || len(b) != 32 {
		return nil, ErrUnauthorized
	}
	d := sha256.Sum256([]byte(raw))
	return d[:], nil
}

func validDevice(value string) bool {
	return utf8.ValidString(value) && len(value) > 0 && len(value) <= 128 && !strings.ContainsFunc(value, unicode.IsControl)
}

func (s *NativeAuth) Login(ctx context.Context, username, password, ip, deviceID, deviceName string) (NativeTokens, error) {
	deviceID, deviceName = strings.TrimSpace(deviceID), strings.TrimSpace(deviceName)
	if !validDevice(deviceID) || !validDevice(deviceName) {
		return NativeTokens{}, ErrInvalid
	}
	u, err := s.accounts.Login(ctx, username, password, ip)
	if err != nil {
		return NativeTokens{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NativeTokens{}, err
	}
	defer database.Rollback(tx)
	// Password changes, disable and all-session logout hold the same user row.
	if err = AuthorizeMutation(ctx, tx, Identity{u.ID, u.Version}, false); err != nil {
		return NativeTokens{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return NativeTokens{}, err
	}
	absolute, idle := now.Add(s.policy.FamilyTTL), now.Add(s.policy.RefreshIdleTTL)
	var family string
	err = tx.QueryRow(ctx, `INSERT INTO native_token_families(user_id,account_version,device_id,device_name,created_at,absolute_expires_at,idle_expires_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, u.ID, u.Version, deviceID, deviceName, now, absolute, idle).Scan(&family)
	if err != nil {
		return NativeTokens{}, err
	}
	pair, err := s.issue(ctx, tx, u, family, 1, nil, now, idle, absolute)
	if err != nil {
		return NativeTokens{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NativeTokens{}, err
	}
	return pair, nil
}

func (s *NativeAuth) issue(ctx context.Context, tx pgx.Tx, u User, family string, generation int64, previous []byte, now, idle, absolute time.Time) (NativeTokens, error) {
	access, ad := opaqueToken("ted_a_")
	refresh, rd := opaqueToken("ted_r_")
	expiry := minTime(now.Add(s.policy.AccessTTL), idle)
	if _, err := tx.Exec(ctx, `INSERT INTO native_refresh_tokens(digest,family_id,generation,previous_digest,created_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, rd, family, generation, previous, now, idle); err != nil {
		return NativeTokens{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO native_access_tokens(digest,family_id,created_at,expires_at) VALUES ($1,$2,$3,$4)`, ad, family, now, expiry); err != nil {
		return NativeTokens{}, err
	}
	return NativeTokens{"Bearer", access, refresh, int64(expiry.Sub(now) / time.Second), expiry, idle, absolute, family, u}, nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (s *NativeAuth) Refresh(ctx context.Context, raw string) (NativeTokens, error) {
	digest, err := tokenDigest(raw, "ted_r_")
	if err != nil {
		return NativeTokens{}, err
	}
	// Lookup identifies the server-owned family. Unknown credentials cannot revoke
	// a client-supplied family, and refresh never accepts a family ID from callers.
	var family string
	var who Identity
	err = s.pool.QueryRow(ctx, `SELECT f.id::text,f.user_id,f.account_version FROM native_refresh_tokens t
 JOIN native_token_families f ON f.id=t.family_id WHERE t.digest=$1`, digest).Scan(&family, &who.ID, &who.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeTokens{}, ErrUnauthorized
	}
	if err != nil {
		return NativeTokens{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NativeTokens{}, err
	}
	defer database.Rollback(tx)
	// All native writes lock user first, then family: consistent with account
	// mutations, and shared across processes rather than an in-memory mutex.
	u, err := authorize(ctx, tx, who, false)
	if err != nil {
		return NativeTokens{}, err
	}
	var absolute, idle time.Time
	var revoked *time.Time
	if err = tx.QueryRow(ctx, `SELECT absolute_expires_at,idle_expires_at,revoked_at FROM native_token_families WHERE id=$1 FOR UPDATE`, family).Scan(&absolute, &idle, &revoked); err != nil {
		return NativeTokens{}, err
	}
	var expiry, now time.Time
	var consumed *time.Time
	var generation int64
	if err = tx.QueryRow(ctx, `SELECT generation,expires_at,consumed_at,clock_timestamp() FROM native_refresh_tokens WHERE digest=$1`, digest).Scan(&generation, &expiry, &consumed, &now); err != nil {
		return NativeTokens{}, err
	}
	if revoked != nil {
		return NativeTokens{}, ErrUnauthorized
	}
	if consumed != nil {
		if _, err = tx.Exec(ctx, `UPDATE native_token_families SET revoked_at=$2,revocation_reason='refresh_reuse' WHERE id=$1`, family, now); err != nil {
			return NativeTokens{}, err
		}
		// The rejection MUST commit revocation. Rolling back an error here would
		// leave the successful competing request's credentials usable.
		if err = tx.Commit(ctx); err != nil {
			return NativeTokens{}, err
		}
		return NativeTokens{}, ErrUnauthorized
	}
	if !now.Before(absolute) || !now.Before(idle) || !now.Before(expiry) {
		return NativeTokens{}, ErrUnauthorized
	}
	idle = minTime(now.Add(s.policy.RefreshIdleTTL), absolute)
	if _, err = tx.Exec(ctx, `UPDATE native_refresh_tokens SET consumed_at=$2 WHERE digest=$1`, digest, now); err != nil {
		return NativeTokens{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE native_token_families SET idle_expires_at=$2 WHERE id=$1`, family, idle); err != nil {
		return NativeTokens{}, err
	}
	pair, err := s.issue(ctx, tx, u, family, generation+1, digest, now, idle, absolute)
	if err != nil {
		return NativeTokens{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NativeTokens{}, err
	}
	return pair, nil
}

func (s *NativeAuth) Authenticate(ctx context.Context, raw string) (NativeGrant, error) {
	digest, err := tokenDigest(raw, "ted_a_")
	if err != nil {
		return NativeGrant{}, err
	}
	var g NativeGrant
	err = s.pool.QueryRow(ctx, `SELECT u.id,u.username,u.role,u.disabled,u.session_version,u.display_name,f.id::text,f.device_id,t.expires_at
 FROM native_access_tokens t JOIN native_token_families f ON f.id=t.family_id JOIN users u ON u.id=f.user_id
 WHERE t.digest=$1 AND NOT u.disabled AND u.session_version=f.account_version AND f.revoked_at IS NULL
 AND clock_timestamp()<t.expires_at AND clock_timestamp()<f.idle_expires_at AND clock_timestamp()<f.absolute_expires_at`, digest).
		Scan(&g.User.ID, &g.User.Username, &g.User.Role, &g.User.Disabled, &g.User.Version, &g.User.DisplayName, &g.FamilyID, &g.DeviceID, &g.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NativeGrant{}, ErrUnauthorized
	}
	if err != nil {
		return NativeGrant{}, err
	}
	g.digest = digest
	return g, nil
}

// AuthorizeMutation fences native writes against account AND family revocation,
// including refresh reuse, and rechecks expiry after any lock wait.
func (s *NativeAuth) AuthorizeMutation(ctx context.Context, tx pgx.Tx, g NativeGrant) error {
	if err := AuthorizeMutation(ctx, tx, Identity{g.User.ID, g.User.Version}, false); err != nil {
		return err
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT revoked_at IS NULL AND clock_timestamp()<idle_expires_at AND clock_timestamp()<absolute_expires_at
 FROM native_token_families WHERE id=$1 AND user_id=$2 AND account_version=$3 FOR SHARE`, g.FamilyID, g.User.ID, g.User.Version).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM native_access_tokens WHERE digest=$1 AND family_id=$2 AND clock_timestamp()<expires_at)`, g.digest, g.FamilyID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrUnauthorized
	}
	return nil
}

func (s *NativeAuth) Logout(ctx context.Context, g NativeGrant, all bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = s.AuthorizeMutation(ctx, tx, g); err != nil {
		return err
	}
	if all {
		// Browser sessions and every family are invalidated by account version.
		_, err = tx.Exec(ctx, `UPDATE users SET session_version=session_version+1 WHERE id=$1`, g.User.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE native_token_families SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE id=$1`, g.FamilyID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
