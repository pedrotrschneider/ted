package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"errors"
	"net/http"
	"strings"
)

func (a *API) libraryRemoval(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	kind := "movie"
	if strings.Contains(r.Pattern, "/series/") {
		kind = "series"
	}
	if r.Method == "DELETE" {
		result, err := a.Streaming.RemoveTitle(r.Context(), identity(u), r.PathValue("id"), kind)
		if errors.Is(err, catalog.ErrConflict) {
			respond(w, 409, map[string]string{"error": "Finish the pending mapping operation before removing this title."})
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 202, result)
		return
	}
	result, err := a.Streaming.RemovalStatus(r.Context(), r.PathValue("id"), kind)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, true) {
		respond(w, 200, result)
	}
}
