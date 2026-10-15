package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/streaming"
	"errors"
	"net/http"
)

func (a *API) nativeDownloadWatch(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Media []catalog.MediaIdentity `json:"media"`
	}
	if !decodeLimit(w, r, &input, 32<<10) {
		return
	}
	items, err := a.watch.NativeDownloadWatches(r.Context(), g.User.ID, input.Media)
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) downloadDescriptor(r *http.Request, g accounts.NativeGrant, id string) (streaming.NativeDownload, error) {
	if a.Streaming == nil {
		return streaming.NativeDownload{}, streaming.ErrUnavailable
	}
	d, err := a.addons.NativeDownload(r.Context(), g, id)
	if errors.Is(err, addons.ErrNotAddon) {
		return a.Streaming.NativeDownload(r.Context(), g, id)
	}
	return d, err
}
func (a *API) nativeDownloadResolve(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	d, err := a.downloadDescriptor(r, g, input.SessionID)
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	// A bounded probe may finish after sign-out/password change. Recheck the
	// native grant before releasing a new authenticated download descriptor.
	tx, e := a.pool.Begin(r.Context())
	if e != nil {
		a.nativeSourceFailure(w, r, e)
		return
	}
	defer database.Rollback(tx)
	if e = a.NativeAuth.AuthorizeMutation(r.Context(), tx, g); e != nil {
		a.nativeSourceFailure(w, r, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.nativeSourceFailure(w, r, e)
		return
	}
	respond(w, 200, d)
}
func (a *API) nativeDownloadMedia(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	d, err := a.downloadDescriptor(r, g, r.PathValue("id"))
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	// Require a strong pin on every transfer, including retries from native
	// download workers. Never return a different release or a whole file when
	// a client asked to append a range from its saved descriptor.
	if r.Header.Get("If-Match") != d.ETag {
		a.nativeSourceFailure(w, r, streaming.ErrReleaseChanged)
		return
	}
	w.Header().Set("ETag", d.ETag)
	a.nativeMedia(w, r, g)
}
