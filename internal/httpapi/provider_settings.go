package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/settings"
	"errors"
	"net/http"
)

func (a *API) providerSettings(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if a.Providers == nil {
		respond(w, 503, map[string]string{"code": "settings_unavailable", "error": "provider settings unavailable"})
		return
	}
	var status settings.Status
	var err error
	if r.Method == "PUT" {
		var patch settings.Patch
		if !decode(w, r, &patch) {
			return
		}
		status, err = a.Providers.Update(r.Context(), identity(u), patch)
		if err == nil && patch.RDKey != nil && status.RD && a.RDInventory != nil {
			if e := a.RDInventory.Request(r.Context(), identity(u), false); e != nil {
				a.logger.Warn("RD cache refresh could not be scheduled after credential save", "operation", "rd_inventory_enqueue")
			}
		}
	} else {
		status, err = a.Providers.Status(r.Context())
	}
	if err != nil {
		switch {
		case errors.Is(err, settings.ErrInvalid):
			respond(w, 400, map[string]string{"code": "settings_invalid", "error": "invalid provider settings"})
		case errors.Is(err, settings.ErrConflict):
			respond(w, 409, map[string]string{"code": "settings_changed", "error": "provider settings changed; reload before saving"})
		default:
			a.fail(w, r, err)
		}
		return
	}
	respond(w, 200, status)
}
