package addons

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/settings"
)

// Explicitly opted-in, non-persistent CDN experiments. Alternate hosts come
// from RD's official speed-test page. Never logs private URL paths or headers.
func TestLiveCDNComparison(t *testing.T) {
	id := os.Getenv("TED_LIVE_CDN_SESSION")
	if id == "" {
		t.Skip("no CDN experiment requested")
	}
	if !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	original, err := httpURL(source.URL)
	if err != nil {
		t.Fatal("invalid source")
	}
	// Read current available settings, but never update the user's RD account.
	config, err := settings.New(pool).Load(ctx)
	if err == nil {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.real-debrid.com/rest/1.0/settings", nil)
		req.Header.Set("Authorization", "Bearer "+config.RDKey)
		client := &http.Client{Timeout: 10 * time.Second}
		response, e := client.Do(req)
		if e == nil {
			var fields map[string]json.RawMessage
			_ = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&fields)
			response.Body.Close()
			keys := []string{}
			for key := range fields {
				keys = append(keys, key)
			}
			t.Logf("RD settings status=%d available_keys=%v", response.StatusCode, keys)
		} else {
			t.Log("RD settings unavailable")
		}
	}
	candidates := []string{original.Host, "45.download.real-debrid.com", "nyk1.download.real-debrid.com", "mia1.download.real-debrid.com", "sao1.download.real-debrid.com"}
	decodedHosts := 0
	strictFrames := os.Getenv("TED_LIVE_CDN_FRAME_CHECK") == "1"
	expectedFrame := ""
	decodeLimit := 2
	if strictFrames {
		candidates = candidates[:3]
		decodeLimit = 3
	}
	for _, host := range candidates {
		if target := os.Getenv("TED_LIVE_CDN_TARGET"); target != "" && host != target {
			continue
		}
		candidate := source
		u := *original
		u.Host = host
		candidate.URL = u.String()
		s := &Service{media: mediaClient()}
		redirect := s.media.CheckRedirect
		s.media.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			t.Logf("CDN redirect destination=%s", req.URL.Host)
			return redirect(req, via)
		}
		if os.Getenv("TED_LIVE_CDN_TRACE") == "1" {
			s.media.Transport = preparationProbeTransport{base: s.media.Transport, t: t}
		}

		bounded, stop := context.WithTimeout(ctx, 30*time.Second)
		started := time.Now()
		p, e := s.prepare(bounded, candidate)
		stop()
		t.Logf("PREPARE host=%s elapsed=%s result=%s identity_matches=%t", host, time.Since(started), probeResult(e), e == nil && p.Fingerprint == fingerprint)
		s.media.CloseIdleConnections()
		if e != nil || p.Fingerprint != fingerprint {
			continue
		}
		if os.Getenv("TED_LIVE_CDN_DECODE") != "1" || decodedHosts >= decodeLimit {
			continue
		}
		decodedHosts++
		// Only decode when both ends and size match the pinned selected file.
		// The local relay avoids putting private CDN paths in process arguments.
		rounds := 2
		if os.Getenv("TED_LIVE_CDN_ONE_PAIR") == "1" || strictFrames {
			rounds = 1
		}
		for round := 0; round < rounds; round++ {
			modes := []bool{true, false}
			if round == 1 {
				modes = []bool{false, true}
			}
			if strictFrames {
				modes = []bool{false}
			}
			for _, validate := range modes {
				bounded, stop := context.WithTimeout(ctx, 60*time.Second)
				started := time.Now()
				prepareTime := time.Duration(0)
				if validate {
					again, e := s.prepare(bounded, candidate)
					prepareTime = time.Since(started)
					if e != nil || again.Fingerprint != fingerprint {
						t.Logf("TRIAL host=%s round=%d validation=true prepare=%s result=%s", host, round, prepareTime, probeResult(e))
						stop()
						continue
					}
				}
				relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					a, b, e := parseRange(r.Header.Get("Range"), p.Size)
					if e != nil {
						w.WriteHeader(416)
						return
					}
					req, e := sourceRequest(r.Context(), candidate, r.Header.Get("Range"))
					if e != nil {
						w.WriteHeader(502)
						return
					}
					response, e := s.media.Do(req)
					if e != nil {
						w.WriteHeader(502)
						return
					}
					defer response.Body.Close()
					x, y, n, e := contentRange(response.Header.Get("Content-Range"))
					if response.StatusCode != 206 || e != nil || x != a || y != b || n != p.Size || !fileContentType(response.Header.Get("Content-Type")) || !identityEncoding(response.Header.Get("Content-Encoding")) || p.ETag != "" && response.Header.Get("ETag") != p.ETag {
						w.WriteHeader(502)
						return
					}
					for _, key := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
						w.Header().Set(key, response.Header.Get(key))
					}
					w.WriteHeader(206)
					_, _ = io.Copy(w, response.Body)
				}))
				cmd := exec.CommandContext(bounded, "ffmpeg", "-v", "error", "-nostdin", "-threads", "2", "-ss", "480", "-i", relay.URL, "-frames:v", "1", "-an", "-sn", "-f", "framemd5", "-")
				output, decodeErr := cmd.Output()
				e = decodeErr
				frame := ""
				for _, line := range strings.Split(string(output), "\n") {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, "#") {
						fields := strings.Split(line, ",")
						if len(fields) == 6 {
							frame = strings.TrimSpace(fields[5])
						}
					}
				}
				if e == nil && frame == "" {
					e = ErrIncomplete
				}
				sameFrame := false
				if e == nil {
					if expectedFrame == "" {
						expectedFrame = frame
					}
					sameFrame = expectedFrame == frame
					if !sameFrame {
						e = ErrSelection
					}
				}
				t.Logf("FRAME host=%s decoded=%t identical=%t", host, frame != "", sameFrame)
				total := time.Since(started)
				relay.Close()
				stop()
				s.media.CloseIdleConnections()
				t.Logf("TRIAL host=%s round=%d validation=%t prepare=%s http_open_seek_frame_wall=%s total=%s result=%s", host, round, validate, prepareTime, total-prepareTime, total, probeResult(e))
			}
		}
	}
}

func TestLiveCDNPublic(t *testing.T) {
	if os.Getenv("TED_LIVE_CDN_PUBLIC") != "1" {
		t.Skip("no public CDN experiment requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	endpoints := []string{"https://sao1-4.download.real-debrid.com/speedtest/test.rar", "https://sao1.download.real-debrid.com/speedtest/test.rar", "https://mia1.download.real-debrid.com/speedtest/test.rar", "https://nyk1.download.real-debrid.com/speedtest/test.rar", "https://45.download.real-debrid.com/speedtest/testDefault.rar", "https://45.download.real-debrid.cloud/speedtest/testCloudflare.rar"}
	for round := 0; round < 3; round++ {
		for _, endpoint := range endpoints {
			client := mediaClient()
			bounded, stop := context.WithTimeout(ctx, 15*time.Second)
			req, _ := http.NewRequestWithContext(bounded, "GET", endpoint, nil)
			req.Header.Set("Range", "bytes=0-1048575")
			started := time.Now()
			response, e := client.Do(req)
			first := time.Since(started)
			var n int64
			status := 0
			if response != nil {
				status = response.StatusCode
				if status == 200 || status == 206 {
					n, e = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
				}
				response.Body.Close()
			}
			t.Logf("PUBLIC round=%d host=%s status=%d first=%s total=%s bytes=%d result=%s", round, req.URL.Host, status, first, time.Since(started), n, probeResult(e))
			stop()
			client.CloseIdleConnections()
		}
	}
}
