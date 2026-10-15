package httpapi

import (
	tedversion "entertainmentdistrict"
	"net/http"
)

func compatibilityHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-TED-Compatibility-Major", tedversion.Major())
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet && r.URL.Path == "/api/client/v1/version" {
			respond(w, http.StatusOK, map[string]string{"version": tedversion.Release(), "compatibility_major": tedversion.Major()})
			return
		}
		major := r.Header.Get("X-Teddy-Compatibility-Major")
		// Existing clients predate the header and belong to major zero.
		if major == "" {
			major = "0"
		}
		if major != tedversion.Major() {
			respond(w, http.StatusUpgradeRequired, map[string]string{"code": "incompatible_version", "compatibility_major": tedversion.Major()})
			return
		}
		next.ServeHTTP(w, r)
	})
}
