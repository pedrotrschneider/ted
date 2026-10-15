package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/metadata"

	"github.com/jackc/pgx/v5"
)

func (a *API) nativeMetadataRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/client/v1/metadata/configuration", a.nativeProtected(a.nativeMetadataConfiguration))
	mux.HandleFunc("GET /api/client/v1/metadata/items/{id}", a.nativeProtected(a.nativeMetadataItem))
	mux.HandleFunc("POST /api/client/v1/metadata/items/{id}/refresh", a.nativeProtected(a.nativeMetadataRefresh))
	mux.HandleFunc("GET /api/client/v1/metadata/people/{id}", a.nativeProtected(a.nativeMetadataPerson))
	mux.HandleFunc("GET /api/client/v1/metadata/people/{id}/filmography", a.nativeProtected(a.nativeMetadataFilmography))
	mux.HandleFunc("GET /api/client/v1/metadata/images/{id}", a.nativeProtected(a.nativeMetadataImage))
	mux.HandleFunc("GET /api/client/v1/metadata/items/{id}/images/{type}", a.nativeProtected(a.nativeMetadataItemImage))
	mux.HandleFunc("GET /api/client/v1/titles/series/{id}/seasons/{season}/metadata", a.nativeProtected(a.nativeMetadataSeason))
}

func (a *API) enrichNativeDetail(r *http.Request, d *nativeTitleDetail) {
	if a.Metadata == nil {
		return
	}
	req := metadata.Request{Kind: d.Media.Kind, TitleID: d.Media.ID}
	provider := "Tmdb"
	fallback := metadata.CinemetaSeed(d.Metadata.Movie)
	if d.Media.Kind == "series" {
		provider = "Tvdb"
	}
	if d.Media.Kind == "episode" {
		if d.Parent == nil || d.Metadata.Episode == nil {
			return
		}
		ep := d.Metadata.Episode
		provider = "Tvdb"
		req.TitleID = d.Parent.ID
		req.Season, req.Number, req.EpisodeName, req.EpisodeDate = ep.Season, ep.Number, ep.Name, ep.ReleaseDate
		fallback = metadata.Item{Type: "Episode", Name: ep.Name, Overview: ep.Description, IndexNumber: &ep.Number, ParentIndexNumber: &ep.Season}
	}
	snapshot, err := a.Metadata.Ensure(r.Context(), provider, req, fallback)
	if err != nil {
		d.Issues = append(d.Issues, nativeCatalogIssue{Code: "metadata_unavailable"})
		return
	}
	d.Enrichment = &snapshot
}

