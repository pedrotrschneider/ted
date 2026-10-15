package streaming

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"entertainmentdistrict/internal/database"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type downloadArgs struct {
	MovieID string `json:"movie_id"`
	Remove  bool   `json:"remove"`
}

func (downloadArgs) Kind() string { return "local_download" }

type downloadWorker struct {
	river.WorkerDefaults[downloadArgs]
	s *Service
}

func (w *downloadWorker) Work(ctx context.Context, job *river.Job[downloadArgs]) error {
	s := w.s
	if s.storage == nil {
		return w.failure(job, "storage_unavailable")
	}
	if !uuidPattern.MatchString(job.Args.MovieID) {
		return river.JobCancel(ErrInvalid)
	}
	_, release, err := s.fileLock(ctx, job.Args.MovieID+":download", false)
	if errors.Is(err, ErrLocalBusy) {
		return river.JobSnooze(3 * time.Second)
	}
	if err != nil {
		return ErrUnavailable
	}
	defer release()
	if job.Args.Remove {
		err = s.removeFiles(ctx, job)
	} else {
		err = s.downloadFile(ctx, job)
	}
	if err == nil {
		return nil
	}
	var snooze *river.JobSnoozeError
	var cancel *river.JobCancelError
	if errors.As(err, &snooze) || errors.As(err, &cancel) {
		return err
	}
	var transfer *transferFailure
	if errors.As(err, &transfer) {
		s.logger.Warn("RD range transfer failed", "movie_id", job.Args.MovieID, "reason", transfer.reason, "status", transfer.status)
	}
	code := "transfer_failed"
	switch {
	case errors.Is(err, ErrSpace):
		code = "insufficient_space"
	case errors.Is(err, ErrStorage):
		code = "storage_unavailable"
	case errors.Is(err, errValidation):
		code = "validation_failed"
	}
	return w.failure(job, code)
}
func (w *downloadWorker) failure(job *river.Job[downloadArgs], code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	phase := "queued"
	if job.Attempt >= job.MaxAttempts {
		phase = "failed"
	}
	if job.Args.Remove {
		phase = "removing"
	}
	_, _ = w.s.pool.Exec(ctx, `UPDATE local_files SET phase=$3,error_code=$4,transfer_bytes_per_second=0,transfer_sampled_at=NULL,updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase NOT IN ('cancelled','ready','absent')`, job.Args.MovieID, job.ID, phase, code)
	w.s.logger.Warn("local media operation failed", "movie_id", job.Args.MovieID, "job_id", job.ID, "error_code", code, "attempt", job.Attempt)
	return errors.New(code)
}

var errValidation = errors.New("local file validation failed")

type transferFailure struct {
	reason string
	status int
}

func (e *transferFailure) Error() string {
	return fmt.Sprintf("RD range %s (HTTP %d)", e.reason, e.status)
}

