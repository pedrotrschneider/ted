package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"net/http"
	"strings"
)

func (a *API) mappingOperation(w http.ResponseWriter, r *http.Request, u accounts.User) {
	kind := "series"
	if strings.Contains(r.Pattern, "/movies/") {
		kind = "movie"
	}
	if kind == "movies" {
		kind = "movie"
	}
	if (kind != "movie" && kind != "series") || !cinemeta.ValidID(r.PathValue("id")) {
		respond(w, 400, map[string]string{"error": "Invalid mapping target."})
		return
	}
	if a.Streaming == nil {
		respond(w, 503, map[string]string{"error": "TED media workers are unavailable."})
		return
	}
	op, err := a.Streaming.MappingStatus(r.Context(), r.PathValue("id"), kind)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, true) {
		respond(w, 200, op)
	}
}

func (a *API) mappingRetry(w http.ResponseWriter, r *http.Request, u accounts.User) {
	kind := "series"
	if strings.Contains(r.Pattern, "/movies/") {
		kind = "movie"
	}
	if !cinemeta.ValidID(r.PathValue("id")) {
		respond(w, 400, map[string]string{"error": "Invalid mapping target."})
		return
	}
	var input struct {
		Submission string `json:"submission_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if a.Streaming == nil {
		respond(w, 503, map[string]string{"error": "TED media workers are unavailable."})
		return
	}
	op, err := a.Streaming.RetryMapping(r.Context(), identity(u), r.PathValue("id"), kind, input.Submission)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	respond(w, 202, op)
}
