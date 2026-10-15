package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/watchstate"
)

func TestPublicRedirectDropsProviderHeadersAndRejectsDowngrade(t *testing.T) {
	client := PublicClient()
	before, _ := http.NewRequest("GET", "https://provider.example/file", nil)
	for _, target := range []struct {
		url          string
		keep, reject bool
	}{{"https://provider.example/other", true, false}, {"https://cdn.example/other", false, false}, {"http://cdn.example/other", false, true}, {"https://user:secret@cdn.example/other", false, true}} {
		u, _ := url.Parse(target.url)
		next := &http.Request{URL: u, Header: http.Header{"Authorization": []string{"provider-only"}, "Cookie": []string{"private-cookie"}, "X-Provider-Key": []string{"private-key"}, "Range": []string{"bytes=10-19"}, "Accept-Encoding": []string{"identity"}}}
		err := client.CheckRedirect(next, []*http.Request{before})
		if target.reject {
			if !errors.Is(err, ErrUnsupported) {
				t.Fatal("unsafe redirect accepted", err)
			}
			continue
		}
		if err != nil || next.Header.Get("Range") != "bytes=10-19" || (next.Header.Get("Authorization") != "") != target.keep || !target.keep && (next.Header.Get("Cookie") != "" || next.Header.Get("X-Provider-Key") != "") {
			t.Fatal("redirect range/header policy failed")
		}
	}
}

func TestSelectedFileRejectsIgnoredRangesDisguisedPagesAndChangingTail(t *testing.T) {
	for _, mode := range []string{"valid", "force-download", "force-archive", "7z-as-video", "gzip-as-video", "tar-as-video", "no-range", "html", "compressed-tail", "changed-tail", "wrong-size"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data := bytes.Repeat([]byte("direct-file-----"), 1000)
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("ETag", `"file"`)
				switch mode {
				case "7z-as-video":
					copy(data, []byte("7z\xbc\xaf\x27\x1c"))
				case "gzip-as-video":
					copy(data, []byte{0x1f, 0x8b})
				case "tar-as-video":
					copy(data[257:], []byte("ustar"))
				}
				if mode == "force-download" || mode == "force-archive" {
					w.Header().Set("Content-Type", "application/force-download")
					if mode == "force-download" {
						copy(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
						w.Header().Set("Content-Encoding", "identity")
					} else {
						copy(data, []byte("PK\x03\x04"))
					}
				}
				if mode == "no-range" {
					w.Write(data)
					return
				}
				if mode == "html" {
					copy(data, []byte("<!doctype html><html>Login required</html>"))
				}
				if r.Header.Get("Range") != "bytes=0-4095" {
					if mode == "compressed-tail" {
						w.Header().Set("Content-Encoding", "gzip")
					}
					if mode == "changed-tail" {
						w.Header().Set("ETag", `"another-cut"`)
					}
				}
				http.ServeContent(w, r, "movie.mp4", time.Time{}, bytes.NewReader(data))
			}))
			defer server.Close()
			s := &Service{http: server.Client()}
			source := Source{URL: server.URL}
			if mode == "wrong-size" {
				source.VideoSize = 20000
			}
			prepared, err := s.prepare(context.Background(), source)
			if mode == "valid" || mode == "force-download" {
				if err != nil || prepared.Size != 16000 {
					t.Fatal("valid range file failed", err)
				}
			} else if err == nil {
				t.Fatal("unsupported file advertised as playable")
			}
		})
	}
}

func TestRenewalRefusesChangedBytesAndSelectionExpiry(t *testing.T) {
	s, auth, g, _, provider := fixture(t)
	ctx := context.Background()
	id := choose(t, s, auth, g, fixtureMedia)
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, id)
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.expired = true
	provider.changed = true
	provider.mu.Unlock()
	req := httptest.NewRequest("GET", session.MediaURL, nil)
	req.Header.Set("Range", "bytes=10-19")
	if err = s.Serve(httptest.NewRecorder(), req, g, session.ID); !errors.Is(err, ErrSelection) {
		t.Fatal("changed bytes accepted as old release", err)
	}
	var source Source
	var raw []byte
	if err = s.pool.QueryRow(ctx, `SELECT source FROM native_addon_playback WHERE session_id=$1`, session.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &source) != nil || !strings.HasSuffix(source.URL, "/old") {
		t.Fatal("failed renewal replaced private URL")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE native_addon_sources SET expires_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(ctx, auth, g, fixtureMedia, id); !errors.Is(err, ErrSelection) {
		t.Fatal("expired picker selection accepted", err)
	}
}

