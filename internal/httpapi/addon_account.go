package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/stremio"
	"net/http"
)

func (a *API) addonAccount(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "POST" {
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decode(w, r, &input) {
			return
		}
		session, err := stremio.New().Login(r.Context(), input.Email, input.Password)
		if err != nil {
			a.stremioFailure(w, r, err)
			return
		}
		if err = a.addons.ConfigureProvider(r.Context(), identity(u), &session); err != nil {
			a.fail(w, r, err)
			return
		}
	} else if r.Method == "DELETE" {
		if err := a.addons.ConfigureProvider(r.Context(), identity(u), nil); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	status, err := a.addons.ProviderStatus(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, status)
}
