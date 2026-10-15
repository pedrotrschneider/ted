package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/metadata"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func warmArtwork(t *testing.T, f *fixture, cookie *http.Cookie) metadata.EditableArtwork {
	t.Helper()
	ctx := context.Background()
	h := f.api.Handler()
	cold := call(t, h, "GET", "/api/admin/titles/movie/tt1345836/artwork", nil, cookie)
	requireStatus(t, cold, 200)
	var waiting struct {
		Cached  bool                       `json:"metadata_cached"`
		Pending bool                       `json:"metadata_pending"`
		Images  []metadata.EditableArtwork `json:"images"`
	}
	if err := json.Unmarshal(cold.Body.Bytes(), &waiting); err != nil || waiting.Cached || !waiting.Pending || len(waiting.Images) != 0 {
		t.Fatal("cold cache became editable", cold.Body.String(), err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE metadata_entities SET status='ready',provider_id='49026',fetched_at=now(),applied_generation=generation,item=item||'{"Name":"Movie"}'::jsonb,request=request||'{"original_language_checked":true}'::jsonb WHERE identity='movie:tt1345836'`); err != nil {
		t.Fatal(err)
	}
	ready := call(t, h, "GET", "/api/admin/titles/movie/tt1345836/artwork", nil, cookie)
	requireStatus(t, ready, 200)
	if err := json.Unmarshal(ready.Body.Bytes(), &waiting); err != nil || !waiting.Cached || waiting.Pending || len(waiting.Images) != 4 {
		t.Fatal(ready.Body.String(), err)
	}
	for _, a := range waiting.Images {
		if a.Type == "Primary" {
			return a
		}
	}
	t.Fatal("missing poster slot")
	return metadata.EditableArtwork{}
}
func uploadArtworkHTTP(t *testing.T, h http.Handler, a metadata.EditableArtwork, cookie *http.Cookie, origin string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "https://ted.example/api/admin/artwork/"+a.ID+"/upload?generation=1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	return out
}
func TestArtworkBrowserAuthorizationValidationAndSharedNativeProjection(t *testing.T) {
	f := setup(t)
	installMetadata(t, f)
	f.api.movies = &movieStub{}
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	a := warmArtwork(t, f, cookie)
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	member := login(t, h, "member")
	requireStatus(t, call(t, h, "GET", "/api/admin/titles/movie/tt1345836/artwork", nil, member), 403)
	requireStatus(t, uploadArtworkHTTP(t, h, a, member, "https://ted.example", []byte("ignored")), 403)
	requireStatus(t, uploadArtworkHTTP(t, h, a, cookie, "https://attacker.invalid", []byte("ignored")), 403)
	requireStatus(t, uploadArtworkHTTP(t, h, a, cookie, "https://ted.example", []byte("not an image")), 400)
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, uploadArtworkHTTP(t, h, a, cookie, "https://ted.example", raw.Bytes()), 200)
	requireStatus(t, uploadArtworkHTTP(t, h, a, cookie, "https://ted.example", raw.Bytes()), 409)
	requireStatus(t, call(t, h, "GET", "/api/metadata/images/"+a.ID, nil, nil), 401)
	art := call(t, h, "GET", "/api/metadata/images/"+a.ID, nil, member)
	requireStatus(t, art, 200)
	if !bytes.Equal(art.Body.Bytes(), raw.Bytes()) || art.Header().Get("Content-Type") != "image/png" {
		t.Fatal("shared cached image not served", art.Header())
	}
	movie := call(t, h, "GET", "/api/movies/tt1345836", nil, member)
	requireStatus(t, movie, 200)
	if !strings.Contains(movie.Body.String(), "/api/metadata/images/"+a.ID+"?v=") {
		t.Fatal("browser missed override", movie.Body.String())
	}
	pair := nativeHTTPLogin(t, h)
	detail := nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt1345836", pair.AccessToken, nil, nil)
	requireStatus(t, detail, 200)
	var title nativeTitleDetail
	if err := json.Unmarshal(detail.Body.Bytes(), &title); err != nil || title.Enrichment == nil {
		t.Fatal(detail.Body.String(), err)
	}
	found := false
	for _, img := range title.Enrichment.Images {
		if img.ID == a.ID {
			found = img.UserModified && len(img.Tag) == 64
		}
	}
	if !found {
		t.Fatal("native cache omitted override authority", detail.Body.String())
	}
	var persisted string
	if err := f.pool.QueryRow(context.Background(), `SELECT metadata->>'poster' FROM catalog_media WHERE cinemeta_id='tt1345836'`).Scan(&persisted); err != nil || strings.Contains(persisted, "/api/") {
		t.Fatal("projected session URL leaked into canonical catalog", persisted, err)
	}
}
func TestArtworkCacheFailureRemainsNonEditableAndIsRetryable(t *testing.T) {
	f := setup(t)
	installMetadata(t, f)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/titles/series/tt0386676/artwork", nil, cookie), 200)
	if _, err := f.pool.Exec(context.Background(), `UPDATE metadata_entities SET status='failed',error_code='provider_unavailable' WHERE identity='series:tt0386676'`); err != nil {
		t.Fatal(err)
	}
	failed := call(t, h, "GET", "/api/admin/titles/series/tt0386676/artwork", nil, cookie)
	requireStatus(t, failed, 200)
	if !strings.Contains(failed.Body.String(), `"metadata_cached":false`) || !strings.Contains(failed.Body.String(), `"images":[]`) || !strings.Contains(failed.Body.String(), `"metadata_error":"provider_unavailable"`) {
		t.Fatal(failed.Body.String())
	}
	if !strings.Contains(failed.Body.String(), `"metadata_pending":true`) {
		t.Fatal("active retry stopped preparation", failed.Body.String())
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE river_job SET state='discarded',finalized_at=now() WHERE kind='native_metadata_enrichment'`); err != nil {
		t.Fatal(err)
	}
	final := call(t, h, "GET", "/api/admin/titles/series/tt0386676/artwork", nil, cookie)
	requireStatus(t, final, 200)
	if !strings.Contains(final.Body.String(), `"metadata_pending":false`) {
		t.Fatal("exhausted retry stayed pending", final.Body.String())
	}
	retry := call(t, h, "GET", "/api/admin/titles/series/tt0386676/artwork?retry=1", nil, cookie)
	requireStatus(t, retry, 200)
	if !strings.Contains(retry.Body.String(), `"metadata_pending":true`) {
		t.Fatal(retry.Body.String())
	}
}
