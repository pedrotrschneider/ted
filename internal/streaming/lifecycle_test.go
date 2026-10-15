package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/realdebrid"

	"github.com/riverqueue/river"
)

func nativeService(t *testing.T, original *Service) *Service {
	t.Helper()
	s, err := New(context.Background(), original.pool, original.rd, original.logger, 4)
	if err != nil {
		t.Fatal(err)
	}
	s.mappingMovies, s.mappingSeries, s.probe = original.mappingMovies, original.mappingSeries, original.probe
	return s
}

func TestNativeMappingActivatesOnlyAfterWholeBatchValidation(t *testing.T) {
	ctx := context.Background()
	old, _, who, _ := mappingTestService(t)
	s := nativeService(t, old)
	s.mappingSeries = largeMappingCatalog{2}
	op, err := s.SubmitMapping(ctx, who, "tt1245698", "series", largeSelection(2))
	if err != nil {
		t.Fatal(err)
	}
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	jobs := mappingFileJobs(t, s, op.ID)
	if err = (&mappingFileWorker{s: s}).Work(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.kind='episode'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial batch activated", count, err)
	}
	if err = (&mappingFileWorker{s: s}).Work(ctx, jobs[1]); err != nil {
		t.Fatal(err)
	}
	status, err := s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || status.Phase != "complete" || status.Committed != 2 || status.Validated != 2 || status.PublicationMode != PublicationNative {
		t.Fatal(status, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.kind='episode'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for _, job := range jobs {
		if err = (&mappingFileWorker{s: s}).Work(ctx, job); err != nil {
			t.Fatal("replay", err)
		}
	}
}

func TestNativeFailedBatchRetryPreservesCompletedBatchesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	old, _, who, _ := mappingTestService(t)
	s := nativeService(t, old)
	s.mappingBatchSize = 1
	s.mappingSeries = largeMappingCatalog{3}
	op, err := s.SubmitMapping(ctx, who, "tt1245698", "series", largeSelection(3))
	if err != nil {
		t.Fatal(err)
	}
	validateMapping(t, s, currentBatch(t, s, op.ID))
	s = nativeService(t, s) // The persisted one-file batch size still wins.
	second := currentBatch(t, s, op.ID)
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, second.ID)); err != nil {
		t.Fatal(err)
	}
	stale := mappingFileJobs(t, s, second.ID)[0]
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return nil, ErrProbeInvalid }
	if err = (&mappingFileWorker{s: s}).Work(ctx, stale); err == nil {
		t.Fatal("invalid source accepted")
	}
	status, err := s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || status.Phase != "failed" || status.Committed != 1 || !status.Retryable {
		t.Fatal(status, err)
	}
	if _, err = s.RetryMapping(ctx, who, "tt1245698", "series", op.ID); err != nil {
		t.Fatal(err)
	}
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return json.RawMessage(probeJSON), nil }
	if err = (&mappingFileWorker{s: s}).Work(ctx, stale); err != nil {
		t.Fatal("stale worker", err)
	}
	for range 2 {
		validateMapping(t, s, currentBatch(t, s, op.ID))
	}
	status, err = s.MappingStatus(ctx, "tt1245698", "series")
	if err != nil || status.Phase != "complete" || status.Committed != 3 || status.CompletedBatches != 3 {
		t.Fatal(status, err)
	}
}

func TestNativeActivationCommitFailureKeepsOldMappingAndCanReplay(t *testing.T) {
	ctx := context.Background()
	old, m, who, _ := mappingTestService(t)
	s := nativeService(t, old)
	selection := catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}}
	op, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", selection)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	job := mappingFileJobs(t, s, op.ID)[0]
	// Force the real PostgreSQL commit to fail after all activation SQL ran.
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION reject_activation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.phase='complete' THEN RAISE EXCEPTION 'controlled commit failure'; END IF; RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER reject_activation AFTER UPDATE ON mapping_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_activation()`); err != nil {
		t.Fatal(err)
	}
	if err = (&mappingFileWorker{s: s}).validate(ctx, job); err == nil {
		t.Fatal("failed commit reported success")
	}
	var unchanged bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_mappings WHERE id=$2) AND o.phase='validating' AND c.probe IS NULL FROM mapping_operations o JOIN mapping_candidates c ON c.operation_id=o.id WHERE o.id=$1`, op.ID, m.MappingID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("partial commit survived", unchanged, err)
	}
	if _, err = s.pool.Exec(ctx, "DROP TRIGGER reject_activation ON mapping_operations"); err != nil {
		t.Fatal(err)
	}
	s = nativeService(t, s)
	if err = (&mappingFileWorker{s: s}).validate(ctx, job); err != nil {
		t.Fatal("restart/replay", err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT phase='complete' FROM mapping_operations WHERE id=$1", op.ID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal(err)
	}
}

