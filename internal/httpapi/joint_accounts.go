package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"net/http"
)

func (a *API) jointAccounts(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "POST" {
		var b struct {
			Action string `json:"action"`
			Target int64  `json:"target"`
			Color  string `json:"color"`
		}
		if !decode(w, r, &b) {
			return
		}
		if err := a.accounts.ChangeJoint(r.Context(), identity(u), b.Action, b.Target, b.Color); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	v, err := a.accounts.JointSettings(r.Context(), identity(u))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, v)
}
