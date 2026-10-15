package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestProbeDiagnosticsIdentifyRejectedFieldsWithoutMetadataLeaks(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(map[string]any)
	}{
		{"missing size", "missing_size", func(v map[string]any) { delete(v["format"].(map[string]any), "size") }},
		{"wrong size", "size_mismatch", func(v map[string]any) { v["format"].(map[string]any)["size"] = "9" }},
		{"missing codec", "missing_codec", func(v map[string]any) { delete(v["streams"].([]any)[0].(map[string]any), "codec_name") }},
		{"invalid size", "invalid_size", func(v map[string]any) { v["format"].(map[string]any)["size"] = "https://private.invalid/secret-token" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data map[string]any
			_ = json.Unmarshal(probeWithStreams([]map[string]any{{"index": 0, "codec_type": "video", "codec_name": "h264"}}), &data)
			data["tags"] = "secret-token"
			tc.change(data)
			raw, _ := json.Marshal(data)
			err := validateProbe(raw, 1465140070)
			var detail *probeFailure
			if !errors.Is(err, ErrProbeInvalid) || !errors.As(err, &detail) || detail.Reason != tc.reason {
				t.Fatal(err)
			}
			if detail.ExpectedBytes != 1465140070 || strings.Contains(err.Error(), "secret-token") {
				t.Fatal("unsafe or missing details", err)
			}
			if tc.reason == "missing_codec" && (detail.StreamIndex == nil || *detail.StreamIndex != 0 || detail.StreamType != "video") {
				t.Fatal(detail)
			}
			if tc.reason == "size_mismatch" && (detail.ReportedBytes == nil || *detail.ReportedBytes != 9) {
				t.Fatal(detail)
			}
		})
	}
}
func TestProbeDiagnosticsBoundConcurrentRelayEventsAndSanitizeStderr(t *testing.T) {
	trace := &probeTrace{}
	ctx := context.WithValue(context.Background(), probeTraceKey{}, trace)
	var wg sync.WaitGroup
	for n := range 40 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			recordProbeEvent(ctx, probeRelayEvent{Stage: "upstream_response", Status: 503, Start: int64(n)})
		}(n)
	}
	wg.Wait()
	events, total := trace.snapshot()
	if total != 40 || len(events) != 12 {
		t.Fatal(total, len(events))
	}
	events[0].Stage = "changed"
	again, _ := trace.snapshot()
	if again[0].Stage == "changed" {
		t.Fatal("snapshot aliases active trace")
	}
	var stderr probeStderr
	input := []byte("https://private.invalid/secret-token HTTP error 503 " + strings.Repeat("private-tag", 10000))
	if n, err := stderr.Write(input); err != nil || n != len(input) || len(stderr.data) != 16384 {
		t.Fatal(n, err, len(stderr.data))
	}
	failure := &probeFailure{Cause: ErrUnavailable, Reason: "process_failed", StderrSignals: stderr.signals(), RelayEvents: again, RelayEventsTotal: total}
	if !strings.Contains(failure.Error(), "http error 503") || strings.Contains(failure.Error(), "private") || strings.Contains(failure.Error(), "secret-token") {
		t.Fatal(failure)
	}
}
func TestMappingFailureRetainsProbeDiagnosticsAndEpisodeContext(t *testing.T) {
	s, _, who, _ := mappingTestService(t)
	ctx := context.Background()
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	op, err := s.SubmitMapping(ctx, who, "tt1245698", "series", largeSelection(1))
	if err != nil {
		t.Fatal(err)
	}
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	s.probe = func(context.Context, Media) (json.RawMessage, error) {
		return nil, &probeFailure{Cause: ErrProbeInvalid, Reason: "missing_size", ExpectedBytes: 1000}
	}
	job := mappingFileJobs(t, s, op.ID)[0]
	err = (&mappingFileWorker{s: s}).Work(ctx, job)
	if err == nil || !strings.Contains(err.Error(), "missing_size") {
		t.Fatal("queue error lost rejection", err)
	}
	for _, want := range []string{"probe_details", "missing_size", "tt1245698:1:1", op.ID} {
		if !strings.Contains(logs.String(), want) {
			t.Fatal("log missing", want)
		}
	}
	state, err := s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || state.Phase != "failed" || state.Error != "probe_invalid" {
		t.Fatal("failure semantics changed", state, err)
	}
}
