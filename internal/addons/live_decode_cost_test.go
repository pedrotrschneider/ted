package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
)

// TestLiveDecodeCost separates HTTP startup from decoding, without publishing
// media or viewing reports. A bounded, in-memory range fixture is populated in
// the first pass. Subsequent passes cannot contact RD, even on a cache miss.
func TestLiveDecodeCost(t *testing.T) {
	id := os.Getenv("TED_LIVE_DECODE_COST_SESSION")
	if id == "" {
		t.Skip("no decode cost experiment requested")
	}
	if !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if err != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	var raw []byte
	var fingerprint string
	if pool.QueryRow(ctx, `SELECT source,fingerprint FROM native_addon_playback WHERE session_id=$1`, id).Scan(&raw, &fingerprint) != nil {
		t.Fatal("session unavailable")
	}
	var source Source
	if json.Unmarshal(raw, &source) != nil || source.RDFile == nil {
		t.Fatal("not RD media")
	}
	s := &Service{media: mediaClient()}
	defer s.media.CloseIdleConnections()
	p, err := s.prepare(ctx, source)
	if err != nil || p.Fingerprint != fingerprint {
		t.Fatal("file identity validation failed")
	}
	var mu sync.Mutex
	spans := map[int64][]byte{}
	var cached int64
	expected := ""
	for pass := 0; pass < 3; pass++ {
		warm := pass > 0
		misses := 0
		relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, b, e := parseRange(r.Header.Get("Range"), p.Size)
			if e != nil {
				w.WriteHeader(416)
				return
			}
			mu.Lock()
			data := spans[a]
			mu.Unlock()
			if data == nil {
				if warm {
					mu.Lock()
					misses++
					mu.Unlock()
					w.WriteHeader(502)
					return
				}
				// A finite fixture, not a complete download. Fail instead of
				// silently reading the whole film if its layout needs more.
				end := min(b, a+(16<<20)-1)
				request, e := sourceRequest(ctx, source, fmt.Sprintf("bytes=%d-%d", a, end))
				if e != nil {
					w.WriteHeader(502)
					return
				}
				response, e := s.media.Do(request)
				if e != nil {
					w.WriteHeader(502)
					return
				}
				defer response.Body.Close()
				x, y, n, e := contentRange(response.Header.Get("Content-Range"))
				if response.StatusCode != 206 || e != nil || x != a || y != end || n != p.Size || !fileContentType(response.Header.Get("Content-Type")) || !identityEncoding(response.Header.Get("Content-Encoding")) || p.ETag != "" && response.Header.Get("ETag") != p.ETag {
					w.WriteHeader(502)
					return
				}
				data, e = io.ReadAll(io.LimitReader(response.Body, end-a+2))
				if e != nil || int64(len(data)) != end-a+1 {
					w.WriteHeader(502)
					return
				}
				mu.Lock()
				if cached+int64(len(data)) > 128<<20 {
					mu.Unlock()
					w.WriteHeader(502)
					return
				}
				spans[a] = data
				cached += int64(len(data))
				mu.Unlock()
			}
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Accept-Ranges", "bytes")
			end := min(b, a+int64(len(data))-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, end, p.Size))
			w.Header().Set("Content-Length", fmt.Sprint(end-a+1))
			w.WriteHeader(206)
			_, _ = w.Write(data[:end-a+1])
		}))
		started := time.Now()
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-threads", "2", "-ss", "480", "-i", relay.URL, "-frames:v", "1", "-an", "-sn", "-f", "framemd5", "-")
		out, e := cmd.Output()
		wall := time.Since(started)
		relay.Close()
		frame := ""
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "#") {
				fields := strings.Split(line, ",")
				if len(fields) == 6 {
					frame = strings.TrimSpace(fields[5])
				}
			}
		}
		if e != nil || frame == "" || misses != 0 {
			t.Fatalf("pass=%d failed, cache_misses=%d", pass, misses)
		}
		if expected == "" {
			expected = frame
		}
		if frame != expected {
			t.Fatal("decoded frames differ")
		}
		t.Logf("FRAME pass=%d network_disabled=%t wall=%s cpu_user=%s cpu_system=%s cache_bytes=%d same_frame=true", pass, warm, wall, cmd.ProcessState.UserTime(), cmd.ProcessState.SystemTime(), cached)
	}
}
