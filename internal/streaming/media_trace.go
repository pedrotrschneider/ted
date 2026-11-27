package streaming

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

const MediaRequestIDHeader = "X-TED-Request-ID"
const MediaClientRequestIDHeader = "X-TED-Client-Request-ID"
const MediaDetailHeader = "X-TED-Detailed-Media"

type mediaTraceKey struct{}

// MediaTrace measures delivery at TED's HTTP boundary. Writes measure acceptance
// by the downstream connection (possibly a proxy), not receipt by the player.
// It accepts no provider URLs, headers, filenames or arbitrary error messages.
type MediaTrace struct {
	mu                      sync.Mutex
	logger                  *slog.Logger
	ctx                     context.Context
	id, client, session     string
	started, phaseStarted   time.Time
	phase, source, code     string
	failurePhase            string
	lastIOPhase             string
	user                    int64
	device                  string
	detailed, finished      bool
	done                    chan struct{}
	status, attempt         int
	start, end, size        int64
	readBytes, writtenBytes int64
	readUS, writeUS         int64
	maxReadUS, maxWriteUS   int64
	window                  time.Time
	events, suppressed      int
}

// BeginMediaTrace runs before native authentication so missing headers and
// authorization waits are measurable too. Only an authenticated client with a
// valid correlation ID may enable the additional phase/progress records.
func BeginMediaTrace(w http.ResponseWriter, r *http.Request, logger *slog.Logger, session string) (http.ResponseWriter, *http.Request, *MediaTrace) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	client := r.Header.Get(MediaClientRequestIDHeader)
	if len(r.Header.Values(MediaClientRequestIDHeader)) != 1 || !uuidPattern.MatchString(client) {
		client = ""
	}
	if !uuidPattern.MatchString(session) {
		session = ""
	}
	now := time.Now()
	t := &MediaTrace{logger: logger, ctx: r.Context(), id: id, client: client, session: session,
		started: now, phaseStarted: now, phase: "authorization", start: -1, end: -1, size: -1,
		done: make(chan struct{}), window: now,
		detailed: client != "" && len(r.Header.Values(MediaDetailHeader)) == 1 && r.Header.Get(MediaDetailHeader) == "1"}
	r = r.WithContext(context.WithValue(r.Context(), mediaTraceKey{}, t))
	w.Header().Set(MediaRequestIDHeader, id)
	return &mediaTraceResponse{ResponseWriter: w, trace: t}, r, t
}

func MediaTraceFrom(ctx context.Context) *MediaTrace {
	t, _ := ctx.Value(mediaTraceKey{}).(*MediaTrace)
	return t
}

func (t *MediaTrace) Authenticated(user int64, device string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.user = user
	if uuidPattern.MatchString(device) {
		t.device = device
	}
	t.emitLocked("authorized", slog.Int64("wait_us", time.Since(t.phaseStarted).Microseconds()))
	t.phase, t.phaseStarted = "handler", time.Now()
	detail := t.detailed
	t.mu.Unlock()
	if detail {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-t.done:
					return
				case now := <-ticker.C:
					t.progress(now)
				}
			}
		}()
	}
}

func (t *MediaTrace) progress(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.emitLocked("progress", t.statsLocked(now)...)
}

// Phase names are fixed call-site labels, never values from a URL/provider.
func (t *MediaTrace) Phase(phase string) func(error) {
	if t == nil {
		return func(error) {}
	}
	start := time.Now()
	t.mu.Lock()
	t.phase, t.phaseStarted = phase, start
	t.mu.Unlock()
	return func(err error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.emitLocked(phase, slog.Int64("wait_us", time.Since(start).Microseconds()), slog.String("code", mediaTraceCode(err)))
		if err != nil {
			t.code, t.failurePhase = mediaTraceCode(err), phase
		}
		if t.phaseStarted == start {
			t.phase, t.phaseStarted = "handler", time.Now()
		}
	}
}

