package streaming

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Generate and verify offset-dependent bytes without buffering a large video.
type streamPatternBody struct {
	ctx              context.Context
	position, remain int64
	maxRead          int
	closed           bool
}

func (b *streamPatternBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.remain == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), b.remain)
	if b.maxRead > 0 {
		n = min(n, int64(b.maxRead))
	}
	for i := range n {
		p[i] = byte((b.position + i) % 251)
	}
	b.position += n
	b.remain -= n
	return int(n), nil
}

func (b *streamPatternBody) Close() error { b.closed = true; return nil }

type streamPatternWriter struct {
	header  http.Header
	status  int
	start   int64
	bytes   int64
	invalid bool
	onWrite func()
}

func (w *streamPatternWriter) Header() http.Header    { return w.header }
func (w *streamPatternWriter) WriteHeader(status int) { w.status = status }
func (w *streamPatternWriter) Write(p []byte) (int, error) {
	for i, b := range p {
		if b != byte((w.start+w.bytes+int64(i))%251) {
			w.invalid = true
		}
	}
	w.bytes += int64(len(p))
	if w.onWrite != nil {
		w.onWrite()
	}
	return len(p), nil
}

func streamTestResponse(t *testing.T, r *http.Request, size int64) (*http.Response, *streamPatternBody) {
	t.Helper()
	span, err := parseRange(r.Header.Get("Range"), size)
	if err != nil || !span.partial {
		t.Fatalf("invalid upstream range: %v", err)
	}
	length := span.end - span.start + 1
	body := &streamPatternBody{ctx: r.Context(), position: span.start, remain: length}
	return &http.Response{StatusCode: 206, ContentLength: length, Header: http.Header{
		"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", span.start, span.end, size)},
	}, Body: body}, body
}

func TestRemoteStreamLargeRangeAvoidsSmallRequests(t *testing.T) {
	s, m, _ := fixture(t, &resolver{url: "https://test.download.real-debrid.com/media"})
	m.Bytes = 40 << 20
	const start, length int64 = 123, 32 << 20
	span := fmt.Sprintf("bytes=%d-%d", start, start+length-1)
	calls := 0
	var body *streamPatternBody
	s.mediaHTTP = &http.Client{Transport: transferTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != span || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("large requested range was split or encoded")
		}
		response, b := streamTestResponse(t, r, m.Bytes)
		body = b
		return response, nil
	})}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Range", span)
	w := &streamPatternWriter{header: make(http.Header), start: start}
	s.sendMedia(w, req, m)
	if calls != 1 || w.status != 206 || w.bytes != length || w.invalid || !body.closed {
		t.Fatalf("large transfer: requests=%d status=%d bytes=%d invalid=%v", calls, w.status, w.bytes, w.invalid)
	}
	if w.Header().Get("Content-Length") != fmt.Sprint(length) || w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, m.Bytes) {
		t.Fatal("downstream range contract changed")
	}
}

func TestRemoteStreamRenewsAndValidatesNextLargeRange(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid_second_range_%v", corrupt), func(t *testing.T) {
			const oldURL = "https://test.download.real-debrid.com/old"
			rd := &resolver{url: "https://test.download.real-debrid.com/renewed"}
			s, m, _ := fixture(t, rd)
			m.Bytes = remoteStreamChunkSize + 4096
			const start, length int64 = 17, remoteStreamChunkSize + 31
			if _, err := s.pool.Exec(context.Background(), `INSERT INTO streaming_links VALUES($1,$2,$3,now()+interval '1 hour')`, m.MappingID, rd.CredentialScope(), oldURL); err != nil {
				t.Fatal(err)
			}
			var requests []string
			var bodies []*streamPatternBody
			s.mediaHTTP = &http.Client{Transport: transferTransport(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Header.Get("Range"))
				if len(requests) > 1 && r.URL.String() == oldURL {
					return &http.Response{StatusCode: 403, Header: make(http.Header), Body: http.NoBody}, nil
				}
				response, body := streamTestResponse(t, r, m.Bytes)
				bodies = append(bodies, body)
				if corrupt && len(requests) > 1 {
					response.Header.Set("Content-Range", fmt.Sprintf("bytes 0-30/%d", m.Bytes))
				}
				return response, nil
			})}
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))
			w := &streamPatternWriter{header: make(http.Header), start: start}
			s.sendMedia(w, req, m)
			first := fmt.Sprintf("bytes=%d-%d", start, start+remoteStreamChunkSize-1)
			second := fmt.Sprintf("bytes=%d-%d", start+remoteStreamChunkSize, start+length-1)
			if len(requests) != 3 || requests[0] != first || requests[1] != second || requests[2] != second || rd.calls.Load() != 1 {
				t.Fatalf("renewal changed transfer boundaries: %v, renewals=%d", requests, rd.calls.Load())
			}
			wanted := length
			if corrupt {
				wanted = remoteStreamChunkSize
			}
			if w.status != 206 || w.bytes != wanted || w.invalid {
				t.Fatalf("wrong transfer after renewal: status=%d bytes=%d invalid=%v", w.status, w.bytes, w.invalid)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Fatal("upstream response left open")
				}
			}
		})
	}
}

func TestRemoteStreamCancellationAndOffsetReconnect(t *testing.T) {
	rd := &resolver{url: "https://test.download.real-debrid.com/media"}
	s, m, _ := fixture(t, rd)
	m.Bytes = 2 * remoteStreamChunkSize
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var body *streamPatternBody
	s.mediaHTTP = &http.Client{Transport: transferTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		response, b := streamTestResponse(t, r, m.Bytes)
		b.maxRead = 1024
		body = b
		return response, nil
	})}
	w := &streamPatternWriter{header: make(http.Header), onWrite: cancel}
	s.sendMedia(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx), m)
	if calls != 1 || w.bytes != 1024 || w.invalid || !body.closed {
		t.Fatalf("cancel did not stop and close the range: requests=%d bytes=%d", calls, w.bytes)
	}
	// A new client request resumes at its confirmed offset using the cached link.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Range", "bytes=1024-65535")
	resumed := &streamPatternWriter{header: make(http.Header), start: 1024}
	s.sendMedia(resumed, req, m)
	if calls != 2 || rd.calls.Load() != 1 || resumed.status != 206 || resumed.bytes != 65536-1024 || resumed.invalid || !body.closed {
		t.Fatal("range reconnect lost bytes or renewed an already valid link")
	}
}
