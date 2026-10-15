package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"golang.org/x/sys/unix"
)

var ErrStorage = errors.New("local storage is unavailable; check TED_MEDIA_DIR and folder permissions")
var ErrLocalBusy = errors.New("local file is busy; stop playback or wait for the current operation")
var ErrSpace = errors.New("insufficient free space for this download and queued downloads")

const storageLock int64 = 84051005
const diskReserve int64 = 256 << 20
const chunkSize int64 = 8 << 20

type LocalState struct {
	SpeedMbps        *float64 `json:"speed_mbps"`
	RemainingSeconds *int64   `json:"remaining_seconds"`
	Phase            string   `json:"phase"`
	Completed        int64    `json:"completed_bytes"`
	Validated        int64    `json:"validated_bytes"`
	Total            int64    `json:"total_bytes"`
	Error            string   `json:"error"`
	Configured       bool     `json:"configured"`
	Eligible         bool     `json:"eligible"`
	Mapped           bool     `json:"mapped"`
	Generation       int64    `json:"-"`
	JobID            int64    `json:"-"`
}

// ConfigureStorage is called before workers/HTTP start. All paths beneath this
// root are generated from database identity, never provider filenames.
func (s *Service) ConfigureStorage(dir string) error {
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return ErrStorage
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ErrStorage
	}
	// Long media transfers must not exhaust the short-operation database pool.
	lockConfig := s.pool.Config()
	lockConfig.MaxConns = 16
	s.locks, err = pgxpool.NewWithConfig(context.Background(), lockConfig)
	if err != nil {
		root.Close()
		return ErrStorage
	}
	s.storage = root
	s.freeBytes = func() (int64, error) {
		f, err := root.Open(".")
		if err != nil {
			return 0, err
		}
		defer f.Close()
		var st unix.Statfs_t
		if err = unix.Fstatfs(int(f.Fd()), &st); err != nil {
			return 0, err
		}
		return int64(st.Bavail) * int64(st.Bsize), nil
	}
	s.localProbe = func(ctx context.Context, name string, size int64) error {
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = probeHandler(ctx, size, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "media", time.Time{}, io.NewSectionReader(f, 0, size))
		}))
		return err
	}
	return nil
}
func (s *Service) CloseStorage() {
	if s.storage != nil {
		_ = s.storage.Close()
	}
	if s.locks != nil {
		s.locks.Close()
	}
}
func (s *Service) LocalStatus(ctx context.Context, imdb string) (LocalState, error) {
	v := LocalState{Phase: "absent", Configured: s.storage != nil}
	status, err := s.Status(ctx, imdb)
	if err != nil {
		return v, err
	}
	v.Eligible, v.Mapped = status.Published, status.Mapped
	var rate float64
	var fresh bool
	err = s.pool.QueryRow(ctx, `SELECT l.phase,l.completed_bytes,l.validated_bytes,l.total_bytes,l.error_code,l.transfer_bytes_per_second,COALESCE(l.transfer_sampled_at BETWEEN now()-interval '1 minute' AND now(),false) FROM local_files l JOIN catalog_media c ON c.id=l.movie_id WHERE c.cinemeta_id=$1`, imdb).Scan(&v.Phase, &v.Completed, &v.Validated, &v.Total, &v.Error, &rate, &fresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err == nil && v.Phase == "downloading" && v.Completed < v.Total && rate > 0 && fresh {
		mbps := rate * 8 / 1_000_000
		remaining := int64(math.Ceil(float64(v.Total-v.Completed) / rate))
		v.SpeedMbps, v.RemainingSeconds = &mbps, &remaining
	}
	return v, err
}
func localState(ctx context.Context, tx pgx.Tx, id string) (LocalState, error) {
	var v LocalState
	err := tx.QueryRow(ctx, `SELECT phase,completed_bytes,validated_bytes,total_bytes,error_code,generation,job_id FROM local_files WHERE movie_id=$1 FOR UPDATE`, id).Scan(&v.Phase, &v.Completed, &v.Validated, &v.Total, &v.Error, &v.Generation, &v.JobID)
	return v, err
}
func localNames(id string, generation int64) (string, string) {
	base := fmt.Sprintf("%s/%d", id, generation)
	return base + ".partial", base + ".media"
}

func (s *Service) Download(ctx context.Context, who accounts.Identity, imdb string) error {
	if s.storage == nil {
		return ErrStorage
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, false); err != nil {
		return err
	}
	// Serialize reservations across processes. Running transfers only reduce their
	// reservation after durable writes; external disk users remain checked per chunk.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, storageLock); err != nil {
		return err
	}
	if _, err = s.downloadTx(ctx, tx, imdb); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// downloadTx shares reservation, resumability and job ownership with batch downloads.
// The caller holds storageLock until all reservations and jobs commit.
func (s *Service) downloadTx(ctx context.Context, tx pgx.Tx, imdb string) (bool, error) {
	var id string
	var size int64
	err := tx.QueryRow(ctx, `SELECT c.id::text,m.file_bytes FROM catalog_media c JOIN streaming_items s ON s.movie_id=c.id JOIN media_mappings m ON m.id=s.mapping_id WHERE c.cinemeta_id=$1 AND s.published AND NOT EXISTS(SELECT 1 FROM library_removal_items i JOIN library_removals r ON r.id=i.removal_id WHERE i.item_id=c.id AND NOT r.retired) FOR UPDATE OF c`, imdb).Scan(&id, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotReady
	}
	if err != nil {
		return false, err
	}
	v, err := localState(ctx, tx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil && v.Phase != "absent" && v.Phase != "failed" && v.Phase != "cancelled" {
		return false, nil
	}
	var reserved int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(total_bytes-completed_bytes),0)::bigint FROM local_files WHERE phase IN ('queued','downloading','validating','publishing') AND movie_id!=$1`, id).Scan(&reserved); err != nil {
		return false, err
	}
	free, err := s.freeBytes()
	if err != nil {
		return false, ErrStorage
	}
	if free-diskReserve-reserved < size-v.Completed {
		return false, ErrSpace
	}
	job, err := s.queue.InsertTx(ctx, tx, downloadArgs{MovieID: id}, &river.InsertOpts{MaxAttempts: 5})
	if err != nil {
		return false, err
	}
	generation := v.Generation
	if generation == 0 || v.Phase == "absent" {
		generation = job.Job.ID
	}
	_, err = tx.Exec(ctx, `INSERT INTO local_files(movie_id,generation,job_id,phase,total_bytes) VALUES($1,$2,$3,'queued',$4) ON CONFLICT(movie_id) DO UPDATE SET generation=$2,job_id=$3,phase='queued',error_code='',validated_bytes=0,total_bytes=$4,transfer_bytes_per_second=0,transfer_sampled_at=NULL,updated_at=now()`, id, generation, job.Job.ID, size)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) CancelDownload(ctx context.Context, who accounts.Identity, imdb string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1`, imdb).Scan(&id); err != nil {
		return err
	}
	v, err := localState(ctx, tx, id)
	if err != nil {
		return err
	}
	if v.Phase == "ready" || v.Phase == "removing" || v.Phase == "absent" {
		return ErrLocalBusy
	}
	if _, err = tx.Exec(ctx, `UPDATE local_files SET phase='cancelled',error_code='',updated_at=now() WHERE movie_id=$1`, id); err != nil {
		return err
	}
	if _, err = s.queue.JobCancelTx(ctx, tx, v.JobID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Each transfer holds a cross-process shared lock. Removal never waits while
// holding a row lock; active transfers finish with their selected source intact.
func (s *Service) fileLock(ctx context.Context, id string, shared bool) (*pgxpool.Conn, func(), error) {
	conn, err := s.locks.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	fn := "pg_try_advisory_lock"
	unlock := "pg_advisory_unlock"
	if shared {
		fn = "pg_advisory_lock_shared"
		unlock += "_shared"
	}
	var ok bool
	if shared {
		wait, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, err = conn.Exec(wait, "SELECT "+fn+"(hashtextextended($1,84051006))", id)
		cancel()
		ok = err == nil
	} else {
		err = conn.QueryRow(ctx, "SELECT "+fn+"(hashtextextended($1,84051006))", id).Scan(&ok)
	}
	if err != nil || !ok {
		if err != nil {
			// A cancelled query may have acquired a session lock just before the
			// connection outcome became uncertain. Never return it to the pool.
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Conn().Close(closeCtx)
			cancel()
		}
		conn.Release()
		if err == nil {
			err = ErrLocalBusy
		}
		return nil, nil, err
	}
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, "SELECT "+unlock+"(hashtextextended($1,84051006))", id); err != nil {
			_ = conn.Conn().Close(ctx)
		}
		conn.Release()
	}
	return conn, release, nil
}

