package addons

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/stremio"
)

// Opt-in, read-only service verification. The key is supplied in memory by the
// local helper; this creates no remote watched edits or owner-database writes.
func TestLiveInstalledAddonDirectFiles(t *testing.T) {
	key := os.Getenv("TED_LIVE_STREMIO_KEY")
	if key == "" {
		t.Skip("no live Stremio account supplied")
	}
	s, auth, grant, _, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if result, err := s.pool.Exec(ctx, `UPDATE server_addon_accounts SET auth_key=$1 WHERE account_id='fixture'`, key); err != nil || result.RowsAffected() != 1 {
		t.Fatal("isolated account setup failed")
	}
	s.remote = stremio.New()
	s.http = PublicClient()
	s.media = mediaClient()
	view, err := s.Collection(ctx, grant.User.ID, true)
	if err != nil {
		t.Fatal("live installed collection:", err)
	}
	supported := 0
	for _, addon := range view.Addons {
		if addon.Supported {
			supported++
		}
	}
	t.Logf("Live installed collection: %d add-ons, %d support movie/episode streams", len(view.Addons), supported)
	for _, media := range []struct{ kind, id string }{{"movie", "tt1345836"}, {"episode", "tt0386676:1:4"}} {
		identity := mediaIdentity(media.kind, media.id)
		page, err := s.Sources(ctx, auth, grant, identity, "")
		if err != nil {
			t.Fatal("live source lookup:", err)
		}
		t.Logf("Live %s lookup: %d direct-file choices, %d issues", media.kind, len(page.Sources), len(page.Issues))
		for _, issue := range page.Issues {
			t.Logf("Live issue: %s / %s / %d", issue.Addon, issue.Code, issue.Count)
		}
		prepared := false
		for _, choice := range page.Sources[:min(len(page.Sources), 3)] {
			session, err := s.CreateSession(ctx, auth, grant, identity, choice.ID)
			if err != nil {
				t.Log("Selected live source could not prepare:", err)
				value, e := s.selected(ctx, grant, choice.ID)
				if e == nil {
					req, _ := sourceRequest(ctx, value.source, "bytes=0-4095")
					res, e := s.http.Do(req)
					if e != nil {
						t.Log("Selected probe network rejected or unavailable")
					} else {
						t.Logf("Selected probe: status %d, content type %q, content range %q, encoding %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Range"), res.Header.Get("Content-Encoding"))
						res.Body.Close()
					}
				}
				continue
			}
			req := httptest.NewRequest("GET", session.MediaURL, nil).WithContext(ctx)
			req.Header.Set("Range", "bytes=0-1023")
			w := httptest.NewRecorder()
			if err := s.Serve(w, req, grant, session.ID); err != nil || w.Code != 206 || w.Body.Len() != 1024 {
				t.Fatal("selected live range failed", err, w.Code, w.Body.Len())
			}
			t.Logf("Selected live %s: preparation and authenticated 1024-byte range passed; %d sidecars", media.kind, len(session.Subtitles))
			prepared = true
			break
		}
		if !prepared {
			t.Logf("Live %s playback remains unverified: no usable selected source", media.kind)
		}
	}
}

// Optional protocol diagnosis logs fixed error classes and counts only. Add-on
// transport URLs, stream URLs and provider credentials never enter test output.
func TestLiveTorrentioStreamDiagnosis(t *testing.T) {
	key := os.Getenv("TED_LIVE_STREMIO_KEY")
	if key == "" {
		t.Skip("no live Stremio account supplied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	wire, err := stremio.New().AddonCollection(ctx, key)
	if err != nil {
		t.Fatal("installed collection unavailable")
	}
	client := PublicClient()
	original := client.Transport
	client.Transport = diagnosticTransport{base: original, t: t}
	s := &Service{http: client}
	for _, raw := range wire {
		d, err := parseDescriptor(raw)
		if err != nil || !strings.Contains(strings.ToLower(d.Manifest.Name), "torrentio") {
			continue
		}
		for _, budget := range []time.Duration{4 * time.Second, 15 * time.Second} {
			bounded, stop := context.WithTimeout(ctx, budget)
			started := time.Now()
			sources, unsupported, err := s.fetchStreams(bounded, d, "episode", "tt0386676:1:4")
			elapsed := time.Since(started)
			stop()
			code := "success"
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				code = "deadline"
			case errors.Is(err, ErrUnsupported):
				code = "unsupported"
			case errors.Is(err, ErrIncomplete):
				code = "incomplete"
			case err != nil:
				code = "unavailable"
			}
			t.Logf("%s: budget=%s elapsed=%s result=%s direct=%d unsupported=%d", label(d.Manifest.Name, 256), budget, elapsed, code, len(sources), unsupported)
		}
	}
}

type diagnosticTransport struct {
	base http.RoundTripper
	t    *testing.T
}

// Read-only timing comparison for the owner's reported cold bulk lookups.
// Print only episode IDs, fixed result classes and counts; never private URLs.
func TestLiveBulkTorrentioTiming(t *testing.T) {
	key := os.Getenv("TED_LIVE_STREMIO_KEY")
	if key == "" {
		t.Skip("no live Stremio account supplied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	wire, err := stremio.New().AddonCollection(ctx, key)
	if err != nil {
		t.Fatal("installed collection unavailable")
	}
	client := PublicClient()
	client.Transport.(*http.Transport).ResponseHeaderTimeout = 15 * time.Second
	s := &Service{http: client}
	for _, raw := range wire {
		d, err := parseDescriptor(raw)
		if err != nil || d.Manifest.Name != "Torrentio RD" {
			continue
		}
		for _, id := range []string{"tt14976292:1:5", "tt14976292:1:6", "tt14976292:1:7"} {
			for _, budget := range []time.Duration{4 * time.Second, 15 * time.Second} {
				bounded, stop := context.WithTimeout(ctx, budget)
				started := time.Now()
				sources, unsupported, err := s.fetchStreams(bounded, d, "episode", id)
				elapsed := time.Since(started)
				stop()
				code := "success"
				if errors.Is(err, context.DeadlineExceeded) {
					code = "deadline"
				} else if err != nil {
					code = "unavailable"
				}
				t.Logf("episode=%s budget=%s elapsed=%s result=%s direct=%d unsupported=%d", id, budget, elapsed, code, len(sources), unsupported)
			}
		}
		return
	}
	t.Fatal("Torrentio RD is not installed")
}

func (d diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := d.base.RoundTrip(r)
	if err != nil {
		code := "network"
		if errors.Is(err, ErrUnsupported) {
			code = "unsupported"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "deadline"
		}
		d.t.Log("Request failure class:", code)
	} else {
		d.t.Logf("Response: status=%d protocol=%s content_type=%q encoding=%q challenge=%t", res.StatusCode, res.Proto, res.Header.Get("Content-Type"), res.Header.Get("Content-Encoding"), res.Header.Get("Cf-Mitigated") != "")
	}
	return res, err
}