func (s *Service) downloadFile(ctx context.Context, job *river.Job[downloadArgs]) error {
	id := job.Args.MovieID
	m, err := s.media(ctx, id, true)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	v, err := localState(ctx, tx, id)
	if err != nil {
		return err
	}
	if v.JobID != job.ID || v.Phase == "cancelled" || v.Phase == "removing" || v.Phase == "absent" {
		return river.JobCancel(ErrInvalid)
	}
	if v.Phase == "ready" {
		return tx.Commit(ctx)
	}
	if v.Total != m.Bytes {
		return errValidation
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	stage, final := localNames(id, v.Generation)
	if err = s.storage.Mkdir(id, 0750); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrStorage
	}
	if st, e := s.storage.Lstat(id); e != nil || !st.IsDir() {
		return ErrStorage
	}
	// A rename can precede its database commit. Never discard/re-download that
	// file: verify its recorded chunks and finish publication on the next attempt.
	name := stage
	if _, err = s.storage.Stat(final); err == nil {
		name = final
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrStorage
	}
	if st, e := s.storage.Lstat(name); e == nil && !st.Mode().IsRegular() {
		return ErrStorage
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return ErrStorage
	}
	f, err := s.storage.OpenFile(name, os.O_RDWR|os.O_CREATE, 0640)
	if err != nil {
		return ErrStorage
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	if st.Size() < v.Completed || v.Completed > v.Total {
		return errValidation
	}
	if name == final && st.Size() != v.Total {
		return errValidation
	}
	if name == stage && st.Size() > v.Completed {
		if err = f.Truncate(v.Completed); err != nil {
			return ErrStorage
		}
	}
	deadline := time.Now().Add(45 * time.Second)
	for v.Completed < v.Total {
		if name == final {
			return errValidation
		}
		if time.Now().After(deadline) {
			return river.JobSnooze(time.Second)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		n := min(8*chunkSize, v.Total-v.Completed)
		free, e := s.freeBytes()
		if e != nil {
			return ErrStorage
		}
		if free < diskReserve+n {
			return ErrSpace
		}
		batchStarted := time.Now()
		batch, e := s.fetchBatch(ctx, m, v.Completed, n)
		if e != nil {
			return e
		}
		for _, data := range batch {
			n := int64(len(data))
			tx, e = s.pool.Begin(ctx)
			if e != nil {
				return e
			}
			current, e := localState(ctx, tx, id)
			if e != nil {
				database.Rollback(tx)
				return e
			}
			if current.JobID != job.ID || current.Phase == "cancelled" {
				database.Rollback(tx)
				return river.JobCancel(ErrInvalid)
			}
			if _, e = f.WriteAt(data, v.Completed); e == nil {
				e = f.Sync()
			}
			if e != nil {
				database.Rollback(tx)
				return ErrStorage
			}
			digest := sha256.Sum256(data)
			if _, e = tx.Exec(ctx, `INSERT INTO local_chunks(movie_id,byte_offset,byte_length,sha256) VALUES($1,$2,$3,$4)`, id, v.Completed, n, digest[:]); e == nil {
				_, e = tx.Exec(ctx, `UPDATE local_files SET completed_bytes=$2,phase='downloading',error_code='',updated_at=now() WHERE movie_id=$1`, id, v.Completed+n)
			}
			if e != nil {
				database.Rollback(tx)
				return e
			}
			if e = tx.Commit(ctx); e != nil {
				return e
			}
			v.Completed += n
		}
		// Measure the whole parallel batch, including retries and durable writes,
		// rather than the rapid sequence of checkpoint commits. Smooth recent
		// batches; discard stale samples after interruptions.
		rate := float64(n) / time.Since(batchStarted).Seconds()
		if _, e = s.pool.Exec(ctx, `UPDATE local_files SET
          transfer_bytes_per_second=CASE WHEN transfer_sampled_at > now()-interval '1 minute'
            THEN 0.5*transfer_bytes_per_second+0.5*$3 ELSE $3 END,
          transfer_sampled_at=clock_timestamp()
          WHERE movie_id=$1 AND job_id=$2 AND phase='downloading'`, id, job.ID, rate); e != nil {
			return e
		}
	}
	// Validation is checkpointed too, keeping each River attempt bounded even for
	// large files/slow disks. Downloaded bytes are immutable under the worker lock.
	for v.Validated < v.Total {
		if time.Now().After(deadline) {
			return river.JobSnooze(time.Second)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		var n int64
		var expected []byte
		if err = s.pool.QueryRow(ctx, `SELECT byte_length,sha256 FROM local_chunks WHERE movie_id=$1 AND byte_offset=$2`, id, v.Validated).Scan(&n, &expected); err != nil {
			return errValidation
		}
		if n <= 0 || n > chunkSize || n > v.Total-v.Validated {
			return errValidation
		}
		data := make([]byte, n)
		if _, err = f.ReadAt(data, v.Validated); err != nil {
			return errValidation
		}
		got := sha256.Sum256(data)
		if !bytes.Equal(got[:], expected) {
			return errValidation
		}
		tag, e := s.pool.Exec(ctx, `UPDATE local_files SET validated_bytes=$3,phase='validating',error_code='',updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase!='cancelled'`, id, job.ID, v.Validated+n)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return river.JobCancel(ErrInvalid)
		}
		v.Validated += n
	}
	if err = s.localProbe(ctx, name, m.Bytes); err != nil {
		return errValidation
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	current, err := localState(ctx, tx, id)
	if err != nil {
		return err
	}
	if current.JobID != job.ID || current.Phase == "cancelled" {
		return river.JobCancel(ErrInvalid)
	}
	if _, err = tx.Exec(ctx, `UPDATE local_files SET phase='publishing',updated_at=now() WHERE movie_id=$1`, id); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return ErrStorage
	}
	if name != final {
		if err = s.storage.Rename(stage, final); err != nil {
			return ErrStorage
		}
	}
	if err = s.syncDir(id); err != nil {
		return ErrStorage
	}
	if err = s.syncDir("."); err != nil {
		return ErrStorage
	}
	if _, err = tx.Exec(ctx, `UPDATE local_files SET phase='ready',available=true,error_code='',updated_at=now() WHERE movie_id=$1`, id); err != nil {
		return err
	}
	if _, err = river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.logger.Info("local media published", "movie_id", id, "bytes", m.Bytes)
	return nil
}

func (s *Service) fetchChunk(ctx context.Context, m Media, offset, n int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lastReason := ""
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 && lastReason != "" {
			timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}
		link, err := s.link(ctx, m, "")
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
		if err != nil {
			return nil, ErrUnavailable
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+n-1))
		req.Header.Set("Accept-Encoding", "identity")
		response, err := s.mediaHTTP.Do(req)
		if err != nil {
			reason := "connection_failed"
			var network net.Error
			if errors.As(err, &network) && network.Timeout() {
				reason = "connection_timeout"
			}
			if errors.Is(err, syscall.ECONNRESET) {
				reason = "connection_reset"
			}
			if errors.Is(err, io.EOF) {
				reason = "connection_closed"
			}
			if ctx.Err() != nil {
				reason = "request_cancelled_or_timed_out"
			}
			lastReason = reason
			if ctx.Err() == nil && attempt < 2 {
				continue
			}
			return nil, &transferFailure{reason: reason}
		}
		if response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 410 {
			response.Body.Close()
			if _, err = s.link(ctx, m, link); err != nil {
				return nil, err
			}
			continue
		}
		expected := fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, m.Bytes)
		if response.StatusCode != 206 || response.Header.Get("Content-Range") != expected || response.ContentLength != n || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
			response.Body.Close()
			return nil, &transferFailure{reason: "unexpected_status_or_range", status: response.StatusCode}
		}
		data := make([]byte, n)
		_, err = io.ReadFull(response.Body, data)
		response.Body.Close()
		if err != nil || int64(len(data)) != n {
			lastReason = "interrupted_body"
			if ctx.Err() == nil && attempt < 2 {
				continue
			}
			return nil, &transferFailure{reason: "interrupted_body", status: response.StatusCode}
		}
		if lastReason != "" {
			s.logger.Info("RD range recovered", "movie_id", m.ID, "reason", lastReason, "retries", attempt)
		}
		return data, nil
	}
	return nil, ErrUnavailable
}