func (a *API) metadataAvailable(w http.ResponseWriter) bool {
	if a.Metadata != nil {
		return true
	}
	respond(w, 503, map[string]string{"code": "metadata_not_configured", "error": "native metadata is not configured"})
	return false
}
func (a *API) nativeMetadataConfiguration(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	tmdb, err := a.Metadata.Configured(r.Context(), "Tmdb")
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	tvdb, err := a.Metadata.Configured(r.Context(), "Tvdb")
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"format": "TED descriptive metadata", "format_version": "1", "movies": map[string]any{"provider": "Tmdb", "configured": tmdb}, "shows": map[string]any{"provider": "Tvdb", "configured": tvdb}, "refresh_requires_admin": true, "attribution": []map[string]string{{"provider": "TMDb", "url": "https://www.themoviedb.org", "notice": "This product uses the TMDB API but is not endorsed or certified by TMDB."}, {"provider": "TheTVDB", "url": "https://thetvdb.com", "notice": "TV metadata provided by TheTVDB."}}})
}
func (a *API) nativeMetadataItem(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	v, err := a.Metadata.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, v)
}
func (a *API) nativeMetadataPerson(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	v, err := a.Metadata.RequestPerson(r.Context(), r.PathValue("id"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, v)
}
func (a *API) nativeMetadataRefresh(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if g.User.Role != "admin" {
		respond(w, 403, map[string]string{"code": "forbidden", "error": "administrator required"})
		return
	}
	if !a.metadataAvailable(w) {
		return
	}
	v, err := a.Metadata.Refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, v)
}
func (a *API) nativeMetadataFilmography(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	if _, err := a.Metadata.RequestPerson(r.Context(), r.PathValue("id")); err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	v, err := a.Metadata.Filmography(r.Context(), g.User.ID, r.PathValue("id"), r.URL.Query().Get("after"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	ids := []string{}
	for _, entry := range v.Items {
		if entry.Media != nil && entry.Media.Kind == "series" {
			ids = append(ids, entry.Media.ID)
		}
	}
	counts, err := a.catalog.ReleasedCounts(r.Context(), ids, g.User.ID, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	for i := range v.Items {
		entry := &v.Items[i]
		if entry.Media != nil && entry.Media.Kind == "series" {
			if c := counts[entry.Media.ID]; c.Known {
				entry.EpisodeCounts = &struct {
					Watched int `json:"watched"`
					Total   int `json:"total"`
				}{c.Watched, c.Available}
			}
		}
	}
	media := []catalog.MediaIdentity{}
	for _, entry := range v.Items {
		if entry.Media != nil {
			media = append(media, catalog.MediaIdentity{Kind: entry.Media.Kind, ID: entry.Media.ID})
		}
	}
	joint, err := a.jointWatches(r.Context(), g.User.ID, media)
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	type entryView struct {
		metadata.FilmographyEntry
		JointWatches []jointWatch `json:"joint_watches,omitempty"`
	}
	items := make([]entryView, len(v.Items))
	for i, entry := range v.Items {
		items[i].FilmographyEntry = entry
		if entry.Media != nil {
			items[i].JointWatches = joint[entry.Media.Kind+"/"+entry.Media.ID]
		}
	}
	// Preserve the optional cursor contract when adding joint-watch decorations.
	// An empty cursor is not a valid continuation token for native clients.
	a.respondNativeCatalog(w, r, struct {
		Items     []entryView `json:"items"`
		NextAfter string      `json:"next_after,omitempty"`
	}{Items: items, NextAfter: v.NextAfter})
}
func (a *API) nativeMetadataSeason(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil || season < 0 || season > 9999 {
		a.metadataFailure(w, r, metadata.ErrInvalid)
		return
	}
	show, _, err := a.loadNativeSeries(r.Context(), r.PathValue("id"), false)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	found := false
	for _, ep := range show.Episodes {
		if ep.Season == season {
			found = true
			break
		}
	}
	if !found {
		a.metadataFailure(w, r, metadata.ErrMissing)
		return
	}
	req := metadata.Request{Kind: "season", TitleID: show.ID, Season: season}
	v, err := a.Metadata.Ensure(r.Context(), "Tvdb", req, metadata.Item{Type: "Season", Name: "Season " + strconv.Itoa(season), IndexNumber: &season})
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, v)
}
func (a *API) nativeMetadataItemImage(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	index := 0
	if raw := r.URL.Query().Get("index"); raw != "" {
		var err error
		index, err = strconv.Atoi(raw)
		if err != nil || index < 0 || index > 100 {
			a.metadataFailure(w, r, metadata.ErrInvalid)
			return
		}
	}
	v, err := a.Metadata.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	for _, img := range v.Images {
		if img.Type == r.PathValue("type") && img.Index == index {
			a.serveMetadataImage(w, r, img.ID)
			return
		}
	}
	a.metadataFailure(w, r, metadata.ErrMissing)
}
func (a *API) nativeMetadataImage(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if !a.metadataAvailable(w) {
		return
	}
	a.serveMetadataImage(w, r, r.PathValue("id"))
}
func (a *API) serveMetadataImage(w http.ResponseWriter, r *http.Request, id string) {
	asset, err := a.Metadata.RequestImage(r.Context(), id, false)
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	f, mime, tag, err := a.Metadata.ImageFile(r.Context(), id)
	if errors.Is(err, metadata.ErrMissing) {
		if asset.Status == "failed" || asset.Error != "" {
			respond(w, 502, asset)
			return
		}
		w.Header().Set("Retry-After", "1")
		respond(w, 202, asset)
		return
	}
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("ETag", `"`+tag+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, id, time.Time{}, f)
}
func (a *API) metadataFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, metadata.ErrArtworkForbidden):
		respond(w, 403, map[string]string{"code": "forbidden", "error": "administrator required"})
	case errors.Is(err, metadata.ErrArtworkChanged):
		respond(w, 409, map[string]string{"code": "artwork_changed", "error": "Artwork changed. Refresh the picker before saving."})
	case errors.Is(err, metadata.ErrImage):
		respond(w, 400, map[string]string{"code": "artwork_invalid", "error": "Choose a valid JPEG, PNG or GIF up to 20 MiB and 40 megapixels."})
	case errors.Is(err, metadata.ErrUnconfigured):
		respond(w, 503, map[string]string{"code": "metadata_not_configured", "error": "Configure metadata providers and artwork storage."})
	case errors.Is(err, metadata.ErrInvalid):
		respond(w, 400, map[string]string{"code": "invalid_metadata_request", "error": "invalid metadata request"})
	case errors.Is(err, metadata.ErrMissing), errors.Is(err, pgx.ErrNoRows):
		respond(w, 404, map[string]string{"code": "metadata_not_found", "error": "metadata not found"})
	default:
		a.nativeFail(w, r, err)
	}
}
