package addons

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type expiredResponseConn struct {
	net.Conn
	initial   bool
	mu        sync.Mutex
	deadlines []time.Time
}

func (c *expiredResponseConn) SetWriteDeadline(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadlines = append(c.deadlines, at)
	if !c.initial {
		c.initial = true
		// Deterministically expire the server's total-response deadline. The
		// media handler must replace it on the real socket before writing bytes.
		return c.Conn.SetWriteDeadline(time.Now().Add(-time.Second))
	}
	return c.Conn.SetWriteDeadline(at)
}

type deadlineListener struct {
	net.Listener
	accepted chan *expiredResponseConn
}

func (l deadlineListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &expiredResponseConn{Conn: conn}
	l.accepted <- wrapped
	return wrapped, nil
}

func TestAddonMediaReplacesTotalResponseDeadlineOnRealConnection(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, choose(t, s, auth, g, fixtureMedia))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Serve(w, r, g, session.ID); err != nil {
			t.Error(err)
		}
	}))
	accepted := make(chan *expiredResponseConn, 1)
	server.Listener = deadlineListener{server.Listener, accepted}
	server.Config.WriteTimeout = 20 * time.Second
	server.Start()
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL, nil)
	request.Header.Set("Range", "bytes=0-15999")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal("media did not replace expired server deadline", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 206 || len(body) != 16000 {
		t.Fatal("deadline truncated ranged media", response.StatusCode, len(body), err)
	}
	conn := <-accepted
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.deadlines) < 3 || !conn.deadlines[1].IsZero() || conn.deadlines[2].IsZero() {
		t.Fatal("missing clear/rolling media deadline", conn.deadlines)
	}
}

type shortMediaTransport struct{ base http.RoundTripper }

func (t shortMediaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(r)
	if err == nil && res.StatusCode == 206 {
		res.Body = &limitedMediaBody{ReadCloser: res.Body, left: 128}
	}
	return res, err
}

type limitedMediaBody struct {
	io.ReadCloser
	left int
}

func (b *limitedMediaBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		return 0, io.EOF
	}
	n, err := b.ReadCloser.Read(p[:min(len(p), b.left)])
	b.left -= n
	return n, err
}

func TestAddonShortMediaRetainsAdvertisedLengthWithoutAppendingJSON(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	session, err := s.CreateSession(ctx, auth, g, fixtureMedia, choose(t, s, auth, g, fixtureMedia))
	if err != nil {
		t.Fatal(err)
	}
	s.media = &http.Client{Transport: shortMediaTransport{s.mediaHTTP().Transport}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Serve(w, r, g, session.ID); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL, nil)
	request.Header.Set("Range", "bytes=0-15999")
	res, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != io.ErrUnexpectedEOF || len(body) != 128 || res.ContentLength != 16000 {
		t.Fatal("short media became a successful or JSON response", len(body), res.ContentLength, err)
	}
}
