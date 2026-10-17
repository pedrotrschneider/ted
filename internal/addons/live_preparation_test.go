package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/streaming"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strings"
	"testing"
	"time"
)

// Read an already prepared URL from stdin for a bounded, credential-redacted
// transport diagnosis inside the deployed container. No account writes occur.
func TestLivePreparedMediaTransport(t *testing.T) {
	if os.Getenv("TED_LIVE_MEDIA_TRANSPORT") != "1" {
		t.Skip("no private transport input")
	}
	var source Source
	if json.NewDecoder(os.Stdin).Decode(&source) != nil || !realdebrid.ValidDownloadURL(source.URL) {
		t.Fatal("invalid private source")
	}
	if os.Getenv("TED_LIVE_MEDIA_KEEPALIVE") == "1" {
		source.Headers = map[string]string{"Connection": "keep-alive"}
	}
	client := mediaClient()
	if os.Getenv("TED_LIVE_MEDIA_HTTP2") == "1" {
		client.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	}
	base := client.Transport
	client.Transport = preparationProbeTransport{base: base, t: t}
	defer client.CloseIdleConnections()
	for i, byteRange := range []string{"bytes=0-4095", "bytes=1048576-1052671", "bytes=0-"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, _ := sourceRequest(ctx, source, byteRange)
		start := time.Now()
		res, err := client.Do(req)
		if err != nil {
			t.Logf("range %d failed elapsed=%s result=%s", i, time.Since(start), probeResult(err))
			cancel()
			continue
		}
		n, err := io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		cancel()
		t.Logf("range %d status=%d protocol=%s close=%t bytes=%d elapsed=%s result=%s", i, res.StatusCode, res.Proto, res.Close, n, time.Since(start), probeResult(err))
	}
}

