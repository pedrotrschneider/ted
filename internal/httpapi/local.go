package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/streaming"
	"errors"
	"net/http"
)

func (a *API) localStatus(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	state, err := a.Streaming.LocalStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !a.recheck(w, r, u, false) {
		return
	}
	respond(w, 200, state)
}
func (a *API) localStart(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	if err := a.Streaming.Download(r.Context(), identity(u), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "queued"})
}
func (a *API) localCancel(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	if err := a.Streaming.CancelDownload(r.Context(), identity(u), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "cancelled"})
}
func (a *API) localRemove(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	if err := a.Streaming.RemoveLocal(r.Context(), identity(u), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "removing"})
}

func (a *API) localSeriesStart(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	var input struct {
		Season    *int `json:"season"`
		ReadyOnly bool `json:"ready_only"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Season != nil && *input.Season < 0 {
		respond(w, 400, map[string]string{"error": "Invalid season."})
		return
	}
	show, err := a.series.Series(r.Context(), r.PathValue("id"))
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	ids := []string{}
	for _, episode := range show.Episodes {
		if input.Season == nil || episode.Season == *input.Season {
			ids = append(ids, episode.ID)
		}
	}
	if len(ids) == 0 {
		respond(w, 400, map[string]string{"error": "No episodes in this scope."})
		return
	}
	result, err := a.Streaming.DownloadBatch(r.Context(), identity(u), ids, input.ReadyOnly)
	if errors.Is(err, streaming.ErrNotReady) {
		respond(w, 409, map[string]string{"error": "Some episodes are not ready. Finish their mappings, or select ready episodes only. No downloads were queued."})
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 202, result)
}