// At most 64 MiB of range data is in flight per worker. Writes/checkpoints stay
// ordered, so a failed batch never creates holes or advertises partial media.
func (s *Service) fetchBatch(ctx context.Context, m Media, offset, length int64) ([][]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	count := int((length + chunkSize - 1) / chunkSize)
	if count < 1 || count > 8 {
		return nil, ErrInvalid
	}
	data := make([][]byte, count)
	var firstError error
	var failure sync.Once
	var group sync.WaitGroup
	for i := range count {
		group.Go(func() {
			var err error
			data[i], err = s.fetchChunk(ctx, m, offset+int64(i)*chunkSize, min(chunkSize, length-int64(i)*chunkSize))
			if err != nil {
				failure.Do(func() { firstError = err; cancel() })
			}
		})
	}
	group.Wait()
	if firstError != nil {
		return nil, firstError
	}
	return data, nil
}
func (s *Service) syncDir(name string) error {
	f, err := s.storage.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *Service) removeFiles(ctx context.Context, job *river.Job[downloadArgs]) error {
	id := job.Args.MovieID
	conn, release, err := s.fileLock(ctx, id, false)
	if errors.Is(err, ErrLocalBusy) {
		return river.JobSnooze(3 * time.Second)
	}
	if err != nil {
		return err
	}
	defer release()
	if st, e := s.storage.Lstat(id); e == nil && !st.IsDir() {
		return ErrStorage
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return ErrStorage
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	v, err := localState(ctx, tx, id)
	if err != nil {
		return err
	}
	if v.JobID != job.ID || v.Phase != "removing" {
		return river.JobCancel(ErrInvalid)
	}
	// Commit the switch before physical removal. A crash leaves only owned
	// cleanup work; new requests use RD and readers already finished under lock.
	if _, err = tx.Exec(ctx, `UPDATE local_files SET available=false WHERE movie_id=$1`, id); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	stage, final := localNames(id, v.Generation)
	for _, name := range []string{stage, final} {
		if err = s.storage.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrStorage
		}
	}
	if err = s.syncDir(id); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStorage
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	current, err := localState(ctx, tx, id)
	if err != nil {
		return err
	}
	if current.JobID != job.ID || current.Phase != "removing" {
		return river.JobCancel(ErrInvalid)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM local_chunks WHERE movie_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE local_files SET phase='absent',completed_bytes=0,validated_bytes=0,error_code='',updated_at=now() WHERE movie_id=$1 AND job_id=$2`, id, job.ID); err != nil {
		return err
	}
	if _, err = river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
