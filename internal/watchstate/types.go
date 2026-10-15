// Package watchstate owns per-user viewing observations independently of library membership.
package watchstate

import (
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid     = errors.New("invalid watch-state input")
	ErrConflict    = errors.New("watch-state association or journal conflict")
	ErrUnavailable = errors.New("watch state unavailable")
	uuid           = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type State struct {
	Played     bool       `json:"played"`
	Position   int64      `json:"position_ticks"`
	PlayCount  int        `json:"play_count"`
	LastPlayed *time.Time `json:"last_played"`
}
