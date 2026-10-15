package streaming

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
)

func parallelMapping(t *testing.T, s *Service, submit func() (MappingOperation, error)) MappingOperation {
	t.Helper()
	op, err := submit()
	if err != nil {
		t.Fatal(err)
	}
	if err = (&mappingWorker{s: s}).Work(context.Background(), mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	return op
}

func awaitMappingPhase(t *testing.T, s *Service, id, kind, phase string) {
	t.Helper()
	deadline := time.After(8 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		state, err := s.MappingStatus(context.Background(), id, kind)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == phase {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("mapping remained %+v, wanted %s", state, phase)
		case <-tick.C:
		}
	}
}

// Real River workers execute persisted children. Held probes make the worker
// limit and the all-files publication barrier observable without timing races.
func TestMappingParallelWorkersBoundedAndDurable(t *testing.T) {
	ctx := context.Background()
	s, m, who, _ := mappingTestService(t)
	if _, err := s.pool.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES($1,$2,'ready',true,$3)`, m.ID, m.MappingID, probeJSON); err != nil {
		t.Fatal(err)
	}
	batch := catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{
		{EpisodeID: "tt1245698:1:1", Selection: mappingSelection("T2", "/new.mkv")},
		{EpisodeID: "tt1245698:2:1", Selection: mappingSelection("T3", "/second.mkv")},
	}}
	op := parallelMapping(t, s, func() (MappingOperation, error) { return s.SubmitMapping(ctx, who, "tt1245698", "series", batch) })
	parallelMapping(t, s, func() (MappingOperation, error) {
		return s.SubmitMapping(ctx, who, m.IMDB, "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}})
	})
	before := mappingFileJobs(t, s, op.ID)
	// Coordinator replay must reuse the durable children, not enqueue duplicates.
	if err := (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	after := mappingFileJobs(t, s, op.ID)
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Fatal("coordinator replaced child")
		}
	}
	entered := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	var active, peak atomic.Int32
	s.probe = func(ctx context.Context, _ Media) (json.RawMessage, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
			return json.RawMessage(probeJSON), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// A fresh queue instance proves that persisted children survive the coordinator.
	restarted, err := New(ctx, s.pool, s.rd, s.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	restarted.mappingMovies = s.mappingMovies
	restarted.mappingSeries = s.mappingSeries
	restarted.probe = s.probe
	start(t, restarted)
	for range 2 {
		select {
		case <-entered:
		case <-time.After(8 * time.Second):
			t.Fatal("workers did not overlap")
		}
	}
	var running, total int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE state='running'),count(*) FROM river_job WHERE kind='mapping_file_validation'").Scan(&running, &total); err != nil || running != 2 || total != 3 {
		t.Fatal("worker bound", running, total, err)
	}
	state, err := s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || state.Phase != "validating" {
		t.Fatal("published held batch", state, err)
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("queued child did not continue")
	}
	release <- struct{}{}
	release <- struct{}{}
	awaitMappingPhase(t, s, "tt1245698", "series", "complete")
	awaitMappingPhase(t, s, m.IMDB, "movie", "complete")
	if peak.Load() != 2 {
		t.Fatal("concurrency", peak.Load())
	}
	current, err := s.media(ctx, m.ID, true)
	if err != nil || current.MappingID == m.MappingID {
		t.Fatal("validated mapping was not activated", current, err)
	}
}

func TestMappingParallelFailureFencesLateSuccess(t *testing.T) {
	ctx := context.Background()
	s, _, who, _ := mappingTestService(t)
	op := parallelMapping(t, s, func() (MappingOperation, error) {
		return s.SubmitMapping(ctx, who, "tt1245698", "series", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{
			{EpisodeID: "tt1245698:1:1", Selection: mappingSelection("T2", "/new.mkv")},
			{EpisodeID: "tt1245698:2:1", Selection: mappingSelection("T3", "/second.mkv")},
		}})
	})
	jobs := mappingFileJobs(t, s, op.ID)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	s.probe = func(_ context.Context, m Media) (json.RawMessage, error) {
		if m.MappingID == jobs[0].Args.Mapping {
			close(entered)
			<-release
			return json.RawMessage(probeJSON), nil
		}
		return nil, ErrProbeInvalid
	}
	go func() { done <- (&mappingFileWorker{s: s}).Work(ctx, jobs[0]) }()
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("probe did not start")
	}
	if err := (&mappingFileWorker{s: s}).Work(ctx, jobs[1]); err == nil {
		t.Error("invalid peer succeeded")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	state, err := s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || state.Phase != "failed" || state.Validated != 0 {
		t.Fatal("late worker revived batch", state, err)
	}
	peer, err := s.queue.JobGet(ctx, jobs[0].ID)
	if err != nil || peer.State != "cancelled" {
		t.Fatal("peer not durably cancelled", peer, err)
	}
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	state, err = s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || state.Phase != "failed" {
		t.Fatal("coordinator replay revived failed batch", state, err)
	}
}

func TestMappingRestartKeepsValidatedFiles(t *testing.T) {
	ctx := context.Background()
	s, _, who, _ := mappingTestService(t)
	op := parallelMapping(t, s, func() (MappingOperation, error) {
		return s.SubmitMapping(ctx, who, "tt1245698", "series", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{
			{EpisodeID: "tt1245698:1:1", Selection: mappingSelection("T2", "/new.mkv")},
			{EpisodeID: "tt1245698:2:1", Selection: mappingSelection("T3", "/second.mkv")},
		}})
	})
	jobs := mappingFileJobs(t, s, op.ID)
	if err := (&mappingFileWorker{s: s}).Work(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-upgrade coordinator's saved partial progress: no child
	// ownership existed yet. Completed probes must not be re-run on recovery.
	if _, err := s.pool.Exec(ctx, "WITH removed AS (DELETE FROM river_job WHERE kind='mapping_file_validation') UPDATE mapping_candidates SET validation_job_id=NULL WHERE operation_id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(ctx, s.pool, s.rd, s.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	restarted.mappingMovies = s.mappingMovies
	restarted.mappingSeries = s.mappingSeries
	var calls atomic.Int32
	restarted.probe = func(_ context.Context, m Media) (json.RawMessage, error) {
		calls.Add(1)
		if m.MappingID == jobs[0].Args.Mapping {
			return nil, ErrProbeInvalid
		}
		return json.RawMessage(probeJSON), nil
	}
	start(t, restarted)
	awaitMappingPhase(t, s, "tt1245698", "series", "complete")
	if calls.Load() != 1 {
		t.Fatal("repeated saved validation", calls.Load())
	}
	var validated, children int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE probe IS NOT NULL),count(validation_job_id) FROM mapping_candidates WHERE operation_id=$1", op.ID).Scan(&validated, &children); err != nil || validated != 2 || children != 1 {
		t.Fatal(validated, children, err)
	}
}
