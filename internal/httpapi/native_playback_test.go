package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/watchstate"
)

const nativeTestMedia = "11111111-1111-4111-8111-111111111111"
const nativeTestMapping = "22222222-2222-4222-8222-222222222222"
const nativeTestProbe = `{"format":{"format_name":"matroska","duration":"100","size":"1000"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}]}`

func nativePlaybackFixture(t *testing.T) (*fixture, http.Handler, accounts.NativeTokens) {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	s, err := streaming.New(ctx, f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = s
	root := t.TempDir()
	if err = s.ConfigureStorage(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseStorage)
	_, err = f.pool.Exec(ctx, `INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES('11111111-1111-4111-8111-111111111111','tt1234567','{"id":"tt1234567","name":"Native fixture","poster":"https://example.com/poster.jpg","year":"2026"}');
 INSERT INTO media_mappings(id,movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','fixture-private-account','PRIVATE_TORRENT',repeat('a',40),1,'/movie.mkv',1000,'{"secret":"private-provider-url"}',1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES($1,$2,'ready',true,$3)`, nativeTestMedia, nativeTestMapping, nativeTestProbe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, `INSERT INTO local_files(movie_id,generation,job_id,phase,total_bytes,completed_bytes,validated_bytes,available) VALUES($1,1,0,'ready',1000,1000,1000,true)`, nativeTestMedia)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, nativeTestMedia), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, nativeTestMedia, "1.media"), bytes.Repeat([]byte{'x'}, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	return f, h, nativeHTTPLogin(t, h)
}

func createNativeHTTPPlayback(t *testing.T, h http.Handler, token string) streaming.NativeSession {
	t.Helper()
	r := nativeCall(t, h, "POST", "/api/client/v1/playback/sessions", token, map[string]any{"media": catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}}, nil)
	requireStatus(t, r, 201)
	for _, secret := range []string{"fixture-private-account", "PRIVATE_TORRENT", "private-provider-url", "22222222-2222"} {
		if strings.Contains(r.Body.String(), secret) {
			t.Fatal("session leaked provider/mapping details")
		}
	}
	var session streaming.NativeSession
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.DurationMS != 100000 || session.Source != "local" || session.Media.Kind != "movie" || session.Media.ID != "tt1234567" {
		t.Fatal("incomplete session")
	}
	return session
}

func nativeMediaCall(h http.Handler, method, path, token, rangeHeader string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://ted.example"+path, nil)
	r.Header.Set("X-Teddy-Compatibility-Version", "0.3")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNativeMappedCatalogPagingIsolationAndMalformedMetadata(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `WITH titles AS (
 INSERT INTO catalog_media(id,cinemeta_id,metadata)
 SELECT ('77777777-7777-4777-8777-'||lpad(n::text,12,'0'))::uuid,'tt'||lpad(n::text,7,'0'),jsonb_build_object('id','tt'||lpad(n::text,7,'0'),'name','Mapped fixture '||n,'private','provider-secret')
 FROM generate_series(1,50) n RETURNING id
 ), mappings AS (
 INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 SELECT id,'private-account','PRIVATE_TORRENT',repeat('b',40),1,'/movie.mkv',1000,'{}',1 FROM titles RETURNING id,movie_id
 ) INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe)
 SELECT movie_id,id,'ready',true,$1 FROM mappings`, nativeTestProbe)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/mapped", "", nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/mapped?after=invalid", pair.AccessToken, nil, nil), 400)
	seen := map[catalog.MediaIdentity]bool{}
	after := ""
	for page := 0; page < 2; page++ {
		r := nativeCall(t, h, "GET", "/api/client/v1/titles/mapped"+after, pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		for _, secret := range []string{"provider-secret", "private-account", "PRIVATE_TORRENT", "source_snapshot"} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatal("mapped list leaked provider data")
			}
		}
		var data struct {
			Titles []nativeTitleDetail `json:"titles"`
			Next   *string             `json:"next_after"`
		}
		if err = json.Unmarshal(r.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if page == 0 && (len(data.Titles) != 50 || data.Next == nil) || page == 1 && (len(data.Titles) != 1 || data.Next != nil) {
			t.Fatal("incomplete mapped paging")
		}
		for _, title := range data.Titles {
			if seen[title.Media] || title.State.Played || title.State.PositionMS != 0 || !title.Ready {
				t.Fatal("duplicate identity or another user's state")
			}
			seen[title.Media] = true
		}
		if data.Next != nil {
			after = "?after=" + *data.Next
		}
	}
	if len(seen) != 51 {
		t.Fatal("lost mapped item")
	}
	_, err = f.pool.Exec(ctx, `UPDATE catalog_media SET metadata='{"name":{"invalid":"provider-secret"}}' WHERE id=$1`, nativeTestMedia)
	if err != nil {
		t.Fatal(err)
	}
	// A bad retained row must fail the page, not advertise a successful smaller
	// list. Force that row to be the first candidate without changing identity.
	r := nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt1234567", pair.AccessToken, nil, nil)
	requireStatus(t, r, 503)
	if strings.Contains(r.Body.String(), "provider-secret") {
		t.Fatal("metadata error leaked its value")
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/mapped", pair.AccessToken, nil, nil), 503)
}

