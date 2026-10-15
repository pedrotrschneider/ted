package streaming

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func localFixture(t *testing.T) (*Service, Media, accounts.Identity, string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want, e := parseRange(r.Header.Get("Range"), 1000)
		if e != nil {
			w.WriteHeader(416)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/1000", want.start, want.end))
		w.Header().Set("Content-Length", fmt.Sprint(want.end-want.start+1))
		w.WriteHeader(206)
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, int(want.end-want.start+1)))
	}))
	t.Cleanup(upstream.Close)
	s, m, who := fixture(t, &resolver{url: upstream.URL})
	s.mediaHTTP = upstream.Client()
	root := t.TempDir()
	if err := s.ConfigureStorage(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseStorage)
	s.localProbe = func(context.Context, string, int64) error { return nil }
	if _, err := s.pool.Exec(context.Background(), `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES($1,$2,'ready',true,$3)`, m.ID, m.MappingID, probeJSON); err != nil {
		t.Fatal(err)
	}
	return s, m, who, root
}
func awaitLocal(t *testing.T, s *Service, phase string) LocalState {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.LocalStatus(context.Background(), "tt1345836")
		if e != nil {
			t.Fatal(e)
		}
		if v.Phase == phase {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := s.LocalStatus(context.Background(), "tt1345836")
	t.Fatalf("local state did not reach %s: %+v", phase, v)
	return LocalState{}
}
func localRange(t *testing.T, s *Service, m Media) {
	t.Helper()
	r := httptest.NewRequest("GET", "/playback/"+m.ID, nil)
	r.Header.Set("Range", "bytes=100-131")
	w := httptest.NewRecorder()
	s.playMedia(w, r, m)
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), bytes.Repeat([]byte{'x'}, 32)) {
		t.Fatalf("local/remote range: %d %q", w.Code, w.Body.String())
	}
}
func TestLocalLifecyclePreservesCatalogAndDefersRemovalForTransfer(t *testing.T) {
	s, m, who, root := localFixture(t)
	ctx := context.Background()
	localRange(t, s, m)
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='local_download'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate download", count, err)
	}
	start(t, s)
	v := awaitLocal(t, s, "ready")
	var measured float64
	if err := s.pool.QueryRow(ctx, `SELECT transfer_bytes_per_second FROM local_files WHERE movie_id=$1`, m.ID).Scan(&measured); err != nil || measured <= 0 {
		t.Fatalf("worker did not persist a measured transfer rate: %v, %v", measured, err)
	}
	if v.SpeedMbps != nil || v.RemainingSeconds != nil {
		t.Fatal("ready file advertises transfer estimates")
	}
	if v.Completed != 1000 || v.Validated != 1000 {
		t.Fatal(v)
	}
	// Local playback must not contact RD, even with an unavailable upstream.
	original := s.mediaHTTP
	s.mediaHTTP = &http.Client{Transport: failingTransport{}}
	localRange(t, s, m)
	s.mediaHTTP = original
	// A real PostgreSQL shared transfer lock blocks the deletion worker.
	_, release, err := s.fileLock(ctx, m.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveLocal(ctx, who, m.IMDB); err != nil {
		release()
		t.Fatal(err)
	}
	awaitLocal(t, s, "removing")
	var removalID int64
	if err = s.pool.QueryRow(ctx, `SELECT job_id FROM local_files WHERE movie_id=$1`, m.ID).Scan(&removalID); err != nil {
		release()
		t.Fatal(err)
	}
	var snooze *river.JobSnoozeError
	if err = s.removeFiles(ctx, &river.Job[downloadArgs]{JobRow: &rivertype.JobRow{ID: removalID}, Args: downloadArgs{MovieID: m.ID, Remove: true}}); !errors.As(err, &snooze) {
		release()
		t.Fatal("removal did not defer under a held transfer lock", err)
	}
	var generation int64
	if err = s.pool.QueryRow(ctx, `SELECT generation FROM local_files WHERE movie_id=$1`, m.ID).Scan(&generation); err != nil {
		release()
		t.Fatal(err)
	}
	_, name := localNames(m.ID, generation)
	if _, err = os.Stat(root + "/" + name); err != nil {
		release()
		t.Fatal("removed during transfer", err)
	}
	// Unrelated files within the owned movie directory survive cleanup.
	if err = os.WriteFile(root+"/"+m.ID+"/personal.txt", []byte("keep"), 0600); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	awaitLocal(t, s, "absent")
	localRange(t, s, m)
	if _, err = os.Stat(root + "/" + name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned file retained", err)
	}
	if b, err := os.ReadFile(root + "/" + m.ID + "/personal.txt"); err != nil || string(b) != "keep" {
		t.Fatal("unrelated file changed")
	}
	published, err := s.media(ctx, m.ID, true)
	if err != nil || published.MappingID != m.MappingID || string(published.Probe) != string([]byte(probeJSON)) {
		// PostgreSQL jsonb formatting differs; identity and parsed catalog remain authoritative.
		if err != nil || published.MappingID != m.MappingID || validateProbe(published.Probe, 1000) != nil {
			t.Fatal("catalog changed", err)
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func TestLocalCancellationDuringValidationCannotPublish(t *testing.T) {
	s, m, who, _ := localFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	s.localProbe = func(ctx context.Context, _ string, _ int64) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	if err := s.Download(context.Background(), who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	start(t, s)
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("probe not reached")
	}
	if err := s.CancelDownload(context.Background(), who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	awaitLocal(t, s, "cancelled")
	localRange(t, s, m)
	var available bool
	if err := s.pool.QueryRow(context.Background(), `SELECT available FROM local_files WHERE movie_id=$1`, m.ID).Scan(&available); err != nil || available {
		t.Fatal("cancelled file published", err)
	}
}

func TestLocalRecoversRenameBeforeDatabaseCommitAndRejectsCorruption(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			s, m, who, root := localFixture(t)
			ctx := context.Background()
			if err := s.Download(ctx, who, m.IMDB); err != nil {
				t.Fatal(err)
			}
			var generation int64
			if err := s.pool.QueryRow(ctx, `SELECT generation FROM local_files WHERE movie_id=$1`, m.ID).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			_, name := localNames(m.ID, generation)
			if err := os.Mkdir(root+"/"+m.ID, 0750); err != nil {
				t.Fatal(err)
			}
			data := bytes.Repeat([]byte{'x'}, 1000)
			digest := sha256.Sum256(data)
			if corrupt {
				data[0] = 'y'
			}
			if err := os.WriteFile(root+"/"+name, data, 0640); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, `INSERT INTO local_chunks VALUES($1,0,1000,$2)`, m.ID, digest[:]); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, `UPDATE local_files SET completed_bytes=1000 WHERE movie_id=$1`, m.ID); err != nil {
				t.Fatal(err)
			}
			// New service over the same durable database/files simulates a fresh process.
			replacement, err := New(ctx, s.pool, s.rd, s.logger, 2)
			if err != nil {
				t.Fatal(err)
			}
			if err = replacement.ConfigureStorage(root); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(replacement.CloseStorage)
			replacement.localProbe = s.localProbe
			replacement.mediaHTTP = &http.Client{Transport: failingTransport{}}
			if corrupt {
				if _, err = s.pool.Exec(ctx, `UPDATE river_job SET max_attempts=1 WHERE id=$1`, generation); err != nil {
					t.Fatal(err)
				}
			}
			start(t, replacement)
			if corrupt {
				v := awaitLocal(t, replacement, "failed")
				if v.Error != "validation_failed" {
					t.Fatal(v)
				}
				localRange(t, s, m)
			} else {
				awaitLocal(t, replacement, "ready")
				localRange(t, replacement, m)
			}
		})
	}
}