func TestNativeRemovalWaitsForFileUseAndReAddsStableIdentityWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	old, m, who, root := localFixture(t)
	s := nativeService(t, old)
	if err := s.ConfigureStorage(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseStorage)
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := s.pool.QueryRow(ctx, "SELECT generation FROM local_files WHERE movie_id=$1", m.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/"+m.ID, 0700); err != nil {
		t.Fatal(err)
	}
	partial, _ := localNames(m.ID, generation)
	for _, path := range []string{partial, m.ID + "/personal.txt"} {
		if err := os.WriteFile(root+"/"+path, []byte("retained"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"position_ticks":123}','TED',now())`, who.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,'manual','{"played":true,"position_ticks":123}',now())`, who.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	op, err := s.RemoveTitle(ctx, who, m.IMDB, "movie")
	if err != nil || op.Phase != "removing" {
		t.Fatal(op, err)
	}
	if _, err = s.media(ctx, m.ID, true); !errors.Is(err, ErrNotReady) {
		t.Fatal("deleted item playable", err)
	}
	_, release, err := s.fileLock(ctx, m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	worker := removalWorker{s: s}
	job := &river.Job[removalArgs]{Args: removalArgs{op.ID}}
	err = worker.Work(ctx, job)
	release()
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatal("cleanup did not wait for active file", err)
	}
	if err = worker.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root + "/" + partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned file retained", err)
	}
	if _, err = os.Stat(root + "/" + m.ID + "/personal.txt"); err != nil {
		t.Fatal("unrelated file removed", err)
	}
	var preserved bool
	if err = s.pool.QueryRow(ctx, `SELECT phase='complete' AND EXISTS(SELECT 1 FROM watch_current WHERE user_id=$2 AND media_id=$3 AND state->>'position_ticks'='123') AND EXISTS(SELECT 1 FROM watch_history WHERE user_id=$2 AND media_id=$3 AND state->>'position_ticks'='123') FROM library_removals WHERE id=$1`, op.ID, who.ID, m.ID).Scan(&preserved); err != nil || !preserved {
		t.Fatal("history/receipt invariants", preserved, err)
	}
	s.rd = &mappingRD{details: map[string]realdebrid.Detail{"T2": mappingSource("T2", "/new.mkv")}}
	s.mappingMovies = mappingCatalog{}
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return json.RawMessage(probeJSON), nil }
	selection := catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}}
	readd, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", selection)
	if err != nil {
		t.Fatal(err)
	}
	validateMapping(t, s, readd)
	current, err := s.media(ctx, m.ID, true)
	if err != nil || current.ID != m.ID || current.MappingID == m.MappingID {
		t.Fatal("re-add identity", current, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT phase='complete' FROM mapping_operations WHERE id=$1", readd.ID).Scan(&preserved); err != nil || !preserved {
		t.Fatal("re-add did not complete", err)
	}
}

func TestCoreWorkersRunWithoutRD(t *testing.T) {
	old, _, _, _ := mappingTestService(t)
	s, err := New(context.Background(), old.pool, nil, old.logger, 4)
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var complete int
		if err = s.pool.QueryRow(context.Background(), `SELECT count(DISTINCT kind) FROM river_job WHERE kind IN ('stremio_dispatch','catalog_metadata_dispatch','orphan_cleanup') AND state='completed'`).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		if complete == 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("core periodic workers did not complete without RD")
}
