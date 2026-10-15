package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/watchstate"
	"errors"
	"net/http"
	"strconv"
)

func (a *API) watchFailure(w http.ResponseWriter, r *http.Request, err error) {
	code, status := "", 503
	switch {
	case errors.Is(err, watchstate.ErrInvalid):
		code = "watch_invalid"
		status = 400
	case errors.Is(err, watchstate.ErrConflict):
		code = "watch_conflict"
		status = 409
	case errors.Is(err, watchstate.ErrUnavailable):
		code = "watch_unavailable"
	}
	if code != "" {
		respond(w, status, map[string]string{"code": code})
		return
	}
	a.fail(w, r, err)
}

func (a *API) watchHistory(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var before int64
	var err error
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			a.watchFailure(w, r, watchstate.ErrInvalid)
			return
		}
	}
	v, err := a.watch.History(r.Context(), identity(u), before)
	if err != nil {
		a.watchFailure(w, r, err)
		return
	}
	respond(w, 200, v)
}

func (a *API) watchEdit(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var b watchstate.WriteRequest
	if !decode(w, r, &b) {
		return
	}
	id, err := a.watch.Edit(r.Context(), identity(u), r.PathValue("id"), b)
	if err != nil {
		a.watchFailure(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "accepted", "id": id})
}

func (a *API) watchTitle(w http.ResponseWriter, r *http.Request, u accounts.User) {
	id := r.PathValue("id")
	if !cinemeta.ValidID(id) && !cinemeta.ValidVideoID(id) {
		a.watchFailure(w, r, watchstate.ErrInvalid)
		return
	}
	if r.Method == "GET" {
		v, err := a.watch.Title(r.Context(), identity(u), id)
		if err != nil {
			a.watchFailure(w, r, err)
			return
		}
		respond(w, 200, v)
		return
	}
	var b struct {
		Forget   bool   `json:"forget_history"`
		Episode  string `json:"episode"`
		Version  string `json:"version"`
		Season   *int   `json:"season"`
		Played   *bool  `json:"played"`
		DateMode string `json:"date_mode"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Version == "" || b.Played == nil || (b.Season != nil && *b.Season < 0) {
		a.watchFailure(w, r, watchstate.ErrInvalid)
		return
	}
	if err := a.watch.SetTitle(r.Context(), identity(u), id, b.Version, b.Episode, b.Season, *b.Played, b.DateMode, b.Forget); err != nil {
		a.watchFailure(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "accepted"})
}
