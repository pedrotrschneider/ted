package streaming

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"testing"
	"time"
)

func TestBatchPreparationAtomicCoverageAndIdempotence(t *testing.T) {
	s, m, who := fixture(t, &resolver{})
	ctx := context.Background()
	if _, err := s.EnqueueBatch(ctx, who, []string{m.IMDB, "tt9999999:1:1"}, false); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial enqueue", count, err)
	}
	result, err := s.EnqueueBatch(ctx, who, []string{m.IMDB, "tt9999999:1:1"}, true)
	if err != nil || result != (BatchResult{Total: 2, Queued: 1, Missing: 1}) {
		t.Fatal(result, err)
	}
	result, err = s.EnqueueBatch(ctx, who, []string{m.IMDB, m.IMDB}, false)
	if err != nil || result != (BatchResult{Total: 1, Existing: 1}) {
		t.Fatal(result, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate queue", count, err)
	}
}
func TestBatchPreparationAuthorizationAndRetry(t *testing.T) {
	s, m, who := fixture(t, &resolver{})
	ctx := context.Background()
	if _, err := s.EnqueueBatch(ctx, accounts.Identity{ID: who.ID, Version: who.Version + 1}, []string{m.IMDB}, true); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err := s.EnqueueBatch(ctx, who, []string{m.IMDB}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	result, err := s.EnqueueBatch(ctx, who, []string{m.IMDB}, false)
	if err != nil || result.Queued != 1 {
		t.Fatal(result, err)
	}
}

func TestOverlappingBatchAndSinglePreparationSerialize(t *testing.T) {
	s, m, who := fixture(t, &resolver{})
	ctx := context.Background()
	held, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err = held.Exec(ctx, `SELECT id FROM catalog_media WHERE id=$1 FOR UPDATE`, m.ID); err != nil {
		t.Fatal(err)
	}
	var other accounts.Identity
	if err = s.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('other','Other','unused','admin') RETURNING id,session_version`).Scan(&other.ID, &other.Version); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { _, err := s.EnqueueBatch(ctx, who, []string{m.IMDB}, false); results <- err }()
	go func() { results <- s.Enqueue(ctx, other, m.IMDB) }()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT c.id::text,m.id::text FROM catalog_media%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("both requests did not reach the held media lock")
	}
	if err = held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&n); err != nil || n != 1 {
		t.Fatal("overlap duplicated jobs", n, err)
	}
}
