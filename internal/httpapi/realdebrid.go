package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/realdebrid"
)

type RDReader interface {
	Configured() bool
	List(context.Context, int) (realdebrid.Page, error)
	Torrent(context.Context, string) (realdebrid.Detail, error)
}

func (a *API) SetRDInventory(s *realdebrid.Inventory) { a.RDInventory = s; a.rd = s; a.addons.RD = s }
func (a *API) rdCache(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if a.RDInventory == nil {
		a.rdFailure(w, r, realdebrid.ErrNotConfigured)
		return
	}
	if r.Method == "POST" {
		var input struct {
			Full bool `json:"full"`
		}
		if !decode(w, r, &input) {
			return
		}
		if e := a.RDInventory.Request(r.Context(), identity(u), input.Full); e != nil {
			a.rdFailure(w, r, e)
			return
		}
	}
	v, e := a.RDInventory.Status(r.Context())
	if e != nil {
		a.rdFailure(w, r, e)
		return
	}
	current, e := a.accounts.Current(r.Context(), identity(u))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if current.Role != "admin" {
		a.fail(w, r, accounts.ErrForbidden)
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 202
	}
	respond(w, status, v)
}

func (a *API) rdStatus(w http.ResponseWriter, r *http.Request, _ accounts.User) {
	if configured, ok := a.rd.(interface {
		Configuration(context.Context) (bool, error)
	}); ok {
		value, err := configured.Configuration(r.Context())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, map[string]bool{"configured": value})
		return
	}
	respond(w, 200, map[string]bool{"configured": a.rd != nil && a.rd.Configured()})
}
func (a *API) rdList(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if a.rd == nil {
		a.rdFailure(w, r, realdebrid.ErrNotConfigured)
		return
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10000 {
			a.rdFailure(w, r, realdebrid.ErrInvalid)
			return
		}
		page = n
	}
	result, err := a.rd.List(r.Context(), page)
	if err != nil {
		a.rdFailure(w, r, err)
		return
	}
	// Provider calls can outlive a permission change; recheck before releasing account data.
	current, err := a.accounts.Current(r.Context(), identity(u))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if current.Role != "admin" {
		a.fail(w, r, accounts.ErrForbidden)
		return
	}
	respond(w, 200, result)
}
func (a *API) rdTorrent(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if a.rd == nil {
		a.rdFailure(w, r, realdebrid.ErrNotConfigured)
		return
	}
	result, err := a.rd.Torrent(r.Context(), r.PathValue("id"))
	if err != nil {
		a.rdFailure(w, r, err)
		return
	}
	current, err := a.accounts.Current(r.Context(), identity(u))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if current.Role != "admin" {
		a.fail(w, r, accounts.ErrForbidden)
		return
	}
	respond(w, 200, result)
}
func (a *API) rdFailure(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 502, "rd_unavailable", "Real-Debrid is unavailable. Try again later."
	switch {
	case errors.Is(err, realdebrid.ErrNotConfigured):
		status, code, message = 503, "rd_not_configured", "An administrator can configure Real-Debrid in Your account → Server providers."
	case errors.Is(err, realdebrid.ErrCredentials):
		code, message = "rd_credentials", "Real-Debrid rejected the configured key. Check the server credential and RD account status."
	case errors.Is(err, realdebrid.ErrLimited):
		status, code, message = 429, "rd_limited", "Real-Debrid is rate limiting requests. Wait before refreshing."
	case errors.Is(err, realdebrid.ErrIncomplete):
		code, message = "rd_incomplete", "Real-Debrid returned incomplete data. The previous results are retained; refresh to try again."
	case errors.Is(err, realdebrid.ErrNotFound):
		status, code, message = 404, "rd_not_found", "This torrent is no longer available in the connected RD account."
	case errors.Is(err, realdebrid.ErrInvalid):
		status, code, message = 400, "rd_invalid", "Invalid torrent or page."
	case errors.Is(err, realdebrid.ErrCachePending):
		status, code, message = 503, "rd_cache_pending", "Real-Debrid inventory is being built. The previous results remain available; try again shortly."
	}
	// Only our bounded codes enter logs. Provider messages, keys and links never do.
	a.logger.Warn("Real-Debrid read failed", "operation", r.Pattern, "code", code)
	respond(w, status, map[string]string{"code": code, "error": message})
}
