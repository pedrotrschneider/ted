package addons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/realdebrid"
)

func TestStartupPreparationParallelAndCanonicalIdentity(t *testing.T) {
	for _, size := range []int{3000, 6000, startupHead + 1000, 4 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := bytes.Repeat([]byte{7}, size)
			copy(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				a, b, e := parseRange(r.Header.Get("Range"), int64(size))
				if e != nil {
					t.Error(e)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, size))
				w.Header().Set("Content-Type", "video/x-matroska")
				w.Header().Set("ETag", `"stable"`)
				w.WriteHeader(206)
				w.Write(data[a : b+1])
			}))
			defer server.Close()
			svc := New(nil, nil, server.Client(), nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan prepared, 1)
			failure := make(chan error, 1)
			go func() {
				p, e := svc.preparePlayback(ctx, Source{URL: server.URL, VideoSize: int64(size), RDFile: &realdebrid.FileRequest{}})
				done <- p
				failure <- e
			}()
			requests := 1
			if size > startupHead {
				requests = 2
			}
			for range requests {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("head/tail requests did not overlap")
				}
			}
			close(release)
			p := <-done
			if e := <-failure; e != nil {
				t.Fatal(e)
			}
			canonical := append([]byte(nil), data[:min(4096, size)]...)
			if size > 4096 {
				canonical = append(canonical, data[max(4096, size-4096):]...)
			}
			sum := sha256.Sum256(canonical)
			if p.Fingerprint != hex.EncodeToString(sum[:]) {
				t.Fatal("changed durable fingerprint")
			}
			defer p.startup.close()
			var loaded []byte
			for len(loaded) < min(size, startupHead) {
				part := p.startup.read(ctx, int64(len(loaded)), int64(min(size, startupHead)-1))
				if len(part) == 0 {
					break
				}
				loaded = append(loaded, part...)
			}
			if !bytes.Equal(loaded, data[:min(size, startupHead)]) {
				t.Fatal("incorrect header bytes")
			}
		})
	}
}

func TestStartupRejectsIncompleteAndChangedFile(t *testing.T) {
	for _, fault := range []string{"short", "etag", "range", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			size := int64(4 << 20)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				a, b, _ := parseRange(r.Header.Get("Range"), size)
				if fault == "cancel" {
					<-r.Context().Done()
					return
				}
				n := size
				if fault == "range" {
					n++
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, n))
				w.Header().Set("Content-Type", "video/x-matroska")
				w.Header().Set("ETag", `"a"`)
				if fault == "etag" && a > 0 {
					w.Header().Set("ETag", `"b"`)
				}
				w.WriteHeader(206)
				data := make([]byte, b-a+1)
				copy(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
				if fault == "short" {
					data = data[:len(data)-1]
				}
				w.Write(data)
			}))
			defer server.Close()
			svc := New(nil, nil, server.Client(), nil)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			p, e := svc.preparePlayback(ctx, Source{URL: server.URL, VideoSize: size, RDFile: &realdebrid.FileRequest{}})
			if e == nil || p.startup != nil {
				t.Fatal("invalid preparation published bytes")
			}
		})
	}
}

func TestStartupRangesFlushAndScope(t *testing.T) {
	body := []byte("0123456789abcdefghijklmnopqrstuv")
	c := &startupBytes{head: body[:10], tail: body[25:], tailStart: 25, contentType: "video/mp4"}
	p := prepared{Source: Source{URL: "https://example.com/file", RDScope: "scope1"}, Size: int64(len(body)), Fingerprint: "pin", ETag: "etag", startup: c}
	svc := New(nil, nil, nil, nil)
	svc.rememberStartup("session", p)
	changed := p
	changed.Source.RDScope = "scope2"
	if svc.cachedStartup("session", changed) != nil || svc.cachedStartup("other", p) != nil {
		t.Fatal("cache escaped source/session scope")
	}
	for _, span := range [][2]int64{{0, 31}, {3, 8}, {25, 31}, {7, 28}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		value := fmt.Sprintf("bytes=%d-%d", span[0], span[1])
		r.Header.Set("Range", value)
		calls := 0
		handled := svc.serveStartup(w, r, p, c, span[0], span[1], value, func(out http.ResponseWriter, next *http.Request) error {
			calls++
			if !w.Flushed {
				t.Error("upstream opened before cached bytes flushed")
			}
			a, b, _ := parseRange(next.Header.Get("Range"), p.Size)
			out.WriteHeader(206)
			_, e := out.Write(body[a : b+1])
			return e
		})
		if !handled || !bytes.Equal(w.Body.Bytes(), body[span[0]:span[1]+1]) || w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", span[0], span[1], p.Size) {
			t.Fatal("range bytes/headers changed")
		}
		if (span[1] < 10 || span[0] >= 25) && calls != 0 {
			t.Fatal("fully cached range contacted upstream")
		}
	}
	// Concurrent readers and bounded eviction never mutate published byte slices.
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() { defer wg.Done(); svc.rememberStartup(fmt.Sprint(i), p); _ = svc.cachedStartup("session", p) }()
	}
	wg.Wait()
	if len(svc.startup.sessions) > startupSessions {
		t.Fatal("unbounded cache")
	}
	svc.startup.sessions["expired"] = startupEntry{startupKey(p), c, time.Now().Add(-time.Second)}
	if svc.cachedStartup("expired", p) != nil {
		t.Fatal("expired cache returned")
	}
	// A rejected continuation must not append an API error into a media response.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	svc.serveStartup(w, r, p, c, 0, 31, "bytes=0-", func(out http.ResponseWriter, _ *http.Request) error {
		out.WriteHeader(403)
		_, e := io.WriteString(out, "forbidden")
		return e
	})
	if !bytes.Equal(w.Body.Bytes(), body[:10]) {
		t.Fatal("error mixed into cached media")
	}
}

