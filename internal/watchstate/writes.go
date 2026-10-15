package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type WriteRequest struct {
	Revision int64  `json:"revision"`
	Action   string `json:"action"`
	DateMode string `json:"date_mode"`
}

func (s *Store) Edit(ctx context.Context, who accounts.Identity, media string, request WriteRequest) (string, error) {
	if !uuid.MatchString(media) || request.Revision < 1 || !validDateMode(request.Action == "watched", request.DateMode) {
		return "", ErrInvalid
	}
	switch request.Action {
	case "watched", "unwatched", "reset":
	default:
		return "", ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accounts.MutationLockID); err != nil {
		return "", err
	}
	if err = authorize(ctx, tx, who, false); err != nil {
		return "", err
	}
	item := TitleItem{Media: media}
	err = tx.QueryRow(ctx, "SELECT state,revision FROM watch_current WHERE user_id=$1 AND media_id=$2 FOR UPDATE", who.ID, media).Scan(&item.State, &item.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnavailable
	}
	if err != nil {
		return "", err
	}
	if item.Revision != request.Revision {
		return "", ErrConflict
	}
	if err = s.saveLocalDecision(ctx, tx, who.ID, item, request.Action, request.DateMode, false); err != nil {
		return "", err
	}
	return "", tx.Commit(ctx)
}

// An omitted mode retains the dated behavior for existing API callers.
func validDateMode(watched bool, mode string) bool {
	return mode == "" || watched && (mode == "now" || mode == "undated")
}

func editedState(current State, action, dateMode string) State {
	desired := current
	now := time.Now().UTC().Truncate(100 * time.Nanosecond)
	switch action {
	case "watched":
		desired.Played = true
		desired.Position = 0
		if desired.PlayCount == 0 {
			desired.PlayCount = 1
		}
		desired.LastPlayed = nil
		if dateMode != "undated" {
			desired.LastPlayed = &now
		}
	case "unwatched":
		desired = State{}
	case "reset":
		desired.Position = 0
	}
	return desired
}