func TestLocalAuthorizationDiskReservationAndMissingDirectoryCleanup(t *testing.T) {
	s, m, admin, _ := localFixture(t)
	ctx := context.Background()
	var user accounts.Identity
	if err := s.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('viewer','Viewer','unused','user') RETURNING id,session_version`).Scan(&user.ID, &user.Version); err != nil {
		t.Fatal(err)
	}
	s.freeBytes = func() (int64, error) { return diskReserve + 999, nil }
	if err := s.Download(ctx, user, m.IMDB); !errors.Is(err, ErrSpace) {
		t.Fatal("disk reservation", err)
	}
	s.freeBytes = func() (int64, error) { return diskReserve + 1000, nil }
	if err := s.Download(ctx, accounts.Identity{ID: user.ID, Version: user.Version + 1}, m.IMDB); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal("stale user enqueued", err)
	}
	if err := s.Download(ctx, user, m.IMDB); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelDownload(ctx, user, m.IMDB); !errors.Is(err, accounts.ErrForbidden) {
		t.Fatal("user cancelled shared job", err)
	}
	if err := s.RemoveLocal(ctx, user, m.IMDB); !errors.Is(err, accounts.ErrForbidden) {
		t.Fatal("user removed shared file", err)
	}
	if err := s.CancelDownload(ctx, admin, m.IMDB); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLocal(ctx, admin, m.IMDB); err != nil {
		t.Fatal(err)
	}
	start(t, s)
	awaitLocal(t, s, "absent")
}

func TestDownloadBatchOrdersRangesAndCancelsPeersOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var entered atomic.Int32
			var active, peak atomic.Int32
			gate := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				now := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); now > old; old = peak.Load() {
					if peak.CompareAndSwap(old, now) {
						break
					}
				}
				span, err := parseRange(r.Header.Get("Range"), 8*chunkSize)
				if err != nil {
					t.Error(err)
					return
				}
				if entered.Add(1) == 8 {
					close(gate)
				}
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				if fail {
					if span.start == 0 {
						w.WriteHeader(500)
					} else {
						<-r.Context().Done()
					}
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", span.start, span.end, 8*chunkSize))
				w.Header().Set("Content-Length", fmt.Sprint(span.end-span.start+1))
				w.WriteHeader(206)
				_, _ = w.Write(bytes.Repeat([]byte{byte(span.start / chunkSize)}, int(span.end-span.start+1)))
			}))
			defer upstream.Close()
			s, m, _ := fixture(t, &resolver{url: upstream.URL})
			var rangeLog bytes.Buffer
			s.logger = slog.New(slog.NewTextHandler(&rangeLog, nil))
			s.mediaHTTP = upstream.Client()
			m.Bytes = 8 * chunkSize
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			data, err := s.fetchBatch(ctx, m, 0, m.Bytes)
			// A truncated loopback response can trigger a legitimate sequential retry.
			// The contract is eight concurrent ranges, with at most three attempts each.
			if peak.Load() != 8 || entered.Load() < 8 || entered.Load() > 24 {
				t.Fatalf("batch bounds: peak=%d requests=%d; recovery diagnostics: %s", peak.Load(), entered.Load(), rangeLog.String())
			}
			if fail {
				if err == nil || data != nil {
					t.Fatal("partial batch accepted")
				}
				if ctx.Err() != nil {
					t.Fatal("failure did not cancel peers")
				}
				return
			}
			if err != nil || len(data) != 8 {
				t.Fatal("batch failed", err)
			}
			for i, part := range data {
				if len(part) != int(chunkSize) || part[0] != byte(i) || part[len(part)-1] != byte(i) {
					t.Fatal("range order changed")
				}
			}
		})
	}
}

func TestDownloadResumesOnlyDurablePrefix(t *testing.T) {
	s, m, who, root := localFixture(t)
	ctx := context.Background()
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := s.pool.QueryRow(ctx, `SELECT generation FROM local_files WHERE movie_id=$1`, m.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	stage, final := localNames(m.ID, generation)
	if err := os.Mkdir(root+"/"+m.ID, 0750); err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Repeat([]byte{'x'}, 400)
	digest := sha256.Sum256(prefix)
	// Emulate a crash after a partial write but before its checkpoint committed.
	if err := os.WriteFile(root+"/"+stage, append(prefix, bytes.Repeat([]byte{'z'}, 70)...), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO local_chunks VALUES($1,0,400,$2)`, m.ID, digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE local_files SET completed_bytes=400 WHERE movie_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	start(t, s)
	awaitLocal(t, s, "ready")
	data, err := os.ReadFile(root + "/" + final)
	if err != nil || !bytes.Equal(data, bytes.Repeat([]byte{'x'}, 1000)) {
		t.Fatal("uncommitted suffix was published", err)
	}
}