func (s *Service) RemoveLocal(ctx context.Context, who accounts.Identity, imdb string) error {
	if s.storage == nil {
		return ErrStorage
	}
	// Removal is a durable job, including when playback delays physical deletion.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1`, imdb).Scan(&id); err != nil {
		return err
	}
	v, err := localState(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if v.Phase == "absent" || (v.Phase == "removing" && v.Error == "") {
		return tx.Commit(ctx)
	}
	// Fence the old worker and cancel its durable job in the same transaction
	// that queues removal. The file lock delays deletion until transfers exit.
	if v.Phase == "queued" || v.Phase == "downloading" || v.Phase == "validating" || v.Phase == "publishing" || v.Phase == "removing" {
		if _, err = s.queue.JobCancelTx(ctx, tx, v.JobID); err != nil {
			return err
		}
	}
	job, err := s.queue.InsertTx(ctx, tx, downloadArgs{MovieID: id, Remove: true}, &river.InsertOpts{MaxAttempts: 5})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE local_files SET phase='removing',job_id=$2,error_code='',updated_at=now() WHERE movie_id=$1`, id, job.Job.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) playMedia(w http.ResponseWriter, r *http.Request, m Media) {
	s.playPinnedMedia(w, r, m, "")
}

func (s *Service) playPinnedMedia(w http.ResponseWriter, r *http.Request, m Media, pinned string) {
	if s.storage == nil {
		s.sendMedia(w, r, m)
		return
	}
	conn, release, err := s.fileLock(r.Context(), m.ID, true)
	if err != nil {
		http.Error(w, "playback temporarily unavailable; retry", 503)
		return
	}
	defer release()
	var available bool
	var generation int64
	stateCtx, cancelState := context.WithTimeout(r.Context(), 8*time.Second)
	if pinned != "" {
		var mapping string
		err = conn.QueryRow(stateCtx, `SELECT s.mapping_id::text,COALESCE(l.available,false),COALESCE(l.generation,0) FROM streaming_items s LEFT JOIN local_files l ON l.movie_id=s.movie_id WHERE s.movie_id=$1 AND s.published`, m.ID).Scan(&mapping, &available, &generation)
		cancelState()
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "mapping is no longer ready", 409)
			return
		}
		if err == nil && mapping != pinned {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"code":"release_changed","error":"playback mapping changed; select the source again"}`))
			return
		}
	} else {
		err = conn.QueryRow(stateCtx, `SELECT available,generation FROM local_files WHERE movie_id=$1`, m.ID).Scan(&available, &generation)
		cancelState()
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "storage state unavailable", 503)
		return
	}
	if !available {
		s.sendMedia(w, r, m)
		return
	}
	_, name := localNames(m.ID, generation)
	f, err := s.storage.Open(name)
	if err != nil {
		http.Error(w, "local file unavailable; check storage or remove local copy", 503)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != m.Bytes {
		http.Error(w, "local file invalid; check storage", 503)
		return
	}
	want, err := parseRange(r.Header.Get("Range"), m.Bytes)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", m.Bytes))
		http.Error(w, "invalid range", 416)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-TED-Source", "local")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", fmt.Sprint(want.end-want.start+1))
	if want.partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", want.start, want.end, m.Bytes))
		w.WriteHeader(206)
	}
	if r.Method == "HEAD" {
		return
	}
	controller := http.NewResponseController(w)
	_, _ = copyChunk(controller, w, io.NewSectionReader(f, want.start, want.end-want.start+1), want.end-want.start+1)
}
