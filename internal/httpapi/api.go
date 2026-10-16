package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/stremio"
	"entertainmentdistrict/internal/updates"
	"entertainmentdistrict/internal/watchstate"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type API struct {
	updateChecker    *updates.Checker
	CalendarLocation *time.Location
	Streaming        *streaming.Service
	NativeAuth       *accounts.NativeAuth
	Metadata         *metadata.Service
	Providers        *settings.Store
	RDInventory      *realdebrid.Inventory
	pool             *pgxpool.Pool
	accounts         *accounts.Service
	sessions         *scs.SessionManager
	logger           *slog.Logger
	rd               RDReader
	series           SeriesReader
	movies           MovieReader
	catalog          *catalog.Store
	watch            *watchstate.Store
	stremio          *stremio.Store
	addons           *addons.Service
}

func New(pool *pgxpool.Pool, service *accounts.Service, secure bool, logger *slog.Logger, rd RDReader) *API {
	a := &API{updateChecker: updates.NewChecker(), CalendarLocation: time.UTC, pool: pool, accounts: service, logger: logger, rd: rd, sessions: scs.New()}
	a.NativeAuth, _ = accounts.NewNativeAuth(pool, service, accounts.DefaultNativePolicy())
	a.movies = cinemeta.New()
	a.series = cinemeta.New()
	a.catalog = catalog.New(pool)
	a.watch = watchstate.New(pool)
	a.stremio = stremio.NewStore(pool, stremio.New(), cinemeta.New())
	a.addons = addons.New(pool, stremio.New(), nil, logger)
	a.sessions.Store = pgxstore.NewWithCleanupInterval(pool, 0)
	a.sessions.HashTokenInStore = true
	a.sessions.Lifetime = 24 * time.Hour
	a.sessions.IdleTimeout = 30 * time.Minute
	a.sessions.Cookie.Name = "ted_session"
	a.sessions.Cookie.HttpOnly = true
	a.sessions.Cookie.Secure = secure
	a.sessions.Cookie.SameSite = http.SameSiteStrictMode
	a.sessions.Cookie.Persist = false
	a.sessions.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) { a.fail(w, r, err) }
	return a
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.pool.Ping(ctx); err != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	auth := http.NewServeMux()
	a.artworkRoutes(auth)
	a.stremioCopyRoutes(auth)
	auth.HandleFunc("GET /api/account/joint", a.protected(false, a.jointAccounts))
	auth.HandleFunc("POST /api/account/joint", a.protected(false, a.jointAccounts))
	auth.HandleFunc("GET /api/admin/addon-account", a.protected(true, a.addonAccount))
	auth.HandleFunc("POST /api/admin/addon-account", a.protected(true, a.addonAccount))
	auth.HandleFunc("DELETE /api/admin/addon-account", a.protected(true, a.addonAccount))
	auth.HandleFunc("GET /api/admin/addons", a.protected(true, a.addonPolicies))
	auth.HandleFunc("PUT /api/admin/addons", a.protected(true, a.addonPolicies))
	auth.HandleFunc("GET /api/admin/providers", a.protected(true, a.providerSettings))
	auth.HandleFunc("PUT /api/admin/providers", a.protected(true, a.providerSettings))
	auth.HandleFunc("GET /api/stremio", a.protected(false, a.stremioStatus))
	auth.HandleFunc("POST /api/stremio/link", a.protected(false, a.stremioConnect))
	auth.HandleFunc("DELETE /api/stremio/link", a.protected(false, a.stremioDisconnect))
	auth.HandleFunc("POST /api/watch-history/{id}", a.protected(false, a.watchEdit))
	auth.HandleFunc("GET /api/watch-titles/{id}", a.protected(false, a.watchTitle))
	auth.HandleFunc("POST /api/watch-titles/{id}", a.protected(false, a.watchTitle))
	auth.HandleFunc("GET /api/watch-history", a.protected(false, a.watchHistory))
	auth.HandleFunc("GET /api/admin/movies/{id}/mapping-operation", a.protected(true, a.mappingOperation))
	auth.HandleFunc("GET /api/admin/series/{id}/mapping-operation", a.protected(true, a.mappingOperation))
	auth.HandleFunc("POST /api/admin/series/{id}/mapping-operation/retry", a.protected(true, a.mappingRetry))
	auth.HandleFunc("POST /api/admin/movies/{id}/mapping-operation/retry", a.protected(true, a.mappingRetry))
	auth.HandleFunc("GET /api/series", a.protected(false, a.seriesBrowse))
	auth.HandleFunc("GET /api/series/saved", a.protected(false, a.savedSeries))
	auth.HandleFunc("GET /api/series/{id}", a.protected(false, a.seriesDetail))
	auth.HandleFunc("GET /api/admin/series/{id}/mappings", a.protected(true, a.episodeMappings))
	auth.HandleFunc("PUT /api/admin/series/{id}/mappings", a.protected(true, a.episodeMappingSave))

	for _, kind := range []string{"movies", "series"} {
		auth.HandleFunc("DELETE /api/admin/"+kind+"/{id}/library", a.protected(true, a.libraryRemoval))
		auth.HandleFunc("GET /api/admin/"+kind+"/{id}/library-removal", a.protected(true, a.libraryRemoval))
	}
	auth.HandleFunc("GET /api/movies/{id}/local", a.protected(false, a.localStatus))
	auth.HandleFunc("GET /api/episodes/{id}/local", a.protected(false, a.localStatus))
	auth.HandleFunc("POST /api/series/{id}/local", a.protected(false, a.localSeriesStart))
	auth.HandleFunc("POST /api/movies/{id}/local", a.protected(false, a.localStart))
	auth.HandleFunc("POST /api/episodes/{id}/local", a.protected(false, a.localStart))
	auth.HandleFunc("POST /api/admin/movies/{id}/local/cancel", a.protected(true, a.localCancel))
	auth.HandleFunc("POST /api/admin/episodes/{id}/local/cancel", a.protected(true, a.localCancel))
	auth.HandleFunc("DELETE /api/admin/movies/{id}/local", a.protected(true, a.localRemove))
	auth.HandleFunc("DELETE /api/admin/episodes/{id}/local", a.protected(true, a.localRemove))
	auth.HandleFunc("GET /api/admin/movies/{id}/streaming", a.protected(true, a.streamStatus))
	auth.HandleFunc("GET /api/admin/episodes/{id}/streaming", a.protected(true, a.streamStatus))
	auth.HandleFunc("POST /api/admin/movies/{id}/streaming", a.protected(true, a.streamStart))
	auth.HandleFunc("POST /api/admin/episodes/{id}/streaming", a.protected(true, a.streamStart))
	auth.HandleFunc("POST /api/admin/series/{id}/streaming", a.protected(true, a.streamSeriesStart))
	auth.HandleFunc("DELETE /api/admin/movies/{id}/streaming", a.protected(true, a.streamCancel))
	auth.HandleFunc("DELETE /api/admin/episodes/{id}/streaming", a.protected(true, a.streamCancel))
	auth.HandleFunc("GET /api/library-titles", a.protected(false, a.libraryTitles))
	auth.HandleFunc("GET /api/catalog-ratings", a.protected(false, a.catalogRatings))
	auth.HandleFunc("GET /api/title-statuses", a.protected(false, a.titleStatuses))
	auth.HandleFunc("GET /api/movies/saved", a.protected(false, a.savedMovies))
	auth.HandleFunc("GET /api/movies", a.protected(false, a.movieBrowse))
	auth.HandleFunc("GET /api/movies/{id}", a.protected(false, a.movieDetail))
	auth.HandleFunc("GET /api/admin/mappings", a.protected(true, a.mappingList))
	auth.HandleFunc("GET /api/admin/movies/{id}/mapping", a.protected(true, a.mappingGet))
	auth.HandleFunc("PUT /api/admin/movies/{id}/mapping", a.protected(true, a.mappingSave))
	auth.HandleFunc("GET /api/admin/rd/status", a.protected(true, a.rdStatus))
	auth.HandleFunc("GET /api/admin/rd/torrents", a.protected(true, a.rdList))
	auth.HandleFunc("GET /api/admin/rd/torrents/{id}", a.protected(true, a.rdTorrent))
	auth.HandleFunc("GET /api/admin/rd/cache", a.protected(true, a.rdCache))
	auth.HandleFunc("POST /api/admin/rd/cache/refresh", a.protected(true, a.rdCache))
	auth.HandleFunc("GET /api/admin/updates", a.protected(true, a.updateStatus))
	auth.HandleFunc("POST /api/admin/updates/dismiss", a.protected(true, a.dismissUpdate))
	auth.HandleFunc("POST /api/auth/login", a.login)
	auth.HandleFunc("GET /api/auth/setup", a.setupStatus)
	auth.HandleFunc("POST /api/auth/setup", a.setupAdmin)
	auth.HandleFunc("GET /api/auth/policy", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]int{"min_password_length": a.accounts.MinPasswordLength()})
	})
	auth.HandleFunc("PUT /api/auth/profile", a.protected(false, a.profile))
	auth.HandleFunc("POST /api/auth/logout-browser", a.protected(false, a.logoutBrowser))
	auth.HandleFunc("POST /api/auth/logout", a.protected(false, a.logout))
	auth.HandleFunc("GET /api/auth/me", a.protected(false, func(w http.ResponseWriter, r *http.Request, u accounts.User) { respond(w, 200, u) }))
	auth.HandleFunc("POST /api/auth/password", a.protected(false, a.password))
	auth.HandleFunc("POST /api/admin/users", a.protected(true, a.createUser))
	auth.HandleFunc("GET /api/admin/users", a.protected(true, a.listUsers))
	auth.HandleFunc("PUT /api/admin/users/{id}/disabled", a.protected(true, a.disableUser))
	origins := http.NewCrossOriginProtection()
	origins.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 403, map[string]string{"error": "cross-origin request rejected"})
	}))
	native := origins.Handler(compatibilityHandler(a.nativeHandler()))
	mux.Handle("/api/client/v1/", native)
	mux.Handle("/api/", origins.Handler(a.sessionHandler(auth)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "operation", "http_request", "error_type", fmt.Sprintf("%T", recovered))
				respond(w, 500, map[string]string{"error": "internal server error"})
			}
		}()
		if strings.HasPrefix(r.URL.Path, "/api/client/v1/") {
			native.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool { return decodeLimit(w, r, v, 4096) }
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		respond(w, 400, map[string]string{"error": "invalid JSON request"})
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		respond(w, 400, map[string]string{"error": "expected one JSON object"})
		return false
	}
	return true
}
func (a *API) identity(ctx context.Context) accounts.Identity {
	return accounts.Identity{ID: a.sessions.GetInt64(ctx, "user_id"), Version: a.sessions.GetInt64(ctx, "version")}
}
func identity(u accounts.User) accounts.Identity {
	return accounts.Identity{ID: u.ID, Version: u.Version}
}
func (a *API) protected(admin bool, next func(http.ResponseWriter, *http.Request, accounts.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who := a.identity(r.Context())
		if who.ID == 0 {
			a.fail(w, r, accounts.ErrUnauthorized)
			return
		}
		u, err := a.accounts.Current(r.Context(), who)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if admin && u.Role != "admin" {
			a.fail(w, r, accounts.ErrForbidden)
			return
		}
		next(w, r, u)
	}
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ Username, Password string }
	if !decode(w, r, &input) {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		a.fail(w, r, accounts.ErrInvalid)
		return
	}
	u, err := a.accounts.Login(r.Context(), input.Username, input.Password, ip)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Clear any old session contents and issue a fresh token on authentication.
	if err = a.sessions.Destroy(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	if err = a.sessions.RenewToken(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	a.sessions.Put(r.Context(), "user_id", u.ID)
	a.sessions.Put(r.Context(), "version", u.Version)
	respond(w, 200, u)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if err := a.accounts.Revoke(r.Context(), identity(u)); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.sessions.Destroy(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "all sessions revoked"})
}
func (a *API) password(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := a.accounts.ChangePassword(r.Context(), identity(u), input.CurrentPassword, input.NewPassword); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.sessions.Destroy(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "password changed; all sessions revoked"})
}
func (a *API) profile(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var input struct {
		DisplayName string `json:"display_name"`
	}
	if !decode(w, r, &input) {
		return
	}
	updated, err := a.accounts.SetDisplayName(r.Context(), identity(u), input.DisplayName)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, updated)
}
func (a *API) createUser(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var input struct {
		Username, Password, Role string
		DisplayName              string `json:"display_name"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Role == "" {
		input.Role = "user"
	}
	created, err := a.accounts.Create(r.Context(), identity(u), input.Username, input.DisplayName, input.Password, input.Role)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 201, created)
}
func (a *API) listUsers(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			a.fail(w, r, accounts.ErrInvalid)
			return
		}
	}
	page, err := a.accounts.List(r.Context(), identity(u), after)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, page)
}
func (a *API) disableUser(w http.ResponseWriter, r *http.Request, u accounts.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		a.fail(w, r, accounts.ErrInvalid)
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Disabled == nil {
		a.fail(w, r, accounts.ErrInvalid)
		return
	}
	if err = a.accounts.SetDisabled(r.Context(), identity(u), id, *input.Disabled); err != nil {
		a.fail(w, r, err)
		return
	}
	respond(w, 200, map[string]string{"status": "account updated"})
}
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := 503
	message := "operation unavailable; check server diagnostics"
	switch {
	case errors.Is(err, streaming.ErrLifecycle):
		respond(w, 409, map[string]string{"code": "lifecycle_changed", "error": err.Error()})
		return
	case errors.Is(err, streaming.ErrLocalBusy), errors.Is(err, streaming.ErrNotReady):
		status = 409
		message = err.Error()
	case errors.Is(err, streaming.ErrSpace):
		status = 507
		message = err.Error()
	case errors.Is(err, streaming.ErrStorage):
		message = err.Error()
	case errors.Is(err, accounts.ErrUnauthorized):
		status = 401
		message = "authentication required or credentials invalid"
	case errors.Is(err, accounts.ErrForbidden):
		status = 403
		message = err.Error()
	case errors.Is(err, accounts.ErrInvalid):
		status = 400
		message = "invalid input; check the username, display name, and configured password requirements"
	case errors.Is(err, accounts.ErrConflict):
		status = 409
		message = "username already exists, or operation would remove the last active administrator"
	case errors.Is(err, accounts.ErrNotFound):
		status = 404
		message = "account not found"
	case errors.Is(err, accounts.ErrLimited):
		status = 429
		message = err.Error()
		w.Header().Set("Retry-After", "600")
	case errors.Is(err, accounts.ErrBusy):
		status = 503
		message = err.Error()
		w.Header().Set("Retry-After", "1")
	default:
		code := ""
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			code = pe.Code
		}
		a.logger.Error("request failed", "operation", r.Pattern, "error_type", fmt.Sprintf("%T", err), "sqlstate", code, "invalid_password_record", errors.Is(err, accounts.ErrHash))
	}
	respond(w, status, map[string]string{"error": message})
}
