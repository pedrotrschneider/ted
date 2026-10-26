package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/watchstate"
)

func (a *API) nativeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/client/v1/accounts/{id}/avatar", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) { a.visibleAvatar(w, r, g.User) }))
	a.titlePreferenceRoutes(mux, true)
	a.nativePlaybackRoutes(mux)
	a.nativeMetadataRoutes(mux)
	mux.HandleFunc("POST /api/client/v1/auth/login", a.nativeLogin)
	mux.HandleFunc("POST /api/client/v1/auth/refresh", a.nativeRefresh)
	mux.HandleFunc("POST /api/client/v1/auth/logout", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) { a.nativeLogout(w, r, g, false) }))
	mux.HandleFunc("POST /api/client/v1/auth/logout-all", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) { a.nativeLogout(w, r, g, true) }))
	mux.HandleFunc("GET /api/client/v1/account/avatar", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
		a.avatar(w, r, g.User)
	}))
	mux.HandleFunc("GET /api/client/v1/auth/me", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
		respond(w, 200, map[string]any{"user": g.User, "family_id": g.FamilyID, "device_id": g.DeviceID})
	}))
	mux.HandleFunc("GET /api/client/v1/capabilities", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
		respond(w, 200, map[string]any{"api_version": 1, "authentication": "opaque_rotating", "direct_play_only": true, "mapped_playback": a.Streaming != nil, "completion_fraction": float64(watchstate.CompletionPercent) / 100})
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Authorization")
		// No SCS loading/saving on native routes, even if a browser has a cookie.
		// Reject competing mechanisms instead of silently choosing an identity.
		if _, err := r.Cookie(a.sessions.Cookie.Name); err == nil {
			a.nativeFail(w, r, accounts.ErrUnauthorized)
			return
		}
		_, pattern := mux.Handler(r)
		if pattern != "GET /api/client/v1/playback/sessions/{id}/media" && pattern != "GET /api/client/v1/downloads/{id}/media" {
			budget := 10 * time.Second
			if pattern == "POST /api/client/v1/playback/preparations" || pattern == "POST /api/client/v1/playback/sessions" || pattern == "POST /api/client/v1/downloads/resolve" {
				budget = addons.SessionPreparationTimeout + 5*time.Second
			}
			if pattern == "POST /api/client/v1/playback/sources" {
				budget = addons.SourceLookupTimeout + 3*time.Second
			}
			if budget > 10*time.Second {
				// The server's ordinary JSON write deadline must not expire while
				// source lookup or selected CDN validation is still in progress.
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget + time.Second))
			}
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
}

func accessHeader(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", accounts.ErrUnauthorized
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", accounts.ErrUnauthorized
	}
	return parts[1], nil
}

func (a *API) nativeProtected(next func(http.ResponseWriter, *http.Request, accounts.NativeGrant)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := accessHeader(r)
		if err != nil {
			a.nativeFail(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		g, err := a.NativeAuth.Authenticate(ctx, raw)
		cancel()
		if err != nil {
			a.nativeFail(w, r, err)
			return
		}
		next(w, r, g)
	}
}

func (a *API) nativeLogin(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) != 0 {
		a.nativeFail(w, r, accounts.ErrUnauthorized)
		return
	}
	var input struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if !decode(w, r, &input) {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	pair, err := a.NativeAuth.Login(r.Context(), input.Username, input.Password, ip, input.DeviceID, input.DeviceName)
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	respond(w, 200, pair)
}

func (a *API) nativeRefresh(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) != 0 {
		a.nativeFail(w, r, accounts.ErrUnauthorized)
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &input) {
		return
	}
	pair, err := a.NativeAuth.Refresh(r.Context(), input.RefreshToken)
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	respond(w, 200, pair)
}

func (a *API) nativeLogout(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant, all bool) {
	if err := a.NativeAuth.Logout(r.Context(), g, all); err != nil {
		a.nativeFail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "signed out"})
}

func (a *API) nativeFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, accounts.ErrUnauthorized) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="TED"`)
	}
	a.fail(w, r, err)
}
