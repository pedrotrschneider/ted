package addons

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/stremio"
	"errors"
	"testing"
)

func TestServerProviderIndependentOfPersonalHistory(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	id := choose(t, s, auth, g, fixtureMedia)
	if _, err := s.pool.Exec(ctx, `UPDATE stremio_links SET retired_at=now();UPDATE stremio_accounts SET auth_key='',updated_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selectedForPlayback(ctx, g, fixtureMedia, id); err != nil {
		t.Fatal("history disconnect invalidated discovery", err)
	}
	viewer := secondViewer(t, s, auth)
	if _, err := s.Sources(ctx, auth, viewer, fixtureMedia, ""); err != nil {
		t.Fatal("viewer without personal history link", err)
	}
	if _, err := s.selected(ctx, viewer, id); !errors.Is(err, ErrSelection) {
		t.Fatal("cross-viewer source handle", err)
	}
	if err := s.ConfigureProvider(ctx, accounts.Identity{ID: viewer.User.ID, Version: viewer.User.Version}, nil); !errors.Is(err, accounts.ErrForbidden) {
		t.Fatal("non-admin configured provider", err)
	}
	if err := s.ConfigureProvider(ctx, accounts.Identity{ID: g.User.ID, Version: g.User.Version}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selected(ctx, g, id); !errors.Is(err, ErrSelection) {
		t.Fatal("disconnected provider reused source", err)
	}
}
func TestServerProviderDoesNotEnrollHistory(t *testing.T) {
	s, _, g, _, _ := fixture(t)
	ctx := context.Background()
	session := stremio.Session{ID: "discovery-only", Email: "addons@example.com", Key: "private"}
	if err := s.ConfigureProvider(ctx, accounts.Identity{ID: g.User.ID, Version: g.User.Version}, &session); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stremio_accounts WHERE id='discovery-only'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("provider enrolled history", n, err)
	}
	status, err := s.ProviderStatus(ctx)
	if err != nil || !status.Connected || status.Email != session.Email {
		t.Fatal(status, err)
	}
}
