package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/profileimage"
)

func (a *API) avatarRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/account/avatar", a.protected(false, a.uploadAvatar))
	mux.HandleFunc("GET /api/account/avatar", a.protected(false, a.avatar))
}

func (a *API) uploadAvatar(w http.ResponseWriter, r *http.Request, u accounts.User) {
	r.Body = http.MaxBytesReader(w, r.Body, profileimage.MaxUpload)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			respond(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "avatar_too_large"})
		} else {
			respond(w, http.StatusBadRequest, map[string]string{"code": "avatar_invalid"})
		}
		return
	}
	updated, err := a.accounts.SetAvatar(r.Context(), identity(u), data)
	if errors.Is(err, profileimage.ErrInvalid) {
		respond(w, http.StatusBadRequest, map[string]string{"code": "avatar_invalid"})
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, http.StatusOK, updated)
}

func (a *API) avatar(w http.ResponseWriter, r *http.Request, u accounts.User) {
	image, err := a.accounts.Avatar(r.Context(), identity(u))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(image) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(image)))
	w.Write(image)
}
