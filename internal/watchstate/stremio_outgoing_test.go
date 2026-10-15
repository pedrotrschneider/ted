package watchstate

import (
	"context"
	"testing"
)

func seedOutgoing(t *testing.T, s *Store, user int64) {
	t.Helper()
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('test-account','test@example.com','test-key')`)
	if err != nil {
		t.Fatal(err)
	}
	var link, preview string
	if err = s.pool.QueryRow(ctx, `INSERT INTO stremio_links(user_id,account_id) VALUES($1,'test-account') RETURNING id::text`, user).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `INSERT INTO stremio_previews(link_id,phase,captured_at) VALUES($1,'ready',now()) RETURNING id::text`, link).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO stremio_imports(preview_id,link_id,phase) VALUES($1,$2,'complete')`, preview, link); err != nil {
		t.Fatal(err)
	}
}
func outgoingCount(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM stremio_outgoing`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
