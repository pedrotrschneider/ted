package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"testing"
	"time"
)

func stremioFixture(t *testing.T) (*Store, accounts.Identity) {
	t.Helper()
	s, who := fixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('remote','test@example.com','key');`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO stremio_links(user_id,account_id) VALUES($1,'remote')`, who.ID); err != nil {
		t.Fatal(err)
	}
	return s, who
}
func observeFlag(t *testing.T, s *Store, user int64, played bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The pre-poll baseline follows the earlier local seed for this fixture.
	if err = ObserveStremio(ctx, tx, user, "remote", "tt1234567", []byte(`{"name":"Example"}`), played, time.Now().Add(time.Second), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
