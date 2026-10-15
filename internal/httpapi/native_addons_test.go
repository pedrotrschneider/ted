package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/stremio"
)

type addonAccountFixture struct{ wire []json.RawMessage }
type addonRejectedSession struct{}

func (addonRejectedSession) AddonCollection(context.Context, string) ([]json.RawMessage, error) {
	return nil, stremio.ErrCredentials
}

func (f addonAccountFixture) AddonCollection(context.Context, string) ([]json.RawMessage, error) {
	return f.wire, nil
}
func TestNativeSourcesPreferMappingsAndRequireServerProvider(t *testing.T) {
	f, h, tokens := nativePlaybackFixture(t)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/addons", "", nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/sources", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}}, nil), 409)
	r := nativeCall(t, h, "POST", "/api/client/v1/playback/sources", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}}, nil)
	requireStatus(t, r, 200)
	var page struct {
		Mode, Source string
		Ready        bool `json:"mapped_ready"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || page.Mode != "mapped" || page.Source != "local" || !page.Ready {
		t.Fatal(page, err)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE streaming_items SET phase='failed',published=false`); err != nil {
		t.Fatal(err)
	}
	r = nativeCall(t, h, "POST", "/api/client/v1/playback/sources", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}}, nil)
	requireStatus(t, r, 200)
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || page.Mode != "mapped" || page.Ready {
		t.Fatal("mapped failure fell through to add-ons", page, err)
	}
}
func TestNativeAddonSessionAndSidecarUseBearerOwnershipWithoutLeakingURLs(t *testing.T) {
	f, h, tokens := nativePlaybackFixture(t)
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/stream/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "http://" + r.Host + "/file.mp4", "name": "720p", "subtitles": []map[string]string{{"id": "en", "lang": "eng", "url": "http://" + r.Host + "/subtitle"}}}}})
		case r.URL.Path == "/subtitle":
			_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nSubtitle\n"))
		default:
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "file.mp4", time.Time{}, strings.NewReader(strings.Repeat("media", 1000)))
		}
	}))
	defer upstream.Close()
	descriptor, _ := json.Marshal(map[string]any{"transportUrl": upstream.URL + "/private-addon-key/manifest.json", "manifest": map[string]any{"id": "fixture", "name": "Fixture", "types": []string{"movie"}, "resources": []string{"stream"}}})
	f.api.addons = addons.New(f.pool, addonAccountFixture{[]json.RawMessage{descriptor}}, upstream.Client(), nil)
	if _, err := f.pool.Exec(ctx, `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES('addon','fixture@example.com','private-auth-key')`); err != nil {
		t.Fatal(err)
	}
	h = f.api.Handler()
	r := nativeCall(t, h, "POST", "/api/client/v1/playback/sources", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}}, nil)
	requireStatus(t, r, 200)
	var page struct{ Sources []addons.Choice }
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Sources) != 1 {
		t.Fatal(err, r.Body.String())
	}
	favoriteURL := "/api/client/v1/playback/sources/" + page.Sources[0].ID + "/favorite"
	requireStatus(t, nativeCall(t, h, "PUT", favoriteURL, "", map[string]any{"favorite": true}, nil), 401)
	requireStatus(t, nativeCall(t, h, "PUT", favoriteURL, tokens.AccessToken, map[string]any{}, nil), 422)
	requireStatus(t, nativeCall(t, h, "PUT", favoriteURL, tokens.AccessToken, map[string]any{"favorite": true}, nil), 200)
	r = nativeCall(t, h, "POST", "/api/client/v1/playback/sessions", tokens.AccessToken, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt8888888"}, "source_id": page.Sources[0].ID}, nil)
	requireStatus(t, r, 201)
	for _, secret := range []string{"private-auth-key", "private-addon-key", upstream.URL} {
		if strings.Contains(r.Body.String(), secret) {
			t.Fatal("private source leaked")
		}
	}
	var session streaming.NativeSession
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil || session.DurationMS != 0 || len(session.Subtitles) != 1 {
		t.Fatal(session, err)
	}
	requireStatus(t, nativeCall(t, h, "GET", session.Subtitles[0].URL, "", nil, nil), 401)
	r = nativeCall(t, h, "GET", session.Subtitles[0].URL, tokens.AccessToken, nil, nil)
	requireStatus(t, r, 200)
	if !strings.Contains(r.Body.String(), "Subtitle") {
		t.Fatal("sidecar body missing")
	}
	f.api.addons = addons.New(f.pool, addonRejectedSession{}, upstream.Client(), nil)
	r = nativeCall(t, h, "GET", "/api/client/v1/addons?refresh=1", tokens.AccessToken, nil, nil)
	requireStatus(t, r, 409)
	if !strings.Contains(r.Body.String(), "stremio_credentials_rejected") {
		t.Fatal("missing relink error", r.Body.String())
	}
	if _, err := f.pool.Exec(ctx, `UPDATE server_addon_accounts SET active=false,auth_key=''`); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "GET", session.Subtitles[0].URL, tokens.AccessToken, nil, nil), 409)
}
