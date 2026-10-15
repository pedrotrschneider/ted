package httpapi

import (
	"net/http"
	"testing"
)

func TestRetiredRoutesAreUnavailableToGuestsAndAdministrators(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	admin := login(t, h, "admin")
	for _, cookie := range []*http.Cookie{nil, admin} {
		for _, path := range []string{"/api/admin/watch-links"} {
			r := call(t, h, "GET", path, nil, cookie)
			requireStatus(t, r, http.StatusNotFound)
		}
	}
	// Local account and personal history remain available after retirement.
	requireStatus(t, call(t, h, "GET", "/api/watch-history", nil, admin), http.StatusOK)
}
