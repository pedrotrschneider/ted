package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"errors"
	"net/http"
)

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	required, err := a.accounts.SetupRequired(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]bool{"setup_required": required})
}

func (a *API) setupAdmin(w http.ResponseWriter, r *http.Request) {
	// Avoid password hashing once setup is closed. Bootstrap rechecks under its
	// database lock, so this preflight is not the authorization boundary.
	required, err := a.accounts.SetupRequired(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !required {
		setupComplete(w)
		return
	}
	var input struct{ Username, Password string }
	if !decode(w, r, &input) {
		return
	}
	_, err = a.accounts.Bootstrap(r.Context(), input.Username, input.Password)
	if errors.Is(err, accounts.ErrConflict) {
		setupComplete(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, http.StatusCreated, map[string]string{"status": "administrator created"})
}
func setupComplete(w http.ResponseWriter) {
	respond(w, http.StatusConflict, map[string]string{"error": "setup already completed", "code": "setup_completed"})
}
