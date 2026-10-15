package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestBootstrapConcurrentAndCannotResetExistingAccount(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	passwords, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	s := New(pool, passwords)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", MutationLockID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, name := range []string{"admin_one", "admin_two"} {
		go func() { _, err := s.Bootstrap(ctx, name, "bootstrap-password"); results <- err }()
	}
	waitBlocked(t, pool.QueryRow, 2)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	_, err = s.Bootstrap(ctx, "reset_attempt", "different-password")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("bootstrap reset not refused: %v", err)
	}
}
func TestQueuedMutationRechecksRevokedAuthorization(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	passwords, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	s := New(pool, passwords)
	admin, err := s.Bootstrap(ctx, "admin", "bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", MutationLockID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.Create(ctx, Identity{admin.ID, admin.Version}, "late_user", "Late User", "some-long-password", "user")
		result <- err
	}()
	waitBlocked(t, pool.QueryRow, 1)
	if _, err = tx.Exec(ctx, "UPDATE users SET session_version=session_version+1 WHERE id=$1", admin.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale mutation was authorized: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("stale mutation created account: %v", err)
	}
}

func waitBlocked(t *testing.T, query func(context.Context, string, ...any) pgx.Row, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := query(ctx, "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted", MutationLockID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("workers did not reach the held PostgreSQL lock")
		case <-ticker.C:
		}
	}
}
