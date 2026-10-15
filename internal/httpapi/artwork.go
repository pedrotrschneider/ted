package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/metadata"
)

func (a *API) artworkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/titles/{kind}/{id}/artwork", a.protected(true, a.titleArtwork))
	mux.HandleFunc("GET /api/admin/metadata/items/{id}/artwork-choices", a.protected(true, a.artworkChoices))
	mux.HandleFunc("POST /api/admin/artwork/{id}/select", a.protected(true, a.artworkSelect))
	mux.HandleFunc("POST /api/admin/artwork/{id}/upload", a.protected(true, a.artworkUpload))
	mux.HandleFunc("GET /api/metadata/images/{id}", a.protected(false, a.webMetadataImage))
}
func (a *API) titleArtwork(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	images, err := a.Metadata.TitleArtwork(r.Context(), r.PathValue("kind"), r.PathValue("id"), r.URL.Query().Get("retry") == "1")
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	v, err := a.Metadata.Lookup(r.Context(), metadata.Request{Kind: r.PathValue("kind"), TitleID: r.PathValue("id")})
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	pending, err := a.Metadata.ArtworkMetadataPending(r.Context(), v)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, true) {
		respond(w, 200, map[string]any{"images": images, "metadata_pending": pending, "metadata_cached": v.FetchedAt != nil, "metadata_status": v.Status, "metadata_error": v.Error})
	}
}
func (a *API) artworkChoices(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	v, err := a.Metadata.RequestArtworkChoices(r.Context(), r.PathValue("id"), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	if a.recheck(w, r, u, true) {
		respond(w, 200, v)
	}
}
func (a *API) artworkSelect(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	var b struct {
		Generation int64  `json:"generation"`
		Choice     string `json:"choice_id"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !a.recheck(w, r, u, true) {
		return
	}
	if err := a.Metadata.SelectArtwork(r.Context(), u.ID, r.PathValue("id"), b.Generation, b.Choice); err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "queued"})
}
func (a *API) artworkUpload(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) {
		return
	}
	generation, err := strconv.ParseInt(r.URL.Query().Get("generation"), 10, 64)
	if err != nil || generation < 1 {
		a.metadataFailure(w, r, metadata.ErrInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, metadata.MaxArtworkBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		respond(w, 413, map[string]string{"code": "artwork_too_large", "error": "Artwork must be at most 20 MiB."})
		return
	}
	if !a.recheck(w, r, u, true) {
		return
	}
	if err = a.Metadata.UploadArtwork(r.Context(), u.ID, r.PathValue("id"), generation, raw); err != nil {
		a.metadataFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "saved"})
}
func (a *API) webMetadataImage(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !a.metadataAvailable(w) || !a.recheck(w, r, u, false) {
		return
	}
	a.serveMetadataImage(w, r, r.PathValue("id"))
}
