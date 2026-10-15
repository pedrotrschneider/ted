package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"entertainmentdistrict/internal/catalog"

	"github.com/riverqueue/river"
)

func incompleteTransportProbe() error {
	trace := &probeTrace{}
	ctx := context.WithValue(context.Background(), probeTraceKey{}, trace)
	recordProbeEvent(ctx, probeRelayEvent{Stage: "upstream_request", Code: "unavailable"})
	failure := &probeFailure{Cause: ErrProbeInvalid, Reason: "missing_size", ExpectedBytes: 1000}
	trace.classifyIncomplete(failure)
	return failure
}
func TestProbeRetryRequiresTransportEvidenceAndIncompleteMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		event        probeRelayEvent
		retry        bool
	}{
		{"failed tail", "missing_size", probeRelayEvent{Stage: "upstream_request", Code: "unavailable"}, true},
		{"busy upstream", "missing_codec", probeRelayEvent{Stage: "upstream_response", Status: 503}, true},
		{"normal seek closes body", "missing_size", probeRelayEvent{Stage: "body_copy", Code: "unavailable"}, false},
		{"valid response missing codec", "missing_codec", probeRelayEvent{Stage: "upstream_response", Status: 206}, false},
		{"wrong source size", "size_mismatch", probeRelayEvent{Stage: "upstream_request", Code: "unavailable"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := &probeTrace{}
			ctx := context.WithValue(context.Background(), probeTraceKey{}, trace)
			recordProbeEvent(ctx, tc.event)
			// Correctness cannot depend on the bounded diagnostic history retaining an event.
			for range 20 {
				recordProbeEvent(ctx, probeRelayEvent{Stage: "upstream_response", Status: 206})
			}
			failure := &probeFailure{Cause: ErrProbeInvalid, Reason: tc.reason}
			trace.classifyIncomplete(failure)
			if errors.Is(failure, ErrUnavailable) != tc.retry || errors.Is(failure, ErrProbeInvalid) == tc.retry || failure.RetryableTransport != tc.retry {
				t.Fatal(failure)
			}
		})
	}
}
func TestMappingTransientProbeRetryPreservesSiblingValidation(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovers", true: "exhausted"}[exhausted], func(t *testing.T) {
			ctx := context.Background()
			s, _, who, _ := mappingTestService(t)
			op := parallelMapping(t, s, func() (MappingOperation, error) {
				return s.SubmitMapping(ctx, who, "tt1245698", "series", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{
					{EpisodeID: "tt1245698:1:1", Selection: mappingSelection("T2", "/new.mkv")},
					{EpisodeID: "tt1245698:2:1", Selection: mappingSelection("T3", "/second.mkv")},
				}})
			})
			jobs := mappingFileJobs(t, s, op.ID)
			worker := &mappingFileWorker{s: s}
			if err := worker.Work(ctx, jobs[0]); err != nil {
				t.Fatal(err)
			}
			s.probe = func(context.Context, Media) (json.RawMessage, error) { return nil, incompleteTransportProbe() }
			jobs[1].Attempt = 1
			err := worker.Work(ctx, jobs[1])
			var cancelled *river.JobCancelError
			if err == nil || errors.As(err, &cancelled) {
				t.Fatal("transient failure cancelled the batch", err)
			}
			state, err := s.MappingStatus(ctx, "tt1245698", "series")
			if err != nil || state.Phase != "validating" || state.Validated != 1 {
				t.Fatal("lost completed sibling", state, err)
			}
			retained := mappingFileJobs(t, s, op.ID)
			for i := range jobs {
				if jobs[i].ID != retained[i].ID {
					t.Fatal("replaced durable children")
				}
			}
			if exhausted {
				jobs[1].Attempt = jobs[1].MaxAttempts
				err = worker.Work(ctx, jobs[1])
				if !errors.As(err, &cancelled) {
					t.Fatal("retry limit ignored", err)
				}
				state, err = s.MappingStatus(ctx, "tt1245698", "series")
				if err != nil || state.Phase != "failed" || state.Committed != 0 {
					t.Fatal(state, err)
				}
				return
			}
			jobs[1].Attempt = 2
			s.probe = func(_ context.Context, m Media) (json.RawMessage, error) {
				if m.MappingID != jobs[1].Args.Mapping {
					t.Error("reprobed completed sibling")
				}
				return json.RawMessage(probeJSON), nil
			}
			if err = worker.Work(ctx, jobs[1]); err != nil {
				t.Fatal(err)
			}
			state, err = s.MappingStatus(ctx, "tt1245698", "series")
			if err != nil || state.Phase != "complete" || state.Validated != 2 || state.Committed != 2 {
				t.Fatal(state, err)
			}
		})
	}
}