func TestNextEpisodeGroupIsUniqueAndDoesNotCrossShowOrDevice(t *testing.T) {
	s, auth, g, _, provider := fixture(t)
	ctx := context.Background()
	previous := mediaIdentity("episode", "tt1234567:1:1")
	id := choose(t, s, auth, g, previous)
	session, err := s.CreateSession(ctx, auth, g, previous, id)
	if err != nil {
		t.Fatal(err)
	}
	next := mediaIdentity("episode", "tt1234567:1:2")
	page, err := s.Sources(ctx, auth, g, next, session.ID)
	if err != nil || len(page.Sources) != 1 || page.AutoSelect != page.Sources[0].ID {
		t.Fatal("unique same-add-on group not reused", err)
	}
	provider.mu.Lock()
	provider.ambiguous = true
	provider.mu.Unlock()
	page, err = s.Sources(ctx, auth, g, next, session.ID)
	// Duplicate fresh streams render once, but raw ambiguity still prevents
	// an automatic continuation choice.
	if err != nil || len(page.Sources) != 1 || page.AutoSelect != "" {
		t.Fatal("ambiguous group auto-selected", err)
	}
	provider.mu.Lock()
	provider.ambiguous = false
	provider.mu.Unlock()
	tokens, err := auth.Login(ctx, "admin", "", "127.0.0.1", "other-device", "Other fixture")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		mediaKind, mediaID string
		otherDevice        bool
	}{{"episode", "tt9999999:1:2", false}, {"episode", previous.ID, false}, {"episode", next.ID, true}} {
		grant := g
		if v.otherDevice {
			grant = other
		}
		page, err = s.Sources(ctx, auth, grant, mediaIdentity(v.mediaKind, v.mediaID), session.ID)
		if err != nil || page.AutoSelect != "" {
			t.Fatal("group crossed episode/show/device", err)
		}
	}
}

func TestSubtitleOriginHeadersAndUnsupportedContent(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, choose(t, s, auth, g, fixtureMedia))
	if err != nil {
		t.Fatal(err)
	}
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("provider header crossed subtitle origin")
		}
		switch r.URL.Path {
		case "/valid":
			w.Write([]byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nText\n"))
		case "/html":
			w.Write([]byte("<!doctype html><html>Not subtitles</html>"))
		default:
			w.Write([]byte{0x89, 'P', 'N', 'G', 0, 0, 0})
		}
	}))
	defer sidecar.Close()
	for _, path := range []string{"/valid", "/html", "/binary"} {
		if _, err = s.pool.Exec(ctx, `UPDATE native_addon_playback SET source=jsonb_set(source,'{subtitles,0,url}',to_jsonb($2::text)) WHERE session_id=$1`, session.ID, sidecar.URL+path); err != nil {
			t.Fatal(err)
		}
		data, _, err := s.Subtitle(ctx, g, session.ID, 0)
		if path == "/valid" {
			if err != nil || !bytes.HasPrefix(data, []byte("WEBVTT")) {
				t.Fatal("valid subtitle failed", err)
			}
		} else if !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsupported sidecar accepted", err)
		}
	}
}

func TestUnknownDurationDoesNotCompleteBeforeNaturalEOF(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, choose(t, s, auth, g, fixtureMedia))
	if err != nil {
		t.Fatal(err)
	}
	store := watchstate.New(s.pool)
	now := time.Now().UTC()
	events := []watchstate.NativeEvent{
		{ID: "aaaaaaaa-1111-4111-8111-111111111111", SessionID: session.ID, Sequence: 1, Media: fixtureMedia, Kind: "start", PositionMS: 1000, ObservedAt: now, Origin: "live"},
		{ID: "aaaaaaaa-2222-4222-8222-222222222222", SessionID: session.ID, Sequence: 2, Media: fixtureMedia, Kind: "progress", PositionMS: 5000, ObservedAt: now.Add(time.Second), Origin: "live"},
	}
	if _, err = store.IngestNative(ctx, auth, g, events); err != nil {
		t.Fatal(err)
	}
	state, err := store.NativeState(ctx, g.User.ID, fixtureMedia)
	if err != nil || state.Played || state.PositionMS != 0 || state.DurationMS != nil || state.Revision != 0 {
		t.Fatal("unknown duration published viewing before its threshold could be established", state, err)
	}
	eof := watchstate.NativeEvent{ID: "aaaaaaaa-3333-4333-8333-333333333333", SessionID: session.ID, Sequence: 3, Media: fixtureMedia, Kind: "stop", PositionMS: 8000, NaturalEOF: true, ObservedAt: now.Add(2 * time.Second), Origin: "live"}
	if _, err = store.IngestNative(ctx, auth, g, []watchstate.NativeEvent{eof, eof}); err != nil {
		t.Fatal(err)
	}
	state, err = store.NativeState(ctx, g.User.ID, fixtureMedia)
	if err != nil || !state.Played || state.PlayCount != 1 || state.DurationMS != nil {
		t.Fatal("natural completion or replay failed", state, err)
	}
}
