package addons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"entertainmentdistrict/internal/streaming"
)

// These bounds cover the observed MKV header/track data and trailing cues.
// This is a disposable session optimization, never authoritative media storage.
const startupHead = 2 << 20
const startupTail = 128 << 10
const startupSessions = 16
const startupLifetime = 5 * time.Minute

type startupBytes struct {
	head, tail  []byte
	tailStart   int64
	contentType string
	stream      *startupStream
}
type startupEntry struct {
	key     [32]byte
	bytes   *startupBytes
	expires time.Time
}
type startupCache struct {
	mu       sync.Mutex
	sessions map[string]startupEntry
}

func startupKey(p prepared) [32]byte {
	// Includes private URL/headers and RD credential scope, as well as the pinned
	// file identity. Only its digest is retained; never log these inputs.
	raw, _ := json.Marshal(struct {
		Source            Source
		Size              int64
		Fingerprint, ETag string
	}{p.Source, p.Size, p.Fingerprint, p.ETag})
	return sha256.Sum256(raw)
}
func (s *Service) rememberStartup(session string, p prepared) {
	if p.startup == nil {
		return
	}
	s.startup.mu.Lock()
	defer s.startup.mu.Unlock()
	if s.startup.sessions == nil {
		s.startup.sessions = make(map[string]startupEntry)
	}
	now := time.Now()
	for id, e := range s.startup.sessions {
		if !now.Before(e.expires) {
			e.bytes.close()
			delete(s.startup.sessions, id)
		}
	}
	if len(s.startup.sessions) >= startupSessions {
		var oldest string
		var expiry time.Time
		for id, e := range s.startup.sessions {
			if oldest == "" || e.expires.Before(expiry) {
				oldest, expiry = id, e.expires
			}
		}
		s.startup.sessions[oldest].bytes.close()
		delete(s.startup.sessions, oldest)
	}
	if old, ok := s.startup.sessions[session]; ok && old.bytes != p.startup {
		old.bytes.close()
	}
	s.startup.sessions[session] = startupEntry{startupKey(p), p.startup, now.Add(startupLifetime)}
}
func (s *Service) cachedStartup(session string, p prepared) *startupBytes {
	s.startup.mu.Lock()
	defer s.startup.mu.Unlock()
	e, ok := s.startup.sessions[session]
	if !ok {
		return nil
	}
	if !time.Now().Before(e.expires) {
		e.bytes.close()
		delete(s.startup.sessions, session)
		return nil
	}
	if e.key != startupKey(p) {
		return nil
	}
	return e.bytes
}

type startupPart struct {
	data              []byte
	etag, contentType string
}

func (s *Service) startupPart(ctx context.Context, source Source, start, end int64) (startupPart, error) {
	req, err := sourceRequest(ctx, source, fmt.Sprintf("bytes=%d-%d", start, end))
	if err != nil {
		return startupPart{}, err
	}
	res, err := s.mediaHTTP().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return startupPart{}, ctx.Err()
		}
		return startupPart{}, ErrUnavailable
	}
	defer res.Body.Close()
	a, b, n, e := contentRange(res.Header.Get("Content-Range"))
	if res.StatusCode != 206 || e != nil || a != start || b != end || n != source.VideoSize || !fileContentType(res.Header.Get("Content-Type")) || !identityEncoding(res.Header.Get("Content-Encoding")) {
		return startupPart{}, ErrUnsupported
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, end-start+2))
	if e != nil || int64(len(data)) != end-start+1 {
		if ctx.Err() != nil {
			return startupPart{}, ctx.Err()
		}
		return startupPart{}, ErrUnsupported
	}
	return startupPart{data, res.Header.Get("ETag"), res.Header.Get("Content-Type")}, nil
}
func (s *Service) preparePlayback(ctx context.Context, source Source) (prepared, error) {
	if source.RDFile != nil && source.VideoSize > 0 {
		return s.prepareStartup(ctx, source)
	}
	return s.prepare(ctx, source)
}

