// Package testdb creates isolated schemas only in an explicitly supplied test database.
package testdb

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TED_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("TED_REQUIRE_DATABASE_TESTS") == "1" {
			t.Fatal("TED_TEST_DATABASE_URL is required")
		}
		t.Skip("PostgreSQL integration: set TED_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	t.Cleanup(pool.Close)
	name := fmt.Sprintf("ted_test_%x", random(t))
	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("test database URL invalid")
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}
func random(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
