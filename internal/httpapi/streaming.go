package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/streaming"
	"errors"
	"net/http"
	"strings"
)

func (a *API) streamConfigured(w http.ResponseWriter, r *http.Request) bool {
	valid := cinemeta.ValidID(r.PathValue("id"))
	if strings.Contains(r.Pattern, "/episodes/") {
		valid = cinemeta.ValidVideoID(r.PathValue("id"))
	}
	if !valid {
		respond(w, 400, map[string]string{"error": "invalid media identity"})
		return false
	}
	if a.Streaming == nil {
		respond(w, 503, map[string]string{"code": "stream_unconfigured", "error": "TED media workers are unavailable."})
		return false
	}
	return true
}
func (a *API) streamStatus(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	state, err := a.Streaming.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !a.recheck(w, r, u, true) {
		return
	}
	respond(w, 200, state)
}
func (a *API) streamStart(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	if err := a.Streaming.Enqueue(r.Context(), identity(u), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "queued"})
}
func (a *API) streamCancel(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	if err := a.Streaming.Cancel(r.Context(), identity(u), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "cancelled"})
}

func (a *API) streamSeriesStart(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.streamConfigured(w, r) {
		return
	}
	var input struct {
		Season     *int `json:"season"`
		MappedOnly bool `json:"mapped_only"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Season != nil && *input.Season < 0 {
		respond(w, 400, map[string]string{"error": "invalid season"})
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
		respond(w, 400, map[string]string{"error": "No catalog episodes in this scope."})
		return
	}
	result, err := a.Streaming.EnqueueBatch(r.Context(), identity(u), ids, input.MappedOnly)
	if errors.Is(err, streaming.ErrNotReady) {
		respond(w, 409, map[string]string{"error": "Some episodes have no saved mapping. Save the missing mappings, or explicitly choose mapped episodes only."})
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 202, result)
}
