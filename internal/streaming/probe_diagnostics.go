package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"entertainmentdistrict/internal/realdebrid"
)

// Probe diagnostics contain only fixed codes and numeric observations. Never
// retain provider URLs, arbitrary headers, media tags or raw process stderr.
type probeFailure struct {
	Cause              error             `json:"-"`
	Reason             string            `json:"reason"`
	ExpectedBytes      int64             `json:"expected_bytes"`
	ReportedBytes      *int64            `json:"reported_bytes,omitempty"`
	DurationSeconds    *float64          `json:"duration_seconds,omitempty"`
	StreamIndex        *int              `json:"stream_index,omitempty"`
	StreamType         string            `json:"stream_type,omitempty"`
	StreamCount        int               `json:"stream_count"`
	ExitCode           *int              `json:"exit_code,omitempty"`
	TimedOut           bool              `json:"timed_out,omitempty"`
	StderrSignals      []string          `json:"stderr_signals,omitempty"`
	RelayEvents        []probeRelayEvent `json:"relay_events,omitempty"`
	RelayEventsTotal   int               `json:"relay_events_total,omitempty"`
	RetryableTransport bool              `json:"retryable_transport,omitempty"`
}

func (e *probeFailure) Error() string {
	b, _ := json.Marshal(e)
	return e.Cause.Error() + ": " + string(b)
}
func (e *probeFailure) Unwrap() error { return e.Cause }

type probeRelayEvent struct {
	Stage        string `json:"stage"`
	Code         string `json:"code,omitempty"`
	Status       int    `json:"status,omitempty"`
	Start        int64  `json:"start"`
	End          int64  `json:"end"`
	Length       int64  `json:"length"`
	RangeMatches bool   `json:"range_matches,omitempty"`
}
type probeTraceKey struct{}
type probeTrace struct {
	mu             sync.Mutex
	events         []probeRelayEvent
	total          int
	upstreamFailed bool
}

func recordProbeEvent(ctx context.Context, e probeRelayEvent) {
	trace, _ := ctx.Value(probeTraceKey{}).(*probeTrace)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.total++
	trace.upstreamFailed = trace.upstreamFailed || retryableProbeEvent(e)
	if len(trace.events) == 12 {
		copy(trace.events, trace.events[1:])
		trace.events = trace.events[:11]
	}
	trace.events = append(trace.events, e)
}
func (t *probeTrace) snapshot() ([]probeRelayEvent, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]probeRelayEvent(nil), t.events...), t.total
}
func probeTransportCode(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "incomplete_body"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, realdebrid.ErrLimited):
		return "rd_rate_limited"
	case errors.Is(err, realdebrid.ErrCredentials):
		return "rd_credentials"
	case errors.Is(err, realdebrid.ErrIncomplete):
		return "rd_incomplete"
	case errors.Is(err, realdebrid.ErrNotFound):
		return "rd_not_found"
	case errors.Is(err, realdebrid.ErrMismatch):
		return "rd_source_mismatch"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "unavailable"
}

// Keep draining stderr after the capture limit; diagnostics cannot fail a probe.
type probeStderr struct{ data []byte }

func (b *probeStderr) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < 16384 {
		b.data = append(b.data, p[:min(len(p), 16384-len(b.data))]...)
	}
	return n, nil
}
func (b *probeStderr) signals() []string {
	text := strings.ToLower(string(b.data))
	var result []string
	for _, code := range []string{"http error 401", "http error 403", "http error 404", "http error 429", "http error 500", "http error 502", "http error 503", "connection timed out", "connection refused", "connection reset", "input/output error", "end of file", "invalid data found", "could not find codec parameters", "file ended prematurely", "stream ends prematurely", "moov atom not found"} {
		if strings.Contains(text, code) {
			result = append(result, code)
		}
	}
	if len(b.data) > 0 && len(result) == 0 {
		result = append(result, "unclassified_stderr")
	}
	return result
}

// A downstream close is normal when ffprobe seeks; it is not evidence that
// the source failed. Only explicit upstream/resolve failures justify retrying
// otherwise incomplete metadata, and never bypass metadata validation.
func retryableProbeEvent(e probeRelayEvent) bool {
	switch e.Stage {
	case "upstream_request":
		return e.Code == "unavailable" || e.Code == "timeout" || e.Code == "incomplete_body"
	case "resolve":
		return e.Code == "unavailable" || e.Code == "timeout" || e.Code == "rd_rate_limited" || e.Code == "rd_incomplete"
	case "upstream_response":
		return e.Status == 408 || e.Status == 429 || e.Status >= 500
	}
	return false
}
func (t *probeTrace) classifyIncomplete(f *probeFailure) {
	t.mu.Lock()
	failed := t.upstreamFailed
	t.mu.Unlock()
	if !failed {
		return
	}
	switch f.Reason {
	case "invalid_json", "missing_format", "missing_streams", "missing_size", "missing_codec", "missing_video":
		f.Cause = ErrUnavailable
		f.RetryableTransport = true
	}
}
