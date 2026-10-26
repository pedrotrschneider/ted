package httpapi

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
)

func (a *API) seriesMetadataProviderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/series/{id}/metadata-provider", a.protected(false, func(w http.ResponseWriter, r *http.Request, u accounts.User) { a.seriesMetadataProvider(w, r, u) }))
	mux.HandleFunc("POST /api/admin/series/{id}/metadata-provider", a.protected(true, func(w http.ResponseWriter, r *http.Request, u accounts.User) { a.changeSeriesMetadataProvider(w, r, u) }))
}
func (a *API) nativeSeriesMetadataProvider(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if r.Method == http.MethodPost {
		if g.User.Role != "admin" {
			respond(w, 403, map[string]string{"code": "forbidden", "error": "administrator required"})
			return
		}
		a.changeSeriesMetadataProvider(w, r, g.User)
		return
	}
	a.seriesMetadataProvider(w, r, g.User)
}
func (a *API) seriesMetadataProvider(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	v, err := a.Metadata.SeriesProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	snapshot, readErr := a.Metadata.Lookup(r.Context(), metadata.Request{Kind: "series", TitleID: r.PathValue("id")})
	if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
		a.metadataFailure(w, r, readErr)
		return
	}
	status := "new"
	code := ""
	if readErr == nil {
		status, code = snapshot.Status, snapshot.Error
	}
	respond(w, 200, struct {
		metadata.SeriesProvider
		CanChange bool   `json:"can_change"`
		Status    string `json:"status"`
		Error     string `json:"error_code"`
	}{v, u.Role == "admin", status, code})
}
func (a *API) changeSeriesMetadataProvider(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	var input struct {
		Provider string `json:"provider"`
		Expected string `json:"expected_provider"`
		metadata.OverrideDecision
	}
	if !decode(w, r, &input) {
		return
	}
	if !cinemeta.ValidID(r.PathValue("id")) || (input.Provider != "Tmdb" && input.Provider != "Tvdb") || (input.Expected != "Tmdb" && input.Expected != "Tvdb") || input.Provider == input.Expected {
		a.metadataFailure(w, r, metadata.ErrInvalid)
		return
	}
	// Provider changes reuse the canonical identity cache. Only a cold series
	// needs discovery; provider enrichment is admitted separately and lazily.
	show, err := a.catalog.CanonicalSeries(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		show, err = a.series.Series(r.Context(), r.PathValue("id"))
		if err == nil {
			err = a.catalog.SaveSchedule(r.Context(), show, time.Now())
		}
	}
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if show.ID != r.PathValue("id") || show.Type != "series" {
		a.catalogFailure(w, r, cinemeta.ErrIncomplete)
		return
	}
	v, err := a.Metadata.ChangeSeriesProvider(r.Context(), u.ID, show, input.Expected, input.Provider, input.OverrideDecision)
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	respond(w, 202, v)
}