func (s *Service) prepareStartup(ctx context.Context, source Source) (prepared, error) {
	if source.VideoSize > 32<<40 {
		return prepared{}, ErrUnsupported
	}
	parent := ctx
	background, backgroundCancel, stopParent := detachedStartup(parent)
	handedOff := false
	defer func() {
		stopParent()
		if !handedOff {
			backgroundCancel()
		}
	}()
	ctx, cancel := context.WithCancelCause(background)
	defer func() {
		if !handedOff {
			cancel(nil)
		}
	}()
	size := source.VideoSize
	headEnd := min(int64(startupHead), size) - 1
	tailStart := max(headEnd+1, size-startupTail)
	var head, tail startupPart
	var headErr, tailErr error
	var wg sync.WaitGroup
	if tailStart < size {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tail, tailErr = s.startupPart(ctx, source, tailStart, size-1)
			if tailErr != nil {
				cancel(tailErr)
			}
		}()
	}
	var stream *startupStream
	if size > startupHead+4096 {
		head, stream, headErr = s.progressiveHead(ctx, source, headEnd)
	} else {
		head, headErr = s.startupPart(ctx, source, 0, headEnd)
	}
	if headErr != nil {
		cancel(headErr)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return prepared{}, context.Cause(ctx)
	}
	if tailStart < size && head.etag != tail.etag {
		return prepared{}, ErrUnsupported
	}
	// Keep the same media validation contract as ordinary preparation.
	if err := validateMediaPrefix(head.data, head.contentType); err != nil {
		return prepared{}, err
	}
	pin := append([]byte(nil), head.data[:min(4096, len(head.data))]...)
	if size > 4096 {
		from := max(int64(4096), size-4096)
		if from <= headEnd {
			pin = append(pin, head.data[from:]...)
		}
		if tailStart < size {
			pin = append(pin, tail.data[max(from-tailStart, 0):]...)
		}
	}
	sum := sha256.Sum256(pin)
	etag := head.etag
	if len(etag) > 1024 || strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	if parent.Err() != nil {
		return prepared{}, parent.Err()
	}
	stopParent()
	if parent.Err() != nil {
		return prepared{}, parent.Err()
	}
	if stream != nil {
		stream.cancel = backgroundCancel
		handedOff = true
	}
	return prepared{Source: source, Size: size, Fingerprint: hex.EncodeToString(sum[:]), ETag: etag, startup: &startupBytes{head: head.data, tail: tail.data, tailStart: tailStart, contentType: head.contentType, stream: stream}}, nil
}

func (c *startupBytes) read(ctx context.Context, start, end int64) []byte {
	if c.stream != nil && start < startupHead {
		return c.stream.read(ctx, start, min(end, startupHead-1))
	}
	return c.at(start, end)
}
func (c *startupBytes) close() {
	if c != nil && c.stream != nil {
		c.stream.cancel()
	}
}
func (c *startupBytes) at(start, end int64) []byte {
	if start < int64(len(c.head)) {
		return c.head[start:min(end+1, int64(len(c.head)))]
	}
	if start >= c.tailStart && start < c.tailStart+int64(len(c.tail)) {
		return c.tail[start-c.tailStart : min(end+1-c.tailStart, int64(len(c.tail)))]
	}
	return nil
}

// Header/index reads can be answered immediately. For an open-ended request,
// flush the cached prefix before opening the remainder: demuxers can parse it
// and cancel/seek without waiting for another CDN connection. The remainder
// still uses the ordinary authorized, identity-checked range handler.
func (s *Service) serveStartup(w http.ResponseWriter, r *http.Request, p prepared, c *startupBytes, start, end int64, rangeValue string, remote func(http.ResponseWriter, *http.Request) error) bool {
	data := c.read(r.Context(), start, end)
	if len(data) == 0 {
		return false
	}
	contentType := c.contentType
	if strings.HasPrefix(strings.ToLower(contentType), "application/force-download") {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", `"ted-`+p.Fingerprint+`"`)
	if r.Method == "HEAD" {
		h.Set("Content-Length", strconv.FormatInt(p.Size, 10))
		w.WriteHeader(200)
		return true
	}
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if rangeValue != "" {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, p.Size))
		w.WriteHeader(206)
	} else {
		w.WriteHeader(200)
	}
	for len(data) > 0 {
		n, e := streaming.CopyMedia(w, bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return true
		}
		start += n
		if start > end {
			return true
		}
		if http.NewResponseController(w).Flush() != nil {
			return true
		}
		data = c.read(r.Context(), start, end)
	}
	if http.NewResponseController(w).Flush() != nil || r.Context().Err() != nil {
		return true
	}
	next := r.Clone(r.Context())
	next.Header = r.Header.Clone()
	next.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	next.Header.Del("If-Range")
	continuation := &startupContinuation{ResponseWriter: w, headers: make(http.Header)}
	if err := remote(continuation, next); err != nil && r.Context().Err() == nil && s.logger != nil {
		s.logger.WarnContext(r.Context(), "startup media continuation failed", "operation", "addon_startup_continuation")
	}
	return true
}

// Do not replace the original response's range/length or append an error body
// after writing cached bytes. A failed continuation remains a short transfer.
type startupContinuation struct {
	http.ResponseWriter
	headers http.Header
	valid   bool
}

func (w *startupContinuation) Header() http.Header    { return w.headers }
func (w *startupContinuation) WriteHeader(status int) { w.valid = status == 206 }
func (w *startupContinuation) Write(b []byte) (int, error) {
	if !w.valid {
		return 0, ErrSelection
	}
	return w.ResponseWriter.Write(b)
}
func (w *startupContinuation) Unwrap() http.ResponseWriter { return w.ResponseWriter }
