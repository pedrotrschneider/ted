package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/realdebrid"
)

const traceSession = "a629058d-513f-43be-8c22-963d0a345c7a"
const traceClient = "b629058d-513f-43be-8c22-963d0a345c7a"

type traceLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *traceLog) Write(p []byte) (int, error) { b.Lock(); defer b.Unlock(); return b.Buffer.Write(p) }
func (b *traceLog) records(t *testing.T) []map[string]any {
	t.Helper()
	b.Lock()
	defer b.Unlock()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.Buffer.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func tracedRequest(t *testing.T, ctx context.Context, w http.ResponseWriter) (http.ResponseWriter, *http.Request, *MediaTrace, *traceLog) {
	t.Helper()
	log := &traceLog{}
	r := httptest.NewRequest("GET", "/media?token=PRIVATE", nil).WithContext(ctx)
	r.Header.Set(MediaClientRequestIDHeader, traceClient)
	r.Header.Set(MediaDetailHeader, "1")
	r.Header.Set("Authorization", "Bearer PRIVATE")
	w, r, trace := BeginMediaTrace(w, r, slog.New(slog.NewJSONHandler(log, nil)), traceSession)
	trace.Authenticated(7, traceClient)
	t.Cleanup(trace.Finish)
	return w, r, trace, log
}

type heldTraceRead struct {
	io.Reader
	entered, release chan struct{}
	once             sync.Once
}

func (r *heldTraceRead) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.Reader.Read(p)
}

type heldTraceWrite struct {
	*httptest.ResponseRecorder
	entered, release chan struct{}
	once             sync.Once
	deadlines        []time.Time
}

func (w *heldTraceWrite) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseRecorder.Write(p)
}
func (w *heldTraceWrite) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestMediaTraceIdentifiesBlockedSideWithoutChangingDelivery(t *testing.T) {
	for _, upstream := range []bool{true, false} {
		name := "downstream_write"
		if upstream {
			name = "source_body"
		}
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter = recorder
			var reader io.Reader = strings.NewReader("exact-range")
			var held *heldTraceWrite
			if upstream {
				reader = &heldTraceRead{Reader: reader, entered: entered, release: release}
			} else {
				held = &heldTraceWrite{ResponseRecorder: recorder, entered: entered, release: release}
				writer = held
			}
			w, r, trace, log := tracedRequest(t, context.Background(), writer)
			trace.Source("mapped_rd", 123, 133, 1000)
			w.Header().Set("Content-Range", "bytes 123-133/1000")
			w.Header().Set("Content-Length", "11")
			w.WriteHeader(206)
			finished := make(chan error, 1)
			go func() {
				n, err := CopyMediaContext(r.Context(), w, reader, 11)
				if n != 11 && err == nil {
					err = io.ErrShortWrite
				}
				finished <- err
			}()
			<-entered
			trace.progress(time.Now()) // Observe while the operation is still held.
			records := log.records(t)
			progress := records[len(records)-1]
			if progress["phase"] != "progress" || progress["wait_phase"] != name || progress["written_bytes"] != float64(0) {
				t.Fatalf("wrong blocked side: %#v", progress)
			}
			wantRead := float64(11)
			if upstream {
				wantRead = 0
			}
			if progress["read_bytes"] != wantRead {
				t.Fatalf("upstream receipt confused with downstream delivery: %#v", progress)
			}
			close(release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			trace.Finish()
			records = log.records(t)
			final := records[len(records)-1]
			if final["phase"] != "finished" || final["read_bytes"] != float64(11) || final["written_bytes"] != float64(11) || final["next_offset"] != float64(134) {
				t.Fatalf("wrong byte ledger: %#v", final)
			}
			if recorder.Code != 206 || recorder.Body.String() != "exact-range" || recorder.Header().Get("Content-Range") != "bytes 123-133/1000" {
				t.Fatal("tracing changed ranged delivery")
			}
			if held != nil && (len(held.deadlines) != 1 || time.Until(held.deadlines[0]) > 30*time.Second || time.Until(held.deadlines[0]) < 25*time.Second) {
				t.Fatal("rolling deadline did not reach underlying writer")
			}
		})
	}
}

