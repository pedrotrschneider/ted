package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/watchstate"
	"errors"
	"net/http"
	"strconv"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/realdebrid"
)

type MovieReader interface {
	Browse(context.Context, string, int, cinemeta.Filters) (cinemeta.Page, error)
	Movie(context.Context, string) (cinemeta.Movie, error)
}

func (a *API) movieBrowse(w http.ResponseWriter, r *http.Request, u accounts.User) {
	skip := 0
	if raw := r.URL.Query().Get("skip"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return
		}
		skip = n
	}
	filters, err := catalogFilters(r)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	page, err := a.movies.Browse(r.Context(), r.URL.Query().Get("search"), skip, filters)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if !a.recheck(w, r, u, false) {
		return
	}
	if err = a.webArtwork(r.Context(), page.Movies); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, page)
}
func (a *API) movieDetail(w http.ResponseWriter, r *http.Request, u accounts.User) {
	movie, err := a.movies.Movie(r.Context(), r.PathValue("id"))
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	raw, err := json.Marshal(movie)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err = a.watch.RememberCatalog(r.Context(), identity(u), movie.ID, "movie", []watchstate.CatalogEntry{{ID: movie.ID, Metadata: raw}}, 0); err != nil {
		a.fail(w, r, err)
		return
	}
	if !a.recheck(w, r, u, false) {
		return
	}
	if err = a.catalog.EnrichTitle(r.Context(), movie); err != nil {
		a.fail(w, r, err)
		return
	}
	titles := []cinemeta.Movie{movie}
	if err = a.webArtwork(r.Context(), titles); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, titles[0])
}
func (a *API) mappingGet(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !cinemeta.ValidID(r.PathValue("id")) {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	m, err := a.catalog.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !a.recheck(w, r, u, true) {
		return
	}
	if m != nil {
		titles := []cinemeta.Movie{m.Movie}
		if err = a.webArtwork(r.Context(), titles); err != nil {
			a.fail(w, r, err)
			return
		}
		m.Movie = titles[0]
	}
	respond(w, 200, map[string]any{"mapping": m})
}

// savedMovies exposes catalog metadata only; source provenance remains admin-only.
func (a *API) savedMovies(w http.ResponseWriter, r *http.Request, u accounts.User) {
	after := r.URL.Query().Get("after")
	if after != "" && !cinemeta.ValidID(after) {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT c.cinemeta_id,c.metadata FROM catalog_media c WHERE c.kind='movie' AND c.cinemeta_id>$1 AND EXISTS(SELECT 1 FROM media_mappings m WHERE m.movie_id=c.id) ORDER BY c.cinemeta_id LIMIT 101`, after)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	page := struct {
		Movies    []cinemeta.Movie `json:"movies"`
		NextAfter string           `json:"next_after,omitempty"`
	}{Movies: []cinemeta.Movie{}}
	for rows.Next() {
		var id string
		var movie cinemeta.Movie
		if err = rows.Scan(&id, &movie); err != nil {
			a.fail(w, r, err)
			return
		}
		page.Movies = append(page.Movies, persistedCatalogTitle(id, "movie", movie))
	}
	if err = rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	if len(page.Movies) > 100 {
		page.Movies = page.Movies[:100]
		page.NextAfter = page.Movies[99].ID
	}
	if a.recheck(w, r, u, false) {
		if err = a.webArtwork(r.Context(), page.Movies); err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, page)
	}
}

func (a *API) mappingList(w http.ResponseWriter, r *http.Request, u accounts.User) {
	after := r.URL.Query().Get("after")
	if after != "" && !cinemeta.ValidID(after) {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	page, err := a.catalog.List(r.Context(), after)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !a.recheck(w, r, u, true) {
		return
	}
	respond(w, 200, page)
}
func (a *API) mappingSave(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var selected catalog.Selection
	if !decode(w, r, &selected) {
		return
	}
	if a.Streaming == nil {
		respond(w, 503, map[string]string{"error": "TED media workers are unavailable."})
		return
	}
	op, err := a.Streaming.SubmitMapping(r.Context(), identity(u), r.PathValue("id"), "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: r.PathValue("id"), Selection: selected}}})
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	respond(w, 202, op)
}

func (a *API) recheck(w http.ResponseWriter, r *http.Request, u accounts.User, admin bool) bool {
	current, err := a.accounts.Current(r.Context(), identity(u))
	if err != nil {
		a.fail(w, r, err)
		return false
	}
	if admin && current.Role != "admin" {
		a.fail(w, r, accounts.ErrForbidden)
		return false
	}
	return true
}
func (a *API) catalogFailure(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 502, "catalog_unavailable", "Cinemeta is unavailable. Try again later."
	switch {
	case errors.Is(err, cinemeta.ErrInvalid), errors.Is(err, catalog.ErrInvalid):
		status, code, message = 400, "catalog_invalid", "Invalid title, episode, or file selection."
	case errors.Is(err, cinemeta.ErrNotFound):
		status, code, message = 404, "catalog_not_found", "This title is not available from Cinemeta."
	case errors.Is(err, cinemeta.ErrIncomplete):
		code, message = "catalog_incomplete", "Cinemeta returned incomplete data. Try again later."
	case errors.Is(err, catalog.ErrChanged):
		status, code, message = 409, "mapping_changed", "The source changed. Refresh its files and select again."
	case errors.Is(err, catalog.ErrConflict):
		status, code, message = 409, "mapping_conflict", "An existing assignment or library category conflicts with this selection. Existing mappings were preserved."
	case errors.Is(err, cinemeta.ErrUnavailable):
	case errors.Is(err, realdebrid.ErrNotConfigured), errors.Is(err, realdebrid.ErrCredentials), errors.Is(err, realdebrid.ErrUnavailable), errors.Is(err, realdebrid.ErrLimited), errors.Is(err, realdebrid.ErrIncomplete), errors.Is(err, realdebrid.ErrNotFound), errors.Is(err, realdebrid.ErrInvalid):
		a.rdFailure(w, r, err)
		return
	default:
		a.fail(w, r, err)
		return
	}
	a.logger.Warn("catalog operation failed", "operation", r.Pattern, "code", code)
	respond(w, status, map[string]string{"code": code, "error": message})
}
