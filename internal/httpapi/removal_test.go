package httpapi

import (
	"context"
	"testing"
)

func TestLibraryRemovalIsAdminOnlyAndTitleOnly(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	if _, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	viewer := login(t, h, "viewer")
	for _, kind := range []string{"movies", "series"} {
		path := "/api/admin/" + kind + "/tt1245698/library"
		requireStatus(t, call(t, h, "DELETE", path, nil, nil), 401)
		requireStatus(t, call(t, h, "DELETE", path, nil, viewer), 403)
	}
	requireStatus(t, call(t, h, "DELETE", "/api/admin/episodes/tt1245698:1:1/library", nil, admin), 404)
	requireStatus(t, call(t, h, "DELETE", "/api/admin/seasons/tt1245698:1/library", nil, admin), 404)
}