// Reject reports a fixed validation failure without retaining provider data.
func (t *MediaTrace) Reject(code string) {
	if t == nil {
		return
	}
	switch code {
	case "range_mismatch", "source_etag_changed", "link_rejected":
	default:
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.code, t.failurePhase = code, "upstream_validation"
	t.emitLocked("upstream_validation", slog.String("code", code))
}

func (t *MediaTrace) Source(source string, start, end, size int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.source = source
	if t.start < 0 {
		t.start, t.end, t.size = start, end, size
	}
	t.emitLocked("range_selected", slog.Int64("range_start", start), slog.Int64("range_end", end), slog.Int64("file_bytes", size))
}

func (t *MediaTrace) Failure(err error) {
	if t == nil || err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.code == "" || t.code == "ok" {
		t.code = mediaTraceCode(err)
	}
	if t.failurePhase == "" {
		t.failurePhase = t.lastIOPhase
		if t.failurePhase == "" {
			t.failurePhase = t.phase
		}
	}
}

// Upstream attaches stdlib connection milestones without logging addresses,
// certificate names, redirect targets, response headers or raw transport errors.
func (t *MediaTrace) Upstream(req *http.Request) (*http.Request, func(*http.Response, error)) {
	if t == nil {
		return req, func(*http.Response, error) {}
	}
	t.mu.Lock()
	t.attempt++
	attempt := t.attempt
	detailed := t.detailed
	t.mu.Unlock()
	end := t.Phase("upstream_headers")
	started := time.Now()
	t.mu.Lock()
	span, rangeErr := parseRange(req.Header.Get("Range"), t.size)
	t.mu.Unlock()
	mark := func(phase string, attrs ...slog.Attr) {
		t.mu.Lock()
		defer t.mu.Unlock()
		attrs = append(attrs, slog.Int("upstream", attempt), slog.Int64("request_us", time.Since(started).Microseconds()))
		if rangeErr == nil {
			attrs = append(attrs, slog.Int64("upstream_start", span.start), slog.Int64("upstream_end", span.end))
		}
		t.emitLocked(phase, attrs...)
	}
	if detailed {
		trace := &httptrace.ClientTrace{
			DNSStart: func(httptrace.DNSStartInfo) { mark("dns_begin") },
			DNSDone: func(v httptrace.DNSDoneInfo) {
				mark("dns_done", slog.Int("addresses", len(v.Addrs)), slog.String("code", mediaTraceCode(v.Err)))
			},
			ConnectStart: func(network, addr string) { mark("connect_begin", slog.Int("ip_family", mediaIPFamily(addr))) },
			ConnectDone: func(network, addr string, err error) {
				mark("connect_done", slog.Int("ip_family", mediaIPFamily(addr)), slog.String("code", mediaTraceCode(err)))
			},
			TLSHandshakeStart: func() { mark("tls_begin") },
			TLSHandshakeDone:  func(_ tls.ConnectionState, err error) { mark("tls_done", slog.String("code", mediaTraceCode(err))) },
			GotConn: func(v httptrace.GotConnInfo) {
				mark("connection_ready", slog.Bool("reused", v.Reused), slog.Bool("was_idle", v.WasIdle), slog.Int64("idle_us", v.IdleTime.Microseconds()))
			},
			WroteRequest:         func(v httptrace.WroteRequestInfo) { mark("request_sent", slog.String("code", mediaTraceCode(v.Err))) },
			GotFirstResponseByte: func() { mark("first_response_byte") },
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	}
	return req, func(res *http.Response, err error) {
		end(err)
		if res != nil {
			mark("upstream_response", slog.Int("status", res.StatusCode), slog.Int64("length", res.ContentLength))
		}
		if err != nil {
			t.Failure(err)
		}
	}
}

func mediaIPFamily(addr string) int {
	host, _, _ := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if ip == nil {
		return 0
	}
	if ip.To4() != nil {
		return 4
	}
	return 6
}

func mediaTraceCode(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "short_body"
	}
	if errors.Is(err, io.ErrShortWrite) {
		return "short_write"
	}
	// Keep the existing safe RD classifications (rate limit, credentials,
	// incomplete/missing file and pinned-source mismatch), without raw errors.
	if code := probeTransportCode(err); code != "unavailable" {
		return code
	}
	return "failed"
}

func (t *MediaTrace) ioBegin(phase string) time.Time {
	start := time.Now()
	t.mu.Lock()
	t.phase, t.phaseStarted = phase, start
	t.lastIOPhase = phase
	t.mu.Unlock()
	return start
}

func (t *MediaTrace) ioEnd(read bool, start time.Time, n int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	duration := time.Since(start).Microseconds()
	if read {
		first := t.readBytes == 0 && n > 0
		t.readBytes += int64(n)
		t.readUS += duration
		t.maxReadUS = max(t.maxReadUS, duration)
		if first {
			t.emitLocked("first_body_byte", slog.Int64("wait_us", duration))
		}
	} else {
		first := t.writtenBytes == 0 && n > 0
		t.writtenBytes += int64(n)
		t.writeUS += duration
		t.maxWriteUS = max(t.maxWriteUS, duration)
		if first {
			t.emitLocked("first_downstream_write", slog.Int64("wait_us", duration))
		}
	}
	if err != nil && err != io.EOF {
		t.code = mediaTraceCode(err)
		t.failurePhase = t.phase
	}
	if t.phaseStarted == start {
		t.phase, t.phaseStarted = "handler", time.Now()
	}
}

func (t *MediaTrace) statsLocked(now time.Time) []slog.Attr {
	return []slog.Attr{slog.String("wait_phase", t.phase), slog.Int64("wait_us", max(0, now.Sub(t.phaseStarted).Microseconds())),
		slog.Int64("read_bytes", t.readBytes), slog.Int64("written_bytes", t.writtenBytes),
		slog.Int64("read_us", t.readUS), slog.Int64("write_us", t.writeUS),
		slog.Int64("max_read_us", t.maxReadUS), slog.Int64("max_write_us", t.maxWriteUS),
		slog.Int64("range_start", t.start), slog.Int64("range_end", t.end), slog.Int64("file_bytes", t.size),
		slog.Int64("next_offset", t.start+t.writtenBytes),
		slog.Bool("context_cancelled", t.ctx.Err() != nil), slog.Int("upstream", t.attempt)}
}

func (t *MediaTrace) emitLocked(phase string, attrs ...slog.Attr) {
	if t.logger == nil || t.finished {
		return
	}
	if phase != "finished" {
		if !t.detailed || t.user == 0 {
			return
		}
		if time.Since(t.window) >= time.Minute {
			t.window, t.events = time.Now(), 0
		}
		if t.events >= 120 && phase != "progress" {
			t.suppressed++
			return
		}
		t.events++
	}
	base := []slog.Attr{slog.String("phase", phase), slog.String("request_id", t.id), slog.String("client_request_id", t.client),
		slog.String("session_id", t.session), slog.Int64("user_id", t.user), slog.String("device_id", t.device),
		slog.String("source", t.source), slog.Int64("elapsed_us", time.Since(t.started).Microseconds())}
	level := slog.LevelInfo
	if phase == "finished" && t.code != "ok" && t.code != "cancelled" {
		level = slog.LevelWarn
	}
	t.logger.LogAttrs(t.ctx, level, "media delivery", append(base, attrs...)...)
}

func (t *MediaTrace) Finish() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	attrs := t.statsLocked(time.Now())
	if t.status == 0 {
		t.status = http.StatusOK
	}
	if t.code == "" {
		t.code = "ok"
		if t.ctx.Err() != nil {
			t.code = mediaTraceCode(t.ctx.Err())
		} else if t.status >= 400 {
			t.code = "http_error"
		}
	}
	attrs = append(attrs, slog.Int("status", t.status), slog.String("code", t.code), slog.String("failure_phase", t.failurePhase), slog.Int("suppressed_events", t.suppressed))
	t.emitLocked("finished", attrs...)
	t.finished = true
	close(t.done)
}

