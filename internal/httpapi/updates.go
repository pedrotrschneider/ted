package httpapi

import (
	tedversion "entertainmentdistrict"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/updates"
	"net/http"
)

func (a *API) updateStatus(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var snoozed bool
	if err := a.pool.QueryRow(r.Context(), "SELECT update_notice_after > now() FROM users WHERE id=$1", u.ID).Scan(&snoozed); err != nil {
		a.fail(w, r, err)
		return
	}
	version := ""
	if !snoozed {
		latest, err := a.updateChecker.Version(r.Context(), tedversion.Release())
		if err != nil {
			respond(w, 503, map[string]string{"error": "update check unavailable"})
			return
		}
		if updates.Newer(latest, tedversion.Release()) {
			version = latest
		}
	}
	respond(w, 200, map[string]any{"version": version, "snoozed": snoozed})
}
func (a *API) dismissUpdate(w http.ResponseWriter, r *http.Request, u accounts.User) {
	// Every dismissal is bounded to 24 hours, and covers all versions.
	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(r.Context(), tx, identity(u), true); err != nil {
		a.fail(w, r, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE users SET update_notice_after=now()+interval '1 day' WHERE id=$1", u.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}

	respond(w, 200, map[string]string{"status": "dismissed"})
}