func TestMediaTraceSeparatesTLSHeadersAndBodyAndRedactsProviderData(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("ETag", "PRIVATE")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("data"))
	}))
	defer origin.Close()
	w, r, trace, log := tracedRequest(t, context.Background(), httptest.NewRecorder())
	trace.Source("addon_rd", 0, 3, 4)
	req, _ := http.NewRequestWithContext(r.Context(), "GET", origin.URL+"/PRIVATE?token=PRIVATE", nil)
	req.Header.Set("Range", "bytes=0-3")
	req.Header.Set("Authorization", "Bearer PRIVATE")
	req, done := trace.Upstream(req)
	response := make(chan *http.Response, 1)
	go func() { res, err := origin.Client().Do(req); done(res, err); response <- res }()
	<-entered
	trace.progress(time.Now())
	records := log.records(t)
	if records[len(records)-1]["wait_phase"] != "upstream_headers" {
		t.Fatal("header wait confused with body read")
	}
	close(release)
	res := <-response
	if res == nil {
		t.Fatal("upstream failed")
	}
	defer res.Body.Close()
	if n, err := CopyMediaContext(r.Context(), w, res.Body, 4); n != 4 || err != nil {
		t.Fatalf("copy: %d %v", n, err)
	}
	trace.Failure(errors.New("https://provider.invalid/PRIVATE?token=PRIVATE"))
	trace.Finish()
	rows := log.records(t)
	if rows[len(rows)-1]["level"] != "WARN" {
		t.Fatal("transfer failure was not actionable without verbose logging")
	}
	seen := map[string]bool{}
	for _, row := range log.records(t) {
		seen[row["phase"].(string)] = true
		if row["client_request_id"] != traceClient || row["request_id"] != w.Header().Get(MediaRequestIDHeader) {
			t.Fatal("lost correlation")
		}
		data, _ := json.Marshal(row)
		if bytes.Contains(data, []byte("PRIVATE")) || bytes.Contains(data, []byte(origin.URL)) || bytes.Contains(data, []byte("provider.invalid")) {
			t.Fatalf("private data in log: %s", data)
		}
	}
	for _, phase := range []string{"connect_begin", "connect_done", "tls_begin", "tls_done", "upstream_response", "first_body_byte", "first_downstream_write", "finished"} {
		if !seen[phase] {
			t.Errorf("missing %s", phase)
		}
	}
}

func TestMediaTraceIgnoresMalformedCorrelationAndKeepsOrdinarySummary(t *testing.T) {
	log := &traceLog{}
	r := httptest.NewRequest("GET", "/PRIVATE?token=PRIVATE", nil)
	r.Header.Set(MediaClientRequestIDHeader, "https://provider.invalid/PRIVATE")
	r.Header.Set(MediaDetailHeader, "1")
	w, r, trace := BeginMediaTrace(httptest.NewRecorder(), r, slog.New(slog.NewJSONHandler(log, nil)), "PRIVATE")
	trace.Authenticated(7, "PRIVATE")
	trace.Source("local", 0, 3, 4)
	n, err := CopyMediaContext(r.Context(), w, strings.NewReader("data"), 4)
	trace.progress(time.Now())
	trace.Finish()
	if n != 4 || err != nil {
		t.Fatalf("malformed diagnostics changed delivery: %d %v", n, err)
	}
	rows := log.records(t)
	if len(rows) != 1 || rows[0]["phase"] != "finished" || rows[0]["written_bytes"] != float64(4) {
		t.Fatalf("ordinary summary lost or details enabled: %#v", rows)
	}
	if rows[0]["client_request_id"] != "" || rows[0]["session_id"] != "" || rows[0]["device_id"] != "" {
		t.Fatal("unvalidated correlation was trusted")
	}
}

func TestMediaTracePreservesSafeRDPhaseFailureThroughGenericHandlerError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{realdebrid.ErrLimited, "rd_rate_limited"},
		{realdebrid.ErrCredentials, "rd_credentials"},
		{realdebrid.ErrIncomplete, "rd_incomplete"},
		{realdebrid.ErrNotFound, "rd_not_found"},
		{realdebrid.ErrMismatch, "rd_source_mismatch"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			w, _, trace, log := tracedRequest(t, context.Background(), httptest.NewRecorder())
			trace.Phase("rd_resolve_file")(fmt.Errorf("%w: https://provider.invalid/PRIVATE", tc.err))
			trace.Failure(ErrUnavailable)
			w.WriteHeader(503)
			trace.Finish()
			rows := log.records(t)
			final := rows[len(rows)-1]
			if final["code"] != tc.code || final["failure_phase"] != "rd_resolve_file" || final["level"] != "WARN" {
				t.Fatalf("specific RD cause lost: %#v", final)
			}
			for _, row := range rows {
				data, _ := json.Marshal(row)
				if bytes.Contains(data, []byte("PRIVATE")) || bytes.Contains(data, []byte("provider.invalid")) {
					t.Fatal("raw provider failure entered trace")
				}
			}
		})
	}
}

func TestMediaTraceRetainsCancellationSummaryAfterSampleLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w, r, trace, log := tracedRequest(t, ctx, httptest.NewRecorder())
	for range 1000 {
		trace.Phase("link_cache_lookup")(nil)
	}
	trace.Source("mapped_rd", 100, 109, 1000)
	reader := &streamPatternBody{ctx: ctx, position: 100, remain: 10, maxRead: 5}
	original := w.(*mediaTraceResponse).ResponseWriter
	w.(*mediaTraceResponse).ResponseWriter = &cancelTraceWriter{ResponseWriter: original, cancel: cancel}
	n, err := CopyMediaContext(r.Context(), w, reader, 10)
	if n != 5 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation changed: n=%d err=%v", n, err)
	}
	trace.progress(time.Now())
	trace.Finish()
	trace.Finish()
	records := log.records(t)
	if len(records) > 123 {
		t.Fatalf("unbounded trace: %d", len(records))
	}
	final := records[len(records)-1]
	if final["phase"] != "finished" || final["code"] != "cancelled" || final["failure_phase"] != "source_body" || final["written_bytes"] != float64(5) || final["suppressed_events"].(float64) < 800 {
		t.Fatalf("lost terminal failure after saturation: %#v", final)
	}
	if records[len(records)-2]["phase"] != "progress" {
		t.Fatal("sample budget hid blocked-call heartbeat")
	}
}

type cancelTraceWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
}

func (w *cancelTraceWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.cancel()
	return n, err
}
