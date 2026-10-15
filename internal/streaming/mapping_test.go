package streaming

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/realdebrid"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

type mappingRD struct {
	resolver
	details map[string]realdebrid.Detail
	during  func()
}

func (r *mappingRD) AccountID(context.Context) (string, error) { return "123", nil }
func (r *mappingRD) Torrent(_ context.Context, id string) (realdebrid.Detail, error) {
	if r.during != nil {
		r.during()
	}
	d, ok := r.details[id]
	if !ok {
		return d, realdebrid.ErrNotFound
	}
	return d, nil
}

type mappingCatalog struct{}

func (mappingCatalog) Movie(_ context.Context, id string) (cinemeta.Movie, error) {
	return cinemeta.Movie{ID: id, Type: "movie", Name: "Movie", Year: "2012", Genres: []string{}}, nil
}
func (mappingCatalog) Series(_ context.Context, id string) (cinemeta.Series, error) {
	return cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: "Show", Year: "2012", Genres: []string{}}, Episodes: []cinemeta.Episode{{ID: id + ":1:1", Season: 1, Number: 1, Name: "One"}, {ID: id + ":2:1", Season: 2, Number: 1, Name: "Two"}}}, nil
}
func mappingSource(torrent, path string) realdebrid.Detail {
	return realdebrid.Detail{Torrent: realdebrid.Torrent{ID: torrent, Name: "Release " + torrent}, Hash: strings.Repeat("b", 40), Files: []realdebrid.File{{ID: 1, Path: path, Bytes: 1000, Video: true, Selected: true}}}
}
func mappingSelection(torrent, path string) catalog.Selection {
	return catalog.Selection{TorrentID: torrent, Hash: strings.Repeat("b", 40), FileID: 1, Path: path, Bytes: 1000}
}
func mappingJob(t *testing.T, s *Service, id string) *river.Job[mappingArgs] {
	t.Helper()
	var jobID int64
	if err := s.pool.QueryRow(context.Background(), `SELECT job_id FROM mapping_operations WHERE id=$1`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	row, err := s.queue.JobGet(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	row.Attempt = 3
	return &river.Job[mappingArgs]{JobRow: row, Args: mappingArgs{ID: id}}
}
func mappingFileJobs(t *testing.T, s *Service, operation string) []*river.Job[mappingFileArgs] {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT validation_job_id,mapping_id::text FROM mapping_candidates WHERE operation_id=$1 ORDER BY media_id", operation)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		id      int64
		mapping string
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.id, &e.mapping); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	jobs := []*river.Job[mappingFileArgs]{}
	for _, e := range entries {
		row, err := s.queue.JobGet(context.Background(), e.id)
		if err != nil {
			t.Fatal(err)
		}
		row.Attempt = 3
		jobs = append(jobs, &river.Job[mappingFileArgs]{JobRow: row, Args: mappingFileArgs{operation, e.mapping}})
	}
	return jobs
}
func validateMapping(t *testing.T, s *Service, op MappingOperation) {
	t.Helper()
	if err := (&mappingWorker{s: s}).Work(context.Background(), mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	for _, job := range mappingFileJobs(t, s, op.ID) {
		if err := (&mappingFileWorker{s: s}).Work(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
}
func mappingTestService(t *testing.T) (*Service, Media, accounts.Identity, *mappingRD) {
	rd := &mappingRD{details: map[string]realdebrid.Detail{"T2": mappingSource("T2", "/new.mkv"), "T3": mappingSource("T3", "/second.mkv")}}
	s, m, who := fixture(t, rd)
	s.mappingMovies = mappingCatalog{}
	s.mappingSeries = mappingCatalog{}
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return json.RawMessage(probeJSON), nil }
	return s, m, who, rd
}

func TestMappingLateProbeFailurePreservesWholeMultiTorrentBatch(t *testing.T) {
	ctx := context.Background()
	s, _, who, _ := mappingTestService(t)
	batch := catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: "tt1245698:1:1", Selection: mappingSelection("T2", "/new.mkv")}, {EpisodeID: "tt1245698:2:1", Selection: mappingSelection("T3", "/second.mkv")}}}
	op, err := s.SubmitMapping(ctx, who, "tt1245698", "series", batch)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	s.probe = func(context.Context, Media) (json.RawMessage, error) {
		calls++
		if calls == 2 {
			return nil, ErrProbeInvalid
		}
		return json.RawMessage(probeJSON), nil
	}
	w := mappingWorker{s: s}
	job := mappingJob(t, s, op.ID)
	if err = w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	files := mappingFileJobs(t, s, op.ID)
	if err = (&mappingFileWorker{s: s}).Work(ctx, files[0]); err != nil {
		t.Fatal(err)
	}
	if err = (&mappingFileWorker{s: s}).Work(ctx, files[1]); err == nil {
		t.Fatal("late failure ignored")
	}
	var n int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.kind='episode'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial active batch", n, err)
	}
	status, _ := s.MappingStatus(ctx, "tt1245698", "series")
	if status.Phase != "failed" {
		t.Fatal(status)
	}
	// Retrying the entire reviewed draft can complete without changing reserved IDs.
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return json.RawMessage(probeJSON), nil }
	retry, err := s.SubmitMapping(ctx, who, "tt1245698", "series", batch)
	if err != nil {
		t.Fatal(err)
	}
	validateMapping(t, s, retry)
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.kind='episode'`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
func TestMappingFencesDownloadAndCleansOnlyDetachedGeneration(t *testing.T) {
	ctx := context.Background()
	s, m, who, _ := mappingTestService(t)
	root := t.TempDir()
	if err := s.ConfigureStorage(root); err != nil {
		t.Fatal(err)
	}
	defer s.CloseStorage()
	if _, err := s.pool.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES($1,$2,'ready',true,$3)`, m.ID, m.MappingID, probeJSON); err != nil {
		t.Fatal(err)
	}
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var generation, jobID int64
	if err := s.pool.QueryRow(ctx, `SELECT generation,job_id FROM local_files WHERE movie_id=$1`, m.ID).Scan(&generation, &jobID); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, m.ID), 0700); err != nil {
		t.Fatal(err)
	}
	partial, final := localNames(m.ID, generation)
	for _, name := range []string{partial, final, "personal.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("kept until success"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO orphan_files(media_id,generation) VALUES($1,$2)`, m.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanOrphan(ctx, m.ID, generation); !errors.Is(err, ErrLocalBusy) {
		t.Fatal("referenced generation was not protected", err)
	}
	if _, err := os.Stat(filepath.Join(root, final)); err != nil {
		t.Fatal("referenced file removed", err)
	}
	op, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}})
	if err != nil {
		t.Fatal(err)
	}
	validateMapping(t, s, op)
	state, err := s.LocalStatus(ctx, m.IMDB)
	if err != nil || state.Phase != "absent" || state.Completed != 0 {
		t.Fatal(state, err)
	}
	oldJob, err := s.queue.JobGet(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.downloadFile(ctx, &river.Job[downloadArgs]{JobRow: oldJob, Args: downloadArgs{MovieID: m.ID}}); err == nil {
		t.Fatal("stale worker continued")
	}
	if err = s.cleanOrphan(ctx, m.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err = s.cleanOrphan(ctx, m.ID, generation); err != nil {
		t.Fatal("cleanup replay", err)
	}
	for _, name := range []string{partial, final} {
		if _, err = os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("orphan remains", name, err)
		}
	}
	if _, err = os.Stat(filepath.Join(root, "personal.txt")); err != nil {
		t.Fatal("unrelated file removed", err)
	}
	if err = s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err = s.pool.QueryRow(ctx, `SELECT generation FROM local_files WHERE movie_id=$1`, m.ID).Scan(&next); err != nil || next == generation {
		t.Fatal("generation reused", next, err)
	}
}

