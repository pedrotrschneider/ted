package streaming

import (
	"context"
	"errors"
	"sort"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
)

type BatchResult struct {
	Total    int `json:"total"`
	Queued   int `json:"queued"`
	Existing int `json:"existing"`
	Missing  int `json:"missing"`
}

// EnqueueBatch commits every eligible episode job together before acknowledging.
// The caller supplies a complete provider episode list for the requested scope.
func (s *Service) EnqueueBatch(ctx context.Context, who accounts.Identity, ids []string, mappedOnly bool) (BatchResult, error) {
	result := BatchResult{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return result, err
	}
	unique := make(map[string]bool, len(ids))
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if !unique[id] {
			unique[id] = true
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered) // Consistent lock order for overlapping season/series requests.
	result.Total = len(ordered)
	for _, id := range ordered {
		queued, e := s.enqueueTx(ctx, tx, id)
		if errors.Is(e, ErrNotReady) && mappedOnly {
			result.Missing++
			continue
		}
		if e != nil {
			return BatchResult{}, e
		}
		if queued {
			result.Queued++
		} else {
			result.Existing++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchResult{}, err
	}
	return result, nil
}

// DownloadBatch atomically reserves space and queues the whole requested scope.
func (s *Service) DownloadBatch(ctx context.Context, who accounts.Identity, ids []string, readyOnly bool) (BatchResult, error) {
	result := BatchResult{}
	if s.storage == nil {
		return result, ErrStorage
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, false); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, storageLock); err != nil {
		return result, err
	}
	unique := map[string]bool{}
	ordered := []string{}
	for _, id := range ids {
		if !unique[id] {
			unique[id] = true
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	result.Total = len(ordered)
	for _, id := range ordered {
		queued, e := s.downloadTx(ctx, tx, id)
		if errors.Is(e, ErrNotReady) && readyOnly {
			result.Missing++
			continue
		}
		if e != nil {
			return BatchResult{}, e
		}
		if queued {
			result.Queued++
		} else {
			result.Existing++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchResult{}, err
	}
	return result, nil
}
