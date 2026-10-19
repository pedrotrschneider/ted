package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/titleprefs"
	"testing"
)

func TestTitlePreferencesNativeAndBrowserAreOneAccountLocalContract(t *testing.T) {
	f := setup(t)
	f.api.movies = &movieStub{}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	path := "/api/client/v1/titles/movie/tt1345836/preferences"
	saved := nativeCall(t, h, "PUT", path+"/watchlist", pair.AccessToken, map[string]any{"watchlist": true}, nil)
	requireStatus(t, saved, 200)
	rated := nativeCall(t, h, "PUT", path+"/rating", pair.AccessToken, map[string]any{"user_rating": 7}, nil)
	requireStatus(t, rated, 200)
	var state titleprefs.State
	if err := json.Unmarshal(rated.Body.Bytes(), &state); err != nil || !state.Watchlist || state.Rating == nil || *state.Rating != 7 {
		t.Fatal(state, err)
	}
	detail := nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt1345836", pair.AccessToken, nil, nil)
	requireStatus(t, detail, 200)
	var title nativeTitleDetail
	if err := json.Unmarshal(detail.Body.Bytes(), &title); err != nil || !title.Watchlist || title.UserRating == nil || *title.UserRating != 7 {
		t.Fatal(title, err)
	}
	for _, route := range []string{"/collections/watchlist?kind=movie", "/collections/ratings/7"} {
		r := nativeCall(t, h, "GET", "/api/client/v1"+route, pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		var page nativePersonalPage
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Titles) != 1 || !page.Titles[0].Watchlist {
			t.Fatal(page, err)
		}
	}
	cookie := login(t, h, "admin")
	browser := call(t, h, "GET", "/api/title-statuses?kind=movie&ids=tt1345836", nil, cookie)
	requireStatus(t, browser, 200)
	var statuses struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(browser.Body.Bytes(), &statuses); err != nil || len(statuses.Statuses) != 1 || !statuses.Statuses[0].Watchlist || statuses.Statuses[0].UserRating == nil {
		t.Fatal(statuses, err)
	}
	// A rating alone never creates viewing or an Unrated candidate.
	r := nativeCall(t, h, "GET", "/api/client/v1/collections/ratings/0", pair.AccessToken, nil, nil)
	requireStatus(t, r, 200)
	var page nativePersonalPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Titles) != 0 {
		t.Fatal(page, err)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM watch_current)+(SELECT count(*) FROM watch_history)+(SELECT count(*) FROM media_mappings)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("personal action created viewing/mapping", count, err)
	}
	// Saved snapshots allow explicit removals/clears even after disposable
	// catalog caches are cleared and Cinemeta is unavailable.
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM native_catalog_titles`); err != nil {
		t.Fatal(err)
	}
	f.api.movies = &movieStub{err: cinemeta.ErrUnavailable}
	requireStatus(t, nativeCall(t, h, "PUT", path+"/rating", pair.AccessToken, map[string]any{"user_rating": nil}, nil), 200)
	requireStatus(t, nativeCall(t, h, "PUT", path+"/watchlist", pair.AccessToken, map[string]any{"watchlist": false}, nil), 200)
}
func TestTitlePreferencesValidateScopeAndFenceRevokedFamilies(t *testing.T) {
	f := setup(t)
	f.api.movies = &movieStub{}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	for _, test := range []struct {
		path string
		body any
	}{
		{"episode/tt1345836:1:1/preferences/rating", map[string]any{"user_rating": 5}},
		{"season/tt1345836/preferences/watchlist", map[string]any{"watchlist": true}},
		{"movie/tt1345836/preferences/rating", map[string]any{}},
		{"movie/tt1345836/preferences/rating", map[string]any{"user_rating": 8}},
		{"movie/tt1345836/preferences/rating", map[string]any{"user_rating": 2.5}},
		{"movie/tt1345836/preferences/watchlist", map[string]any{"watchlist": nil}},
	} {
		requireStatus(t, nativeCall(t, h, "PUT", "/api/client/v1/titles/"+test.path, pair.AccessToken, test.body, nil), 400)
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/auth/logout", pair.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "PUT", "/api/client/v1/titles/movie/tt1345836/preferences/rating", pair.AccessToken, map[string]any{"user_rating": 6}, nil), 401)
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_title_preferences`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid/revoked mutation persisted", count, err)
	}
}