func TestLatePreparationCannotRepublishSupersededMapping(t *testing.T) {
	ctx := context.Background()
	s, m, who, _ := mappingTestService(t)
	if err := s.Enqueue(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	state, err := s.Status(ctx, m.IMDB)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.queue.JobGet(ctx, *state.JobID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s.probe = func(_ context.Context, media Media) (json.RawMessage, error) {
		if media.MappingID == m.MappingID {
			close(entered)
			<-release
		}
		return json.RawMessage(probeJSON), nil
	}
	// Avoid network reads in the legacy worker's source-resolution stage.
	if _, err = s.pool.Exec(ctx, `INSERT INTO streaming_links(mapping_id,credential_scope,download_url,expires_at) VALUES($1,'test-account','http://unused.test/media',now()+interval '1 hour')`, m.MappingID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- (&worker{s: s}).Work(ctx, &river.Job[prepareArgs]{JobRow: row, Args: prepareArgs{MovieID: m.ID}})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach held probe")
	}
	op, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	validateMapping(t, s, op)
	close(release)
	var cancelled *river.JobCancelError
	if err = <-finished; !errors.As(err, &cancelled) {
		t.Fatal("late worker was not fenced", err)
	}
	current, err := s.media(ctx, m.ID, true)
	if err != nil || current.MappingID == m.MappingID || current.Path != "/new.mkv" {
		t.Fatal("old worker replaced new publication", current, err)
	}
}
func TestStaleSourceAndRevokedActorLeaveNoActiveCandidate(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed file", true: "revoked actor"}[revoke], func(t *testing.T) {
			ctx := context.Background()
			s, m, who, rd := mappingTestService(t)
			selection := mappingSelection("T2", "/new.mkv")
			if !revoke {
				selection.Bytes++
			}
			op, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: selection}}})
			if err != nil {
				t.Fatal(err)
			}
			if revoke {
				rd.during = func() {
					if _, err = s.pool.Exec(ctx, `UPDATE users SET session_version=session_version+1 WHERE id=$1`, who.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err == nil {
				t.Fatal("invalid candidate accepted")
			}
			var active string
			if err = s.pool.QueryRow(ctx, `SELECT id::text FROM media_mappings WHERE movie_id=$1`, m.ID).Scan(&active); err != nil || active != m.MappingID {
				t.Fatal("old mapping changed", active, err)
			}
			var staged int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM mapping_candidates WHERE operation_id=$1`, op.ID).Scan(&staged); err != nil || staged != 0 {
				t.Fatal("invalid observations persisted", staged, err)
			}
		})
	}
}

func TestStreamingStatusTracksPendingEpisodeMembership(t *testing.T) {
	ctx := context.Background()
	s, _, who, _ := mappingTestService(t)
	id := "tt1245698"
	selected := id + ":1:1"
	op, err := s.SubmitMapping(ctx, who, id, "series", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: selected, Selection: mappingSelection("T2", "/new.mkv")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "validating", "publishing", "failed"} {
		if _, err = s.pool.Exec(ctx, "UPDATE mapping_operations SET phase=$2 WHERE id=$1", op.ID, phase); err != nil {
			t.Fatal(err)
		}
		state, err := s.Status(ctx, selected)
		if err != nil || state.Adding != (phase != "failed") || state.Published {
			t.Fatal(phase, state, err)
		}
		local, localErr := s.LocalStatus(ctx, selected)
		if localErr != nil || local.Mapped != state.Mapped || local.Eligible {
			t.Fatal("public mapping status", phase, local, localErr)
		}
		other, err := s.Status(ctx, id+":2:1")
		if err != nil || other.Adding || other.Published {
			t.Fatal("unselected episode", other, err)
		}
	}
}

func TestMappingFailedValidationClearsProvisionalEpisodeMapping(t *testing.T) {
	ctx := context.Background()
	s, _, who, _ := mappingTestService(t)
	id := "tt1245698"
	op, err := s.SubmitMapping(ctx, who, id, "series", catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: id + ":1:1", Selection: mappingSelection("T2", "/new.mkv")}}})
	if err != nil {
		t.Fatal(err)
	}
	s.probe = func(context.Context, Media) (json.RawMessage, error) { return nil, ErrProbeInvalid }
	if err = (&mappingWorker{s: s}).Work(ctx, mappingJob(t, s, op.ID)); err != nil {
		t.Fatal(err)
	}
	_ = (&mappingFileWorker{s: s}).Work(ctx, mappingFileJobs(t, s, op.ID)[0])
	state, err := s.Status(ctx, id+":1:1")
	if err != nil || state.Mapped || state.Adding {
		t.Fatal("failed assignment still presented as mapped", state, err)
	}
}