type mediaTraceResponse struct {
	http.ResponseWriter
	trace *MediaTrace
}

func (w *mediaTraceResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaTraceResponse) WriteHeader(status int) {
	w.trace.mu.Lock()
	if w.trace.status == 0 && status >= 200 {
		w.trace.status = status
		w.trace.emitLocked("downstream_headers", slog.Int("status", status))
	}
	w.trace.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}
func (w *mediaTraceResponse) Write(p []byte) (int, error) {
	if w.trace.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

type mediaTraceReader struct {
	io.Reader
	trace *MediaTrace
}

func (r mediaTraceReader) Read(p []byte) (int, error) {
	start := r.trace.ioBegin("source_body")
	n, err := r.Reader.Read(p)
	r.trace.ioEnd(true, start, n, err)
	return n, err
}

type mediaTraceWriter struct {
	io.Writer
	trace *MediaTrace
}

func (w mediaTraceWriter) Write(p []byte) (int, error) {
	start := w.trace.ioBegin("downstream_write")
	n, err := w.Writer.Write(p)
	w.trace.ioEnd(false, start, n, err)
	return n, err
}

// CopyMediaContext preserves CopyMedia's read/write/deadline semantics while
// measuring each side separately, including an in-progress blocked operation.
func CopyMediaContext(ctx context.Context, w http.ResponseWriter, reader io.Reader, length int64) (int64, error) {
	t := MediaTraceFrom(ctx)
	if t == nil {
		return CopyMedia(w, reader, length)
	}
	n, err := copyChunk(http.NewResponseController(w), mediaTraceWriter{w, t}, mediaTraceReader{reader, t}, length)
	t.Failure(err)
	return n, err
}
