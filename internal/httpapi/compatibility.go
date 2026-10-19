package httpapi

import (
	tedversion "entertainmentdistrict"
	"net/http"
)

func compatibilityHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-TED-Compatibility-Major", tedversion.Major())
		w.Header().Set("X-TED-Compatibility-Version", tedversion.Compatibility())
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet && r.URL.Path == "/api/client/v1/version" {
			respond(w, http.StatusOK, map[string]string{"version": tedversion.Release(), "compatibility_major": tedversion.Major(), "compatibility_version": tedversion.Compatibility()})
			return
		}
		contract := r.Header.Get("X-Teddy-Compatibility-Version")
		if contract != tedversion.Compatibility() {
			respond(w, http.StatusUpgradeRequired, map[string]string{"code": "incompatible_version", "compatibility_major": tedversion.Major(), "compatibility_version": tedversion.Compatibility()})
			return
		}
		next.ServeHTTP(w, r)
	})
}
