package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"entertainmentdistrict/internal/accounts"
)

func TestUserListingRequiresAdministratorAndRedactsPrivateFields(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	requireStatus(t, call(t, h, "GET", "/api/admin/users", nil, nil), 401)
	user, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	viewer := login(t, h, "viewer")
	requireStatus(t, call(t, h, "GET", "/api/admin/users", nil, viewer), 403)
	admin := login(t, h, "admin")
	r := call(t, h, "GET", "/api/admin/users", nil, admin)
	requireStatus(t, r, 200)
	var page accounts.UserPage
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Users) != 2 || page.Users[1].ID != user.ID || page.NextAfterID != nil {
		t.Fatalf("unexpected user page: %+v", page)
	}
	for _, secret := range []string{"password", "version", "token"} {
		if strings.Contains(r.Body.String(), secret) {
			t.Fatalf("private field exposed: %s", secret)
		}
	}
	if err = f.service.Revoke(context.Background(), identity(f.admin)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "GET", "/api/admin/users", nil, admin), 401)
}

func TestUserListingPaginationAndInvalidCursor(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `INSERT INTO users(username,password_hash,role,disabled,display_name)
 SELECT 'user_'||n,'unused-test-record','user',n%2=0,'User '||n FROM generate_series(1,51) n`)
	if err != nil {
		t.Fatal(err)
	}
	admin := login(t, h, "admin")
	first := call(t, h, "GET", "/api/admin/users", nil, admin)
	requireStatus(t, first, 200)
	var page accounts.UserPage
	if err = json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Users) != 50 || page.NextAfterID == nil {
		t.Fatalf("unbounded or incomplete first page: %+v", page)
	}
	cursor := *page.NextAfterID
	second := call(t, h, "GET", "/api/admin/users?after="+strconv.FormatInt(cursor, 10), nil, admin)
	requireStatus(t, second, 200)
	var last accounts.UserPage
	if err = json.Unmarshal(second.Body.Bytes(), &last); err != nil {
		t.Fatal(err)
	}
	if len(last.Users) != 2 || last.NextAfterID != nil || last.Users[0].ID <= cursor {
		t.Fatalf("incorrect continuation: %+v", last)
	}
	for _, raw := range []string{"-1", "oops", "9223372036854775808"} {
		requireStatus(t, call(t, h, "GET", "/api/admin/users?after="+raw, nil, admin), 400)
	}
	f.pool.Close()
	failed := call(t, h, "GET", "/api/admin/users", nil, admin)
	requireStatus(t, failed, 503)
	if strings.Contains(failed.Body.String(), `"users"`) {
		t.Fatal("failed read became a successful account listing")
	}
}
