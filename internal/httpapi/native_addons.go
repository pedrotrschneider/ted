package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/realdebrid"
)

var nativeSourceKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a *API) nativeAddons(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	refresh := r.URL.Query().Get("refresh")
	if refresh != "" && refresh != "1" {
		a.nativeSourceFailure(w, r, addons.ErrUnsupported)
		return
	}
	view, err := a.addons.Collection(r.Context(), g.User.ID, refresh == "1")
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, view)
}
func (a *API) nativeSources(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Media          catalog.MediaIdentity `json:"media"`
		Previous       string                `json:"previous_session"`
		Provider       string                `json:"provider"`
		PreferredAddon string                `json:"preferred_addon"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !input.Media.Valid() || (input.Provider != "" && input.Provider != "stremio" && input.Provider != "mapped") ||
		input.PreferredAddon != "" && (input.Provider != "stremio" || !nativeSourceKey.MatchString(input.PreferredAddon)) {
		a.nativeSourceFailure(w, r, addons.ErrUnsupported)
		return
	}
	var mapped, ready, local bool
	var filename string
	err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id WHERE c.kind=$1 AND c.cinemeta_id=$2),
 EXISTS(SELECT 1 FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id JOIN streaming_items s ON s.movie_id=c.id AND s.mapping_id=m.id AND s.published AND s.phase='ready' WHERE c.kind=$1 AND c.cinemeta_id=$2),
 EXISTS(SELECT 1 FROM catalog_media c JOIN local_files l ON l.movie_id=c.id AND l.available WHERE c.kind=$1 AND c.cinemeta_id=$2),
 COALESCE((SELECT m.file_path FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id WHERE c.kind=$1 AND c.cinemeta_id=$2),'')`, input.Media.Kind, input.Media.ID).Scan(&mapped, &ready, &local, &filename)
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	if mapped && input.Provider != "stremio" {
		source := "mapped_rd"
		if local {
			source = "local"
		}
		filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
		a.respondNativeCatalog(w, r, map[string]any{"media": input.Media, "mode": "mapped", "mapped_ready": ready, "mapped_file_name": filename, "source": source, "sources": []addons.Choice{}, "issues": []addons.Issue{}})
		return
	}
	if input.Provider == "mapped" {
		a.respondNativeCatalog(w, r, map[string]any{"media": input.Media, "mode": "addons", "mapped_ready": false, "sources": []addons.Choice{}, "issues": []addons.Issue{}})
		return
	}
	var page addons.SourcePage
	if input.Provider == "stremio" {
		page, err = a.addons.SelectedSourcesPrioritizing(r.Context(), a.NativeAuth, g, input.Media, input.Previous, input.PreferredAddon)
	} else {
		page, err = a.addons.Sources(r.Context(), a.NativeAuth, g, input.Media, input.Previous)
	}
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, map[string]any{"media": page.Media, "mode": "addons", "mapped_ready": false, "sources": page.Sources, "issues": page.Issues, "auto_select": page.AutoSelect, "addons": page.Addons, "preferred_source": page.Preferred})
}
func (a *API) nativeSubtitle(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		a.nativeSourceFailure(w, r, addons.ErrUnsupported)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	data, mime, err := a.addons.Subtitle(ctx, g, r.PathValue("id"), index)
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (a *API) nativeSourceFailure(w http.ResponseWriter, r *http.Request, err error) {
	code, status := "", 503
	switch {
	case errors.Is(err, realdebrid.ErrArchive):
		code, status = "source_archive", 422
	case errors.Is(err, realdebrid.ErrInfringing):
		code, status = "rd_infringing", 422
	case errors.Is(err, realdebrid.ErrRejected):
		code, status = "rd_rejected", 422
	case errors.Is(err, realdebrid.ErrPending), errors.Is(err, realdebrid.ErrCachePending):
		code, status = "rd_source_pending", 409
	case errors.Is(err, realdebrid.ErrNotConfigured):
		code = "rd_not_configured"
	case errors.Is(err, realdebrid.ErrCredentials):
		code = "rd_credentials"
	case errors.Is(err, realdebrid.ErrLimited):
		code, status = "rd_limited", 429
	case errors.Is(err, realdebrid.ErrMismatch):
		code, status = "source_selection_required", 409
	case errors.Is(err, realdebrid.ErrUnavailable), errors.Is(err, realdebrid.ErrIncomplete), errors.Is(err, realdebrid.ErrNotFound):
		code = "rd_unavailable"
	case errors.Is(err, addons.ErrPlaceholder):
		code, status = "source_placeholder", 422
	case errors.Is(err, addons.ErrNotLinked):
		code, status = "stremio_not_linked", 409
	case errors.Is(err, addons.ErrChanged):
		code, status = "stremio_link_changed", 409
	case errors.Is(err, addons.ErrCredentials):
		code, status = "stremio_credentials_rejected", 409
	case errors.Is(err, addons.ErrSelection):
		code, status = "source_selection_required", 409
	case errors.Is(err, addons.ErrUnsupported):
		code, status = "source_unsupported", 422
	case errors.Is(err, addons.ErrPreparation):
		code = "source_preparation_unavailable"
	case errors.Is(err, addons.ErrUnavailable), errors.Is(err, addons.ErrIncomplete):
		code = "addon_unavailable"
	default:
		a.nativePlaybackFailure(w, r, err)
		return
	}
	if a.logger != nil {
		a.logger.WarnContext(r.Context(), "native source request failed", "operation", r.Pattern, "status", status, "code", code)
	}
	respond(w, status, map[string]string{"code": code})
}

func (a *API) nativeFavorite(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Favorite *bool `json:"favorite"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Favorite == nil {
		a.nativeSourceFailure(w, r, addons.ErrUnsupported)
		return
	}
	result, err := a.addons.SetFavorite(r.Context(), a.NativeAuth, g, r.PathValue("id"), *input.Favorite)
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, result)
}
func (a *API) addonPolicies(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if r.Method == "GET" {
		status, err := a.addons.ProviderStatus(r.Context())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if status.Connected {
			if _, err = a.addons.Collection(r.Context(), u.ID, r.URL.Query().Get("refresh") == "1"); err != nil {
				a.nativeSourceFailure(w, r, err)
				return
			}
		}
		values, err := a.addons.Policies(r.Context())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, map[string]any{"addons": values})
		return
	}
	var input struct {
		ID       string `json:"id"`
		Enabled  *bool  `json:"enabled"`
		Revision int64  `json:"revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		respond(w, 400, map[string]string{"code": "settings_invalid"})
		return
	}
	value, err := a.addons.UpdatePolicy(r.Context(), identity(u), input.ID, *input.Enabled, input.Revision)
	if err != nil {
		switch {
		case errors.Is(err, addons.ErrPolicyConflict):
			respond(w, 409, map[string]string{"code": "settings_changed"})
		case errors.Is(err, addons.ErrUnsupported):
			respond(w, 400, map[string]string{"code": "settings_invalid"})
		case errors.Is(err, accounts.ErrForbidden):
			respond(w, 403, map[string]string{"code": "forbidden"})
		default:
			a.fail(w, r, err)
		}
		return
	}
	respond(w, 200, value)
}