// Exercise the exact selected release through production preparation, a durable
// native session, the download descriptor and one small authenticated range.
// All database effects belong to an isolated fixture; no viewing is reported.
func TestLiveSelectedDownloadPreparation(t *testing.T) {
	raw := os.Getenv("TED_LIVE_SELECTED_SOURCES")
	if raw == "" {
		t.Skip("no selected live sources supplied")
	}
	var entries []struct {
		Media      string     `json:"media"`
		Source     Source     `json:"source"`
		Descriptor Descriptor `json:"descriptor"`
		ExpectedMS int64      `json:"expected_ms"`
	}
	if json.Unmarshal([]byte(raw), &entries) != nil || len(entries) == 0 || len(entries) > 3 {
		t.Fatal("invalid private probe input")
	}
	s, auth, grant, remote, _ := fixture(t)
	// A live probe may add a separately owned RD entry. Retain its ownership
	// journal outside the disposable test schema before fixture cleanup runs.
	if report := os.Getenv("TED_LIVE_RD_OWNERSHIP_REPORT"); report != "" {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var raw []byte
			err := s.pool.QueryRow(ctx, `SELECT json_build_object('all_files',(SELECT coalesce(json_agg(t),'[]') FROM rd_addon_torrents t),'individual_files',(SELECT coalesce(json_agg(t),'[]') FROM rd_addon_file_torrents t))`).Scan(&raw)
			if err != nil {
				t.Error("live ownership journal could not be read")
				return
			}
			file, err := os.OpenFile(report, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Error("live ownership journal could not be created")
				return
			}
			_, err = file.Write(raw)
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				t.Error("live ownership journal could not be saved")
			}
		})
	}
	descriptor, _ := json.Marshal(entries[0].Descriptor)
	remote.collection = []json.RawMessage{descriptor}
	s.http, s.media = PublicClient(), mediaClient()
	if os.Getenv("TED_RD_PREPARATION_TRACE") == "1" {
		s.media.Transport = preparationProbeTransport{base: s.media.Transport, t: t}
	}
	defer s.http.CloseIdleConnections()
	defer s.media.CloseIdleConnections()
	ctx, stop := context.WithTimeout(context.Background(), 120*time.Second)
	defer stop()
	if key := os.Getenv("TED_REAL_DEBRID_API_KEY"); key != "" {
		streams, e := streaming.New(ctx, s.pool, realdebrid.New(key), nil, 1)
		if e != nil {
			t.Fatal("RD verification fixture initialization failed")
		}
		s.RD = streams.RDInventory()
	}
	for _, entry := range entries {
		if entry.Source.InfoHash == "" {
			entry.Source.InfoHash = providerBingeHash(entry.Source.BingeGroup)
		}
		media := mediaIdentity("episode", entry.Media)
		page, err := s.SelectedSourcesPrioritizing(ctx, auth, grant, media, "", entry.Descriptor.Key())
		if err != nil {
			t.Fatal("live selected add-on lookup:", probeResult(err))
		}
		key, _, _ := sharedKeys(entry.Descriptor, entry.Source, media)
		matches := []string{}
		for _, choice := range page.Sources {
			if choice.SourceKey == key {
				matches = append(matches, choice.ID)
			}
		}
		if len(matches) != 1 {
			t.Fatal("exact selected live release not available")
		}
		started := time.Now()
		session, err := s.CreateSession(ctx, auth, grant, media, matches[0])
		if err != nil {
			t.Fatal("live selected media preparation:", probeResult(err))
		}
		if entry.ExpectedMS > 0 {
			if _, e := s.pool.Exec(ctx, `UPDATE native_playback_sessions SET expected_duration_ms=$2 WHERE id=$1`, session.ID, entry.ExpectedMS); e != nil {
				t.Fatal("runtime fixture update failed")
			}
		}
		download, err := s.NativeDownload(ctx, grant, session.ID)
		if err != nil || download.Bytes <= 0 || download.ETag == "" {
			t.Fatal("live download descriptor unavailable")
		}
		r := httptest.NewRequest("GET", download.MediaURL, nil).WithContext(ctx)
		r.Header.Set("Range", "bytes=0-1023")
		r.Header.Set("If-Match", download.ETag)
		w := httptest.NewRecorder()
		if err = s.Serve(w, r, grant, session.ID); err != nil || w.Code != 206 || w.Body.Len() != 1024 || w.Header().Get("ETag") != download.ETag {
			t.Fatal("live pinned download range failed:", probeResult(err), w.Code)
		}
		t.Logf("%s: selected release prepared, durable descriptor and pinned 1024-byte download range passed in %s", entry.Media, time.Since(started))
	}
	var viewed int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM watch_history)+(SELECT count(*) FROM native_watch_events)`).Scan(&viewed); err != nil || viewed != 0 {
		t.Fatal("download probe fabricated viewing")
	}
}

// Optional, read-only diagnosis of already selected owner sources. The private
// descriptors enter only through the helper's process environment; output is
// limited to episode identities, HTTP statuses, timings and fixed error classes.
func TestLiveSelectedSourcePreparation(t *testing.T) {
	raw := os.Getenv("TED_LIVE_SELECTED_SOURCES")
	if raw == "" {
		t.Skip("no selected live sources supplied")
	}
	var entries []struct {
		Media  string `json:"media"`
		Source Source `json:"source"`
	}
	if json.Unmarshal([]byte(raw), &entries) != nil || len(entries) > 3 {
		t.Fatal("invalid private probe input")
	}
	client := PublicClient()
	defer client.CloseIdleConnections()
	client.Transport = preparationProbeTransport{base: client.Transport, t: t}
	s := &Service{http: client}
	for _, entry := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		started := time.Now()
		_, err := s.prepare(ctx, entry.Source)
		cancel()
		t.Logf("%s production preparation: elapsed=%s result=%s", entry.Media, time.Since(started), probeResult(err))
		if err != nil {
			// A diagnostic comparison follows only an observed production failure.
			// It does not change application transport settings or retry policy.
			comparison := publicClient(12*time.Second, 15*time.Second)
			defer comparison.CloseIdleConnections()
			comparison.Transport = preparationProbeTransport{base: comparison.Transport, t: t}
			longer := &Service{http: comparison}
			bounded, stop := context.WithTimeout(context.Background(), 35*time.Second)
			started = time.Now()
			_, err = longer.prepare(bounded, entry.Source)
			stop()
			t.Logf("%s comparison preparation: elapsed=%s result=%s", entry.Media, time.Since(started), probeResult(err))
		}
	}
}

func probeResult(err error) string {
	if errors.Is(err, realdebrid.ErrUnavailable) {
		return "rd_unavailable"
	}
	if errors.Is(err, ErrPreparation) {
		return "media_preparation_unavailable"
	}
	if errors.Is(err, realdebrid.ErrNotConfigured) {
		return "rd_not_configured"
	}
	if errors.Is(err, realdebrid.ErrCredentials) {
		return "rd_credentials"
	}
	if errors.Is(err, realdebrid.ErrMismatch) {
		return "rd_identity_mismatch"
	}
	if errors.Is(err, realdebrid.ErrPending) {
		return "rd_pending"
	}
	if errors.Is(err, realdebrid.ErrRejected) {
		return "rd_rejected"
	}
	if errors.Is(err, realdebrid.ErrInfringing) {
		return "rd_infringing"
	}
	if errors.Is(err, realdebrid.ErrIncomplete) {
		return "rd_incomplete"
	}
	if errors.Is(err, realdebrid.ErrLimited) {
		return "rd_limited"
	}
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrSelection):
		return "selection"
	case strings.Contains(err.Error(), "timeout awaiting response headers"):
		return "headers_timeout"
	case strings.Contains(err.Error(), "TLS handshake timeout"):
		return "tls_timeout"
	default:
		return "unavailable"
	}
}

type preparationProbeTransport struct {
	base http.RoundTripper
	t    *testing.T
}

func (p preparationProbeTransport) CloseIdleConnections() {
	if closer, ok := p.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (p preparationProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	p.t.Logf("Selected media range: %s", r.Header.Get("Range"))
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{
		ConnectStart: func(_, address string) {
			p.t.Logf("Selected media phase: connect-start at %s ipv6=%t", time.Since(started), strings.Contains(address, "["))
		},
		ConnectDone: func(_, address string, err error) {
			p.t.Logf("Selected media phase: connect-done at %s ipv6=%t result=%s", time.Since(started), strings.Contains(address, "["), probeResult(err))
		},
		TLSHandshakeStart: func() { p.t.Logf("Selected media phase: tls-start at %s", time.Since(started)) },
		GotConn: func(info httptrace.GotConnInfo) {
			p.t.Logf("Selected media phase: connected at %s reused=%t", time.Since(started), info.Reused)
		},
		WroteHeaders:         func() { p.t.Logf("Selected media phase: headers-sent at %s", time.Since(started)) },
		GotFirstResponseByte: func() { p.t.Logf("Selected media phase: response-start at %s", time.Since(started)) },
	}))
	res, err := p.base.RoundTrip(r)
	status := 0
	if res != nil {
		status = res.StatusCode
	}
	p.t.Logf("Selected media HTTP: elapsed=%s status=%d result=%s", time.Since(started), status, probeResult(err))
	return res, err
}
