package httpapi

import (
	"errors"
	"net/http"
	"regexp"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/stremio"
)

var copyIDPattern = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

func (a *API) stremioCopyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/stremio-copy", a.protected(true, func(w http.ResponseWriter, r *http.Request, u accounts.User) {
		v, err := a.stremio.CopyStatus(r.Context(), identity(u))
		if err != nil {
			a.stremioFailure(w, r, err)
			return
		}
		respond(w, 200, map[string]any{"copy": v})
	}))
	mux.HandleFunc("POST /api/admin/stremio-copy", a.protected(true, func(w http.ResponseWriter, r *http.Request, u accounts.User) {
		var input stremio.CopyRequest
		if !decodeLimit(w, r, &input, 12<<10) {
			return
		}
		id, err := a.stremio.StartCopy(r.Context(), identity(u), input)
		if errors.Is(err, stremio.ErrCopySameAccount) {
			respond(w, 400, map[string]string{"code": "stremio_copy_same_account"})
			return
		}
		if errors.Is(err, stremio.ErrChanged) {
			respond(w, 409, map[string]string{"code": "stremio_copy_active"})
			return
		}
		if err != nil {
			a.stremioFailure(w, r, err)
			return
		}
		respond(w, 202, map[string]string{"id": id})
	}))
	for _, action := range []string{"retry", "cancel"} {
		mux.HandleFunc("POST /api/admin/stremio-copy/{id}/"+action, a.protected(true, func(w http.ResponseWriter, r *http.Request, u accounts.User) {
			id := r.PathValue("id")
			if !copyIDPattern.MatchString(id) {
				respond(w, 400, map[string]string{"code": "stremio_invalid"})
				return
			}
			err := a.stremio.ControlCopy(r.Context(), identity(u), id, action == "cancel")
			if errors.Is(err, stremio.ErrNotFound) {
				respond(w, 404, map[string]string{"code": "stremio_copy_missing"})
				return
			}
			if errors.Is(err, stremio.ErrChanged) {
				respond(w, 409, map[string]string{"code": "stremio_copy_finished"})
				return
			}
			if err != nil {
				a.stremioFailure(w, r, err)
				return
			}
			respond(w, 202, map[string]string{"status": action})
		}))
	}
}
