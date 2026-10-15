package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"net/http"
	"strings"
)

func (a *API) catalogRatings(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	kind := r.URL.Query().Get("kind")
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if (kind != "movie" && kind != "series") || len(ids) > 100 {
		respond(w, 400, map[string]string{"error": "Invalid rating request."})
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !cinemeta.ValidID(id) || seen[id] {
			respond(w, 400, map[string]string{"error": "Invalid rating identities."})
			return
		}
		seen[id] = true
	}
	values, err := a.catalog.Ratings(r.Context(), kind, ids, a.Streaming != nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, false) {
		respond(w, 200, map[string]any{"ratings": values})
	}
}
