// Package settings stores server-owned provider credentials. Public status never includes values.
package settings

import (
	"context"
	"errors"
	"strings"

	"entertainmentdistrict/internal/accounts"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalid = errors.New("invalid provider settings")
var ErrConflict = errors.New("provider settings changed; reload before saving")

type Credentials struct {
	RDKey     string `json:"-"`
	TMDBToken string `json:"-"`
	TVDBKey   string `json:"-"`
	TVDBPIN   string `json:"-"`
	Revision  int64  `json:"-"`
}
type Status struct {
	Revision int64 `json:"revision"`
	RD       bool  `json:"rd_configured"`
	TMDB     bool  `json:"tmdb_configured"`
	TVDB     bool  `json:"tvdb_configured"`
	PIN      bool  `json:"tvdb_pin_configured"`
}
type Patch struct {
	Revision  int64   `json:"revision"`
	RDKey     *string `json:"rd_key,omitempty"`
	TMDBToken *string `json:"tmdb_token,omitempty"`
	TVDBKey   *string `json:"tvdb_key,omitempty"`
	TVDBPIN   *string `json:"tvdb_pin,omitempty"`
}
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Existing environment credentials are imported once. A saved empty value remains empty on restart.
func (s *Store) Initialize(ctx context.Context, c Credentials) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO provider_settings(id,rd_key,tmdb_token,tvdb_key,tvdb_pin) VALUES(true,$1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, c.RDKey, c.TMDBToken, c.TVDBKey, c.TVDBPIN)
	return err
}
func (s *Store) Load(ctx context.Context) (Credentials, error) {
	var c Credentials
	err := s.pool.QueryRow(ctx, `SELECT rd_key,tmdb_token,tvdb_key,tvdb_pin,revision FROM provider_settings WHERE id`).Scan(&c.RDKey, &c.TMDBToken, &c.TVDBKey, &c.TVDBPIN, &c.Revision)
	return c, err
}
func status(c Credentials) Status {
	return Status{Revision: c.Revision, RD: c.RDKey != "", TMDB: c.TMDBToken != "", TVDB: c.TVDBKey != "", PIN: c.TVDBPIN != ""}
}
func (s *Store) Status(ctx context.Context) (Status, error) {
	c, err := s.Load(ctx)
	return status(c), err
}
func (s *Store) Update(ctx context.Context, actor accounts.Identity, p Patch) (Status, error) {
	if p.Revision < 1 {
		return Status{}, ErrInvalid
	}
	for _, v := range []*string{p.RDKey, p.TMDBToken, p.TVDBKey, p.TVDBPIN} {
		if v != nil {
			*v = strings.TrimSpace(*v)
			if len(*v) > 8192 || strings.ContainsAny(*v, "\r\n\t ") {
				return Status{}, ErrInvalid
			}
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT role='admin' AND NOT disabled AND session_version=$2 FROM users WHERE id=$1 FOR SHARE`, actor.ID, actor.Version).Scan(&allowed); err != nil {
		return Status{}, err
	}
	if !allowed {
		return Status{}, accounts.ErrForbidden
	}
	var c Credentials
	err = tx.QueryRow(ctx, `SELECT rd_key,tmdb_token,tvdb_key,tvdb_pin,revision FROM provider_settings WHERE id FOR UPDATE`).Scan(&c.RDKey, &c.TMDBToken, &c.TVDBKey, &c.TVDBPIN, &c.Revision)
	if err != nil {
		return Status{}, err
	}
	if c.Revision != p.Revision {
		return Status{}, ErrConflict
	}
	for _, v := range []struct {
		target *string
		value  *string
	}{{&c.RDKey, p.RDKey}, {&c.TMDBToken, p.TMDBToken}, {&c.TVDBKey, p.TVDBKey}, {&c.TVDBPIN, p.TVDBPIN}} {
		if v.value != nil {
			*v.target = *v.value
		}
	}
	c.Revision++
	_, err = tx.Exec(ctx, `UPDATE provider_settings SET rd_key=$1,tmdb_token=$2,tvdb_key=$3,tvdb_pin=$4,revision=$5,updated_at=now() WHERE id`, c.RDKey, c.TMDBToken, c.TVDBKey, c.TVDBPIN, c.Revision)
	if err != nil {
		return Status{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Status{}, err
	}
	return status(c), nil
}
