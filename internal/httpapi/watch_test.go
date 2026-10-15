package httpapi

import "testing"

func TestWatchRoutesRequireLocalSession(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	requireStatus(t, call(t, h, "GET", "/api/watch-history", nil, nil), 401)
	requireStatus(t, call(t, h, "POST", "/api/watch-history/33333333-3333-3333-3333-333333333333", map[string]any{"revision": 1, "action": "watched"}, nil), 401)
	requireStatus(t, call(t, h, "GET", "/api/watch-titles/tt1234567", nil, nil), 401)
	requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1234567", map[string]any{"version": "v", "played": true}, nil), 401)
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/watch-titles/tt1234567", nil, cookie), 200)
	requireStatus(t, call(t, h, "GET", "/api/watch-titles/invalid", nil, cookie), 400)
	requireStatus(t, call(t, h, "POST", "/api/watch-titles/tt1234567", map[string]any{"version": "v"}, cookie), 400)
	requireStatus(t, call(t, h, "POST", "/api/watch-history/33333333-3333-3333-3333-333333333333", map[string]any{"revision": 1, "action": "watched"}, cookie), 503)
	requireStatus(t, call(t, h, "POST", "/api/watch-history/33333333-3333-3333-3333-333333333333", map[string]any{"revision": 1, "action": "delete"}, cookie), 400)
	requireStatus(t, call(t, h, "GET", "/api/watch-history", nil, cookie), 200)
	requireStatus(t, call(t, h, "GET", "/api/watch-history?before=-1", nil, cookie), 400)
	requireStatus(t, call(t, h, "GET", "/api/admin/watch-links", nil, cookie), 404)
}
