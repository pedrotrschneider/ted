package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"entertainmentdistrict/internal/realdebrid"
)

type rdStub struct {
	calls  int
	fail   error
	during func()
}

func (s *rdStub) Configured() bool { return true }
func (s *rdStub) List(context.Context, int) (realdebrid.Page, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	return realdebrid.Page{Torrents: []realdebrid.Torrent{}, Page: 1}, s.fail
}
func (s *rdStub) Torrent(context.Context, string) (realdebrid.Detail, error) {
	s.calls++
	return realdebrid.Detail{}, s.fail
}
func TestRDAccountAccessAndFailureBoundaries(t *testing.T) {
	f := setup(t)
	stub := &rdStub{}
	f.api.rd = stub
	h := f.api.Handler()
	for _, path := range []string{"/api/admin/rd/status", "/api/admin/rd/torrents", "/api/admin/rd/torrents/T1"} {
		requireStatus(t, call(t, h, "GET", path, nil, nil), 401)
	}
	_, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	viewer := login(t, h, "viewer")
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents", nil, viewer), 403)
	if stub.calls != 0 {
		t.Fatal("unauthorized access reached provider")
	}
	admin := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents?page=bad", nil, admin), 400)
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents", nil, admin), 200)
	stub.fail = realdebrid.ErrCredentials
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents", nil, admin), 502)
	requireStatus(t, call(t, h, "GET", "/api/auth/me", nil, admin), 200)
	stub.fail = errors.New("sensitive-provider-url-and-key")
	r := call(t, h, "GET", "/api/admin/rd/torrents", nil, admin)
	requireStatus(t, r, 502)
	if strings.Contains(r.Body.String()+f.logs.String(), "sensitive-provider-url-and-key") {
		t.Fatal("integration detail leaked")
	}
	stub.fail = nil
	stub.during = func() {
		if err := f.service.Revoke(context.Background(), identity(f.admin)); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents", nil, admin), 401)
}

func TestRDRechecksAdministratorAfterProviderRead(t *testing.T) {
	f := setup(t)
	stub := &rdStub{}
	f.api.rd = stub
	h := f.api.Handler()
	admin := login(t, h, "admin")
	stub.during = func() {
		if _, err := f.pool.Exec(context.Background(), "UPDATE users SET role='user' WHERE id=$1", f.admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	requireStatus(t, call(t, h, "GET", "/api/admin/rd/torrents", nil, admin), 403)
}
