package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/streaming"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeDownloadRangesPinsAndOwnership(t *testing.T) {
	f, h, tokens := nativePlaybackFixture(t)
	session := createNativeHTTPPlayback(t, h, tokens.AccessToken)
	r := nativeCall(t, h, "POST", "/api/client/v1/downloads/resolve", tokens.AccessToken, map[string]any{"session_id": session.ID}, nil)
	requireStatus(t, r, 200)
	var d streaming.NativeDownload
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Bytes != 1000 || d.MediaURL != "/api/client/v1/downloads/"+session.ID+"/media" || d.ETag == "" {
		t.Fatal(d)
	}
	get := func(token, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "https://ted.example"+d.MediaURL, nil)
		req.Header.Set("X-Teddy-Compatibility-Version", "0.2")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Range", "bytes=100-199")
		req.Header.Set("If-Match", etag)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	r = get(tokens.AccessToken, d.ETag)
	requireStatus(t, r, http.StatusPartialContent)
	if r.Body.Len() != 100 || r.Header().Get("Content-Range") != "bytes 100-199/1000" || r.Header().Get("ETag") != d.ETag {
		t.Fatal("incorrect pinned range", r.Header(), r.Body.Len())
	}
	requireStatus(t, get(tokens.AccessToken, `"other"`), 409)
	requireStatus(t, get("", d.ETag), 401)
	// Same account's different device cannot adopt a saved transfer session.
	other := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]any{"username": "admin", "password": testPassword, "device_id": "another-device", "device_name": "Other"}, nil)
	requireStatus(t, other, 200)
	var pair accounts.NativeTokens
	if err := json.Unmarshal(other.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, get(pair.AccessToken, d.ETag), 404)
	if _, err := f.pool.Exec(context.Background(), `UPDATE streaming_items SET published=false WHERE movie_id=$1`, nativeTestMedia); err != nil {
		t.Fatal(err)
	}
	if r = get(tokens.AccessToken, d.ETag); r.Code == 206 || r.Code == 200 {
		t.Fatal("unpublished source resumed")
	}
}
