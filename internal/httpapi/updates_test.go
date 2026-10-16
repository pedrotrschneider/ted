package httpapi

import (
	"context"
	"testing"
)

func TestUpdateNoticeAdminOnlyAndDailyDismissal(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	ctx := context.Background()
	_, err := f.service.Create(ctx, identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/updates", "/api/admin/updates/dismiss"} {
		method := "GET"
		if path == "/api/admin/updates/dismiss" {
			method = "POST"
		}
		requireStatus(t, call(t, h, method, path, nil, nil), 401)
		requireStatus(t, call(t, h, method, path, nil, login(t, h, "viewer")), 403)
	}
	admin := login(t, h, "admin")
	requireStatus(t, call(t, h, "POST", "/api/admin/updates/dismiss", map[string]any{}, admin), 200)
	var hours float64
	if err := f.pool.QueryRow(ctx, "SELECT extract(epoch FROM update_notice_after-now())/3600 FROM users WHERE id=$1", f.admin.ID).Scan(&hours); err != nil {
		t.Fatal(err)
	}
	if hours < 23.9 || hours > 24 {
		t.Fatal(hours)
	}
	// A new session sees the same dismissal without querying external services.
	requireStatus(t, call(t, h, "GET", "/api/admin/updates", nil, login(t, h, "admin")), 200)
	var viewerSnoozed bool
	if err := f.pool.QueryRow(ctx, "SELECT update_notice_after>now() FROM users WHERE username='viewer'").Scan(&viewerSnoozed); err != nil || viewerSnoozed {
		t.Fatal(viewerSnoozed, err)
	}
}
