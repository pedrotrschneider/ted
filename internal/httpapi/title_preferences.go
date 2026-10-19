package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/titleprefs"
	"github.com/jackc/pgx/v5"
)

func (a *API) titlePreferenceRoutes(mux *http.ServeMux, native bool) {
	prefix := "/api"
	if native {
		prefix += "/client/v1"
	}
	for _, route := range []string{"GET /collections/watchlist", "GET /collections/ratings/{bucket}", "GET /titles/{kind}/{id}/preferences", "PUT /titles/{kind}/{id}/preferences/{field}"} {
		if native {
			mux.HandleFunc(route[:4]+prefix+route[4:], a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
				a.titlePreferences(w, r, g.User, &g)
			}))
		} else {
			mux.HandleFunc(route[:4]+prefix+route[4:], a.protected(false, func(w http.ResponseWriter, r *http.Request, u accounts.User) { a.titlePreferences(w, r, u, nil) }))
		}
	}
}

func (a *API) titlePreferences(w http.ResponseWriter, r *http.Request, u accounts.User, grant *accounts.NativeGrant) {
	w.Header().Set("Cache-Control", "no-store")
	store := titleprefs.New(a.pool)
	if r.PathValue("id") == "" {
		list := r.PathValue("bucket")
		if list == "" {
			list = "watchlist"
		}
		random := r.URL.Query().Get("random") == "true"
		page, err := store.List(r.Context(), u.ID, list, r.URL.Query().Get("kind"), r.URL.Query().Get("after"), random)
		if err != nil {
			a.preferenceFailure(w, r, err)
			return
		}
		if grant != nil {
			result, err := a.preferenceNativePage(r.Context(), u.ID, page)
			if err != nil {
				a.nativeFail(w, r, err)
				return
			}
			a.respondNativeCatalog(w, r, result)
		} else if a.recheck(w, r, u, false) {
			if err = a.webArtwork(r.Context(), page.Titles); err != nil {
				a.fail(w, r, err)
				return
			}
			respond(w, 200, map[string]any{"titles": page.Titles, "next_after": page.Next})
		}
		return
	}
	media := catalog.MediaIdentity{Kind: r.PathValue("kind"), ID: r.PathValue("id")}
	if !titleprefs.Valid(media) {
		a.preferenceFailure(w, r, titleprefs.ErrInvalid)
		return
	}
	if r.Method == "GET" {
		values, err := store.States(r.Context(), u.ID, []catalog.MediaIdentity{media})
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if a.recheck(w, r, u, false) {
			respond(w, 200, values[media.Kind+"/"+media.ID])
		}
		return
	}
	field := r.PathValue("field")
	var body struct {
		Watchlist *bool           `json:"watchlist"`
		Rating    json.RawMessage `json:"user_rating"`
	}
	if !decode(w, r, &body) {
		return
	}
	var rating *int
	if field == "watchlist" && body.Watchlist == nil || field == "rating" && (len(body.Rating) == 0 || json.Unmarshal(body.Rating, &rating) != nil) || field != "watchlist" && field != "rating" || rating != nil && (*rating < 1 || *rating > 7) {
		a.preferenceFailure(w, r, titleprefs.ErrInvalid)
		return
	}
	title, err := a.preferenceTitle(r.Context(), u.ID, media)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if grant != nil {
		err = a.NativeAuth.AuthorizeMutation(r.Context(), tx, *grant)
	} else {
		err = accounts.AuthorizeMutation(r.Context(), tx, identity(u), false)
	}
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	saved := body.Watchlist != nil && *body.Watchlist
	state, err := titleprefs.Set(r.Context(), tx, u.ID, title, field, saved, rating)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		a.preferenceFailure(w, r, err)
		return
	}
	respond(w, 200, state)
}
func (a *API) preferenceFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, titleprefs.ErrInvalid) {
		respond(w, 400, map[string]string{"code": "invalid_title_preference"})
		return
	}
	a.fail(w, r, err)
}
func (a *API) preferenceTitle(ctx context.Context, user int64, m catalog.MediaIdentity) (cinemeta.Movie, error) {
	title, err := a.catalog.NativeTitle(ctx, m.Kind, m.ID)
	if err == nil {
		return title, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return title, err
	}
	err = a.pool.QueryRow(ctx, `SELECT metadata FROM account_title_preferences WHERE user_id=$3 AND kind=$1 AND title_id=$2
 UNION ALL SELECT metadata FROM catalog_media WHERE $1='movie' AND kind='movie' AND cinemeta_id=$2
 UNION ALL SELECT metadata FROM catalog_series WHERE $1='series' AND cinemeta_id=$2 LIMIT 1`, m.Kind, m.ID, user).Scan(&title)
	if err == nil && title.Name != "" {
		return persistedCatalogTitle(m.ID, m.Kind, title), nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return title, err
	}
	if m.Kind == "movie" {
		title, err = a.movies.Movie(ctx, m.ID)
	} else {
		var series cinemeta.Series
		series, err = a.series.Series(ctx, m.ID)
		title = series.Movie
	}
	if err == nil {
		title.ID, title.Type = m.ID, m.Kind
		err = a.catalog.SaveNativeTitle(ctx, title, time.Now())
	}
	return title, err
}
func (a *API) preferenceNativePage(ctx context.Context, user int64, page titleprefs.Page) (nativePersonalPage, error) {
	out := nativePersonalPage{Titles: []nativeTitleDetail{}, Issues: []nativeCatalogIssue{}, NextAfter: page.Next}
	for _, title := range page.Titles {
		out.Titles = append(out.Titles, nativeDetail(title, "cached"))
	}
	return out, a.decorateNative(ctx, user, out.Titles)
}