type heldNativeBody struct {
	http.ResponseWriter
	once             sync.Once
	entered, release chan struct{}
}

func (w *heldNativeBody) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseWriter.Write(p)
}

func TestNativeMediaExpiryRefreshOpenBodyAndRangePinning(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	session := createNativeHTTPPlayback(t, h, pair.AccessToken)
	for _, token := range []string{"", pair.RefreshToken} {
		requireStatus(t, nativeMediaCall(h, "GET", session.MediaURL, token, "bytes=100-131"), 401)
	}
	for _, method := range []string{"HEAD", "GET"} {
		r := nativeMediaCall(h, method, session.MediaURL, pair.AccessToken, "bytes=100-131")
		requireStatus(t, r, 206)
		if r.Header().Get("Content-Range") != "bytes 100-131/1000" || r.Header().Get("Content-Length") != "32" {
			t.Fatal("range information changed")
		}
		if method == "HEAD" && r.Body.Len() != 0 || method == "GET" && r.Body.String() != strings.Repeat("x", 32) {
			t.Fatal("wrong original bytes")
		}
	}
	r := httptest.NewRequest("GET", "https://ted.example"+session.MediaURL, nil)
	r.Header.Set("X-Teddy-Compatibility-Version", "0.3")
	r.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	held := &heldNativeBody{ResponseWriter: w, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(held, r) }()
	select {
	case <-held.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("media body was buffered or did not start")
	}
	// Expire access deterministically while the authorized body is open.
	_, err := f.pool.Exec(context.Background(), `UPDATE native_access_tokens SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE family_id=$1`, pair.FamilyID)
	if err != nil {
		close(held.release)
		<-done
		t.Fatal(err)
	}
	requireStatus(t, nativeMediaCall(h, "GET", session.MediaURL, pair.AccessToken, "bytes=100-131"), 401)
	rotated := nativeCall(t, h, "POST", "/api/client/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken}, nil)
	requireStatus(t, rotated, 200)
	var next accounts.NativeTokens
	if err = json.Unmarshal(rotated.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.FamilyID != pair.FamilyID {
		t.Fatal("playback refresh changed family")
	}
	requireStatus(t, nativeMediaCall(h, "GET", session.MediaURL, next.AccessToken, "bytes=100-131"), 206)
	close(held.release)
	<-done
	requireStatus(t, w, 200)
	if w.Body.Len() != 1000 {
		t.Fatal("access expiry interrupted an authorized open body")
	}
	// A remap cannot silently serve a different release under this session URL.
	_, err = f.pool.Exec(context.Background(), `UPDATE mapping_versions SET active=false WHERE id=$1`, nativeTestMapping)
	if err != nil {
		t.Fatal(err)
	}
	var replacement string
	err = f.pool.QueryRow(context.Background(), `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 VALUES($1,'fixture','T2',repeat('b',40),2,'/new-cut.mkv',1000,'{}',1) RETURNING id::text`, nativeTestMedia).Scan(&replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(context.Background(), `UPDATE streaming_items SET mapping_id=$2 WHERE movie_id=$1`, nativeTestMedia, replacement); err != nil {
		t.Fatal(err)
	}
	changed := nativeMediaCall(h, "GET", session.MediaURL, next.AccessToken, "bytes=100-131")
	requireStatus(t, changed, 409)
	if !strings.Contains(changed.Body.String(), "release_changed") {
		t.Fatal("mapping replacement not actionable")
	}
}

func TestNativePlaybackDoesNotRequireOptionalDescriptiveMetadata(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	for _, metadata := range []string{`{"id":"tt1234567","name":"Native fixture"}`, `{}`} {
		if _, err := f.pool.Exec(context.Background(), `UPDATE catalog_media SET metadata=$2 WHERE id=$1`, nativeTestMedia, metadata); err != nil {
			t.Fatal(err)
		}
		session := createNativeHTTPPlayback(t, h, pair.AccessToken)
		r := nativeMediaCall(h, "GET", session.MediaURL, pair.AccessToken, "bytes=20-39")
		requireStatus(t, r, 206)
		if r.Body.String() != strings.Repeat("x", 20) {
			t.Fatal("optional metadata changed media bytes")
		}
	}
}

func TestNativeMappedRewatchResumesAndCompletionClearsPosition(t *testing.T) {
	_, h, pair := nativePlaybackFixture(t)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/watch-state", pair.AccessToken,
		watchstate.NativeEdit{Media: catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}, Action: "watched"}, nil), 200)
	session := createNativeHTTPPlayback(t, h, pair.AccessToken)
	if session.ResumeMS != 0 {
		t.Fatal("completed viewing retained a resume position")
	}
	at := time.Now().UTC().Add(time.Second)
	events := []watchstate.NativeEvent{
		{ID: "33333333-3333-4333-8333-333333333333", SessionID: session.ID, Sequence: 1, Media: session.Media, Kind: "start", ObservedAt: at, Origin: "live"},
		{ID: "44444444-4444-4444-8444-444444444444", SessionID: session.ID, Sequence: 2, Media: session.Media, Kind: "stop", PositionMS: 40000, ObservedAt: at, Origin: "live"},
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", pair.AccessToken, map[string]any{"events": events}, nil), 200)
	resume := createNativeHTTPPlayback(t, h, pair.AccessToken)
	if resume.ResumeMS != 40000 {
		t.Fatal("watched flag suppressed mapped rewatch resume", resume)
	}
	finish := []watchstate.NativeEvent{
		{ID: "55555555-5555-4555-8555-555555555555", SessionID: resume.ID, Sequence: 1, Media: resume.Media, Kind: "start", PositionMS: 40000, ObservedAt: at, Origin: "live"},
		{ID: "66666666-6666-4666-8666-666666666666", SessionID: resume.ID, Sequence: 2, Media: resume.Media, Kind: "stop", PositionMS: 95000, ObservedAt: at, Origin: "live"},
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", pair.AccessToken, map[string]any{"events": finish}, nil), 200)
	completed := createNativeHTTPPlayback(t, h, pair.AccessToken)
	if completed.ResumeMS != 0 {
		t.Fatal("completed rewatch retained its resume position", completed)
	}
}

func TestNativeReportsResumeAnotherDeviceAndCatalogPrivacy(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	session := createNativeHTTPPlayback(t, h, pair.AccessToken)
	at := time.Now().UTC().Truncate(time.Millisecond)
	events := []watchstate.NativeEvent{
		{ID: "33333333-3333-4333-8333-333333333333", SessionID: session.ID, Sequence: 1, Media: session.Media, Kind: "start", ObservedAt: at, Origin: "live"},
		{ID: "66666666-6666-4666-8666-666666666666", SessionID: session.ID, Sequence: 2, Media: session.Media, Kind: "progress", PositionMS: 15000, DurationMS: 100000, ObservedAt: at, Origin: "live"},
		{ID: "44444444-4444-4444-8444-444444444444", SessionID: session.ID, Sequence: 3, Media: session.Media, Kind: "stop", PositionMS: 40000, DurationMS: 100000, ObservedAt: at, Origin: "live"},
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", pair.AccessToken, map[string]any{"events": events}, nil), 200)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", pair.AccessToken, map[string]any{"events": events}, nil), 200)
	other, err := f.api.NativeAuth.Login(context.Background(), "admin", testPassword, "127.0.0.1", "other-device", "Other player")
	if err != nil {
		t.Fatal(err)
	}
	resume := createNativeHTTPPlayback(t, h, other.AccessToken)
	if resume.ResumeMS != 40000 || resume.WatchRevision != 2 {
		t.Fatal("another device lost resume position", resume.ResumeMS, resume.WatchRevision)
	}
	requireStatus(t, nativeMediaCall(h, "GET", session.MediaURL, other.AccessToken, "bytes=0-31"), 404)
	view := nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt1234567", pair.AccessToken, nil, nil)
	requireStatus(t, view, 200)
	if strings.Contains(view.Body.String(), "PRIVATE_TORRENT") || !strings.Contains(view.Body.String(), `"position_ms":40000`) {
		t.Fatal("detail leaked sources or omitted personal state")
	}
	history := nativeCall(t, h, "GET", "/api/client/v1/watch-history", pair.AccessToken, nil, nil)
	requireStatus(t, history, 200)
	var page watchstate.NativeHistory
	if err = json.Unmarshal(history.Body.Bytes(), &page); err != nil || len(page.Entries) != 2 {
		t.Fatal("replay duplicated history", len(page.Entries), err)
	}
	// All users can play shared media, while personal state remains isolated.
	_, err = f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := f.api.NativeAuth.Login(context.Background(), "viewer", testPassword, "127.0.0.1", "viewer-device", "Viewer player")
	if err != nil {
		t.Fatal(err)
	}
	viewerSession := createNativeHTTPPlayback(t, h, viewer.AccessToken)
	if viewerSession.ResumeMS != 0 {
		t.Fatal("another user's position leaked")
	}
	requireStatus(t, nativeMediaCall(h, "GET", viewerSession.MediaURL, viewer.AccessToken, "bytes=0-31"), 206)
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", viewer.AccessToken, map[string]any{"events": events}, nil), 404)
}

func TestNativePlaybackUsesCachedOriginalLanguageWithoutDelayingSource(t *testing.T) {
	f, h, tokens := nativePlaybackFixture(t)
	installMetadata(t, f)
	ctx := context.Background()
	v, err := f.api.Metadata.Ensure(ctx, "Tmdb", metadata.Request{Kind: "movie", TitleID: "tt1234567"}, metadata.Item{Type: "Movie", Name: "English title"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE metadata_entities SET request=request||'{"original_language":"ja","original_language_checked":true}'::jsonb,status='ready' WHERE id=$1`, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	if got := createNativeHTTPPlayback(t, h, tokens.AccessToken); got.OriginalLanguage != "ja" {
		t.Fatalf("cached session language: %+v", got)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE metadata_entities SET request=request-'original_language',status='failed' WHERE id=$1`, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	if got := createNativeHTTPPlayback(t, h, tokens.AccessToken); got.OriginalLanguage != "" {
		t.Fatal("unknown metadata should leave source usable")
	}
}
