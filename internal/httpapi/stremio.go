package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/stremio"
	"errors"
	"net/http"
)

func (a *API) stremioFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, stremio.ErrInvalid):
		respond(w, 400, map[string]string{"code": "stremio_invalid"})
	case errors.Is(err, stremio.ErrCredentials):
		respond(w, 400, map[string]string{"code": "stremio_credentials"})
	case errors.Is(err, stremio.ErrChanged):
		respond(w, 409, map[string]string{"code": "stremio_changed"})
	case errors.Is(err, stremio.ErrIncomplete), errors.Is(err, stremio.ErrUnavailable):
		respond(w, 503, map[string]string{"code": "stremio_unavailable"})
	default:
		a.fail(w, r, err)
	}
}
func (a *API) stremioStatus(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	v, err := a.stremio.Status(r.Context(), identity(u))
	if err != nil {
		a.stremioFailure(w, r, err)
		return
	}
	respond(w, 200, v)
}
func (a *API) stremioConnect(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := a.stremio.Connect(r.Context(), identity(u), b.Email, b.Password); err != nil {
		a.stremioFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "linked"})
}
func (a *API) stremioDisconnect(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if err := a.stremio.Disconnect(r.Context(), identity(u)); err != nil {
		a.stremioFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "disconnected"})
}