type transferTransport func(*http.Request) (*http.Response, error)

func (f transferTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadChunkRetriesInterruptedTransportButRemainsBounded(t *testing.T) {
	for _, mode := range []string{"reset", "short", "permanent", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, m, _ := fixture(t, &resolver{url: "https://test.download.real-debrid.com/media"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s.mediaHTTP = &http.Client{Transport: transferTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Range") != "bytes=0-999" {
					t.Error("retry changed offset")
				}
				if mode == "cancel" {
					cancel()
					return nil, syscall.ECONNRESET
				}
				if mode == "permanent" || (mode == "reset" && calls == 1) {
					return nil, syscall.ECONNRESET
				}
				body := strings.Repeat("x", 1000)
				if mode == "short" && calls == 1 {
					body = "short"
				}
				return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": []string{"bytes 0-999/1000"}}, ContentLength: 1000, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			data, err := s.fetchChunk(ctx, m, 0, 1000)
			if mode == "permanent" {
				if err == nil || calls != 3 || data != nil {
					t.Fatal("retry bound", calls, err)
				}
				return
			}
			if mode == "cancel" {
				if err == nil || calls != 1 {
					t.Fatal("cancel retried", calls, err)
				}
				return
			}
			if err != nil || calls != 2 || !bytes.Equal(data, bytes.Repeat([]byte{'x'}, 1000)) {
				t.Fatal("range recovery", calls, err)
			}
		})
	}
}

func TestLocalTransferEstimatesUseDecimalMegabitsAndHideStaleSamples(t *testing.T) {
	s, m, who, _ := localFixture(t)
	ctx := context.Background()
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE local_files SET phase='downloading',total_bytes=125000001,transfer_bytes_per_second=12500000,transfer_sampled_at=now() WHERE movie_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	v, err := s.LocalStatus(ctx, m.IMDB)
	if err != nil || v.SpeedMbps == nil || *v.SpeedMbps != 100 || v.RemainingSeconds == nil || *v.RemainingSeconds != 11 {
		t.Fatalf("Mbps/rounded ETA: %+v, %v", v, err)
	}
	for _, phase := range []string{"queued", "cancelled", "failed", "validating", "publishing", "ready", "removing", "absent"} {
		if _, err := s.pool.Exec(ctx, `UPDATE local_files SET phase=$2 WHERE movie_id=$1`, m.ID, phase); err != nil {
			t.Fatal(err)
		}
		v, err = s.LocalStatus(ctx, m.IMDB)
		if err != nil || v.SpeedMbps != nil || v.RemainingSeconds != nil {
			t.Fatalf("inactive phase %s exposes estimates: %+v, %v", phase, v, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE local_files SET phase='downloading',transfer_sampled_at=now()-interval '61 seconds' WHERE movie_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.LocalStatus(ctx, m.IMDB)
	if err != nil || v.SpeedMbps != nil || v.RemainingSeconds != nil {
		t.Fatalf("stale estimate: %+v, %v", v, err)
	}
}

func TestLocalResumeDiscardsPreviousTransferEstimate(t *testing.T) {
	s, m, who, _ := localFixture(t)
	ctx := context.Background()
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE local_files SET phase='cancelled',transfer_bytes_per_second=12500000,transfer_sampled_at=now() WHERE movie_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	var rate float64
	var sampled *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT transfer_bytes_per_second,transfer_sampled_at FROM local_files WHERE movie_id=$1`, m.ID).Scan(&rate, &sampled); err != nil || rate != 0 || sampled != nil {
		t.Fatalf("resume retained old estimate: %v %v %v", rate, sampled, err)
	}
}

func TestRemoveActiveLocalDownloadFencesWorker(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			s, m, who, root := localFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			s.localProbe = func(ctx context.Context, _ string, _ int64) error {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			}
			ctx := context.Background()
			if err := s.Download(ctx, who, m.IMDB); err != nil {
				t.Fatal(err)
			}
			if running {
				start(t, s)
				select {
				case <-entered:
				case <-time.After(8 * time.Second):
					t.Fatal("validation not reached")
				}
			}
			if err := s.RemoveLocal(ctx, who, m.IMDB); err != nil {
				t.Fatal(err)
			}
			if err := s.RemoveLocal(ctx, who, m.IMDB); err != nil {
				t.Fatal("repeat removal", err)
			}
			if !running {
				start(t, s)
			}
			awaitLocal(t, s, "absent")
			var generation int64
			if err := s.pool.QueryRow(ctx, "SELECT generation FROM local_files WHERE movie_id=$1", m.ID).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			partial, complete := localNames(m.ID, generation)
			for _, name := range []string{partial, complete} {
				if _, err := os.Stat(root + "/" + name); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("owned file remains", name, err)
				}
			}
			localRange(t, s, m)
		})
	}
}

func TestRemoveCompletedLocalAfterJobRetention(t *testing.T) {
	s, m, who, _ := localFixture(t)
	ctx := context.Background()
	if err := s.Download(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	start(t, s)
	awaitLocal(t, s, "ready")
	if _, err := s.pool.Exec(ctx, "DELETE FROM river_job WHERE id=(SELECT job_id FROM local_files WHERE movie_id=$1)", m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLocal(ctx, who, m.IMDB); err != nil {
		t.Fatal(err)
	}
	awaitLocal(t, s, "absent")
}

func TestDownloadBatchRollbackAndExplicitSubset(t *testing.T) {
	s, m, who, _ := localFixture(t)
	ctx := context.Background()
	missing := "tt9999999"
	if _, err := s.DownloadBatch(ctx, who, []string{m.IMDB, missing}, false); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM river_job").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial batch", count, err)
	}
	result, err := s.DownloadBatch(ctx, who, []string{m.IMDB, missing, m.IMDB}, true)
	if err != nil || result != (BatchResult{Total: 2, Queued: 1, Missing: 1}) {
		t.Fatal(result, err)
	}
	result, err = s.DownloadBatch(ctx, who, []string{m.IMDB}, false)
	if err != nil || result != (BatchResult{Total: 1, Existing: 1}) {
		t.Fatal("duplicate", result, err)
	}
}

func TestDownloadBatchSpaceFailureRollsBackEarlierJobs(t *testing.T) {
	s, m, who, _ := localFixture(t)
	ctx := context.Background()
	// A second source sees the first staged reservation in the same transaction.
	var other, mapping string
	if err := s.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata) VALUES('tt9999998','{}') RETURNING id::text`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) SELECT $1,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by FROM media_mappings WHERE id=$2 RETURNING id::text`, other, m.MappingID).Scan(&mapping); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES($1,$2,'ready',true,$3)`, other, mapping, probeJSON); err != nil {
		t.Fatal(err)
	}
	s.freeBytes = func() (int64, error) { return diskReserve + 1500, nil }
	if _, err := s.DownloadBatch(ctx, who, []string{m.IMDB, "tt9999998"}, false); !errors.Is(err, ErrSpace) {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM local_files").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial reservations", count, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM river_job").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial queue", count, err)
	}
}