func TestStartupPlaybackKeepsAuthorizationAndRenewal(t *testing.T) {
	s, auth, g, _, provider := fixture(t)
	ctx := context.Background()
	choice := choose(t, s, auth, g, fixtureMedia)
	session, e := s.CreateSession(ctx, auth, g, fixtureMedia, choice)
	if e != nil {
		t.Fatal(e)
	}
	_, p, e := s.playback(ctx, g, session.ID)
	if e != nil {
		t.Fatal(e)
	}
	data := bytes.Repeat([]byte("fixture-file----"), 1000)
	p.startup = &startupBytes{head: data[:4096], tail: data[len(data)-4096:], tailStart: int64(len(data) - 4096), contentType: "video/mp4"}
	s.rememberStartup(session.ID, p)
	request := func(good bool, span string) (*httptest.ResponseRecorder, error) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Range", span)
		w := httptest.NewRecorder()
		grant := g
		if !good {
			grant.User.ID++
		}
		return w, s.Serve(w, r, grant, session.ID)
	}
	provider.mu.Lock()
	before := provider.requests
	provider.expired = true
	provider.mu.Unlock()
	w, e := request(false, "bytes=0-10")
	if e == nil || w.Body.Len() != 0 {
		t.Fatal("cache bypassed user ownership")
	}
	w, e = request(true, "bytes=0-10")
	if e != nil || !bytes.Equal(w.Body.Bytes(), data[:11]) {
		t.Fatal("cached prefix failed", e)
	}
	provider.mu.Lock()
	after := provider.requests
	provider.mu.Unlock()
	if after != before {
		t.Fatal("cached range contacted provider")
	}
	// Prefix is already written when the continuation discovers an expired URL.
	// Renewal must still pin the same file, and not replace the outer range.
	w, e = request(true, "bytes=0-15999")
	if e != nil || !bytes.Equal(w.Body.Bytes(), data) || w.Header().Get("Content-Range") != "bytes 0-15999/16000" {
		t.Fatal("cached continuation/renewal failed", e, w.Body.Len())
	}
	if _, e = s.pool.Exec(ctx, `UPDATE server_addon_accounts SET active=false,auth_key='' WHERE active`); e != nil {
		t.Fatal(e)
	}
	w, e = request(true, "bytes=0-10")
	if e == nil || w.Body.Len() != 0 {
		t.Fatal("cache bypassed disconnected account")
	}
}

func TestProgressiveStartupReturnsBeforeHeadAndSurvivesRequestClose(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 4<<20)
	copy(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
	release := make(chan struct{})
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, b, _ := parseRange(r.Header.Get("Range"), int64(len(data)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, len(data)))
		w.Header().Set("Content-Type", "video/x-matroska")
		w.Header().Set("ETag", `"stable"`)
		w.WriteHeader(206)
		if a == 0 {
			w.Write(data[:4096])
			w.(http.Flusher).Flush()
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			a = 4096
		}
		w.Write(data[a : b+1])
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := New(nil, nil, server.Client(), nil)
	done := make(chan prepared, 1)
	fail := make(chan error, 1)
	go func() {
		p, e := svc.preparePlayback(ctx, Source{URL: server.URL, VideoSize: int64(len(data)), RDFile: &realdebrid.FileRequest{}})
		done <- p
		fail <- e
	}()
	<-entered
	var p prepared
	select {
	case p = <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("preparation waited for 2 MiB remainder")
	}
	if e := <-fail; e != nil {
		t.Fatal(e)
	}
	defer p.startup.close()
	cancel() // Returning the JSON request must not kill its handed-off body.
	got := p.startup.read(context.Background(), 0, 4095)
	if !bytes.Equal(got, data[:4096]) {
		t.Fatal("prefix changed")
	}
	close(release)
	readCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	for len(got) < startupHead {
		b := p.startup.read(readCtx, int64(len(got)), startupHead-1)
		if len(b) == 0 {
			t.Fatal("request cancellation killed continuation")
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, data[:startupHead]) {
		t.Fatal("continuation corrupted")
	}
}

func TestProgressiveStartupDiscardCancelsBlockedBody(t *testing.T) {
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, b, _ := parseRange(r.Header.Get("Range"), 4<<20)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, 4<<20))
		w.Header().Set("Content-Type", "video/x-matroska")
		w.WriteHeader(206)
		if a == 0 {
			d := make([]byte, 4096)
			copy(d, []byte{0x1a, 0x45, 0xdf, 0xa3})
			w.Write(d)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(stopped)
			return
		}
		w.Write(make([]byte, b-a+1))
	}))
	defer server.Close()
	svc := New(nil, nil, server.Client(), nil)
	p, e := svc.preparePlayback(context.Background(), Source{URL: server.URL, VideoSize: 4 << 20, RDFile: &realdebrid.FileRequest{}})
	if e != nil {
		t.Fatal(e)
	}
	p.startup.close()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("discard kept connection alive")
	}
}
