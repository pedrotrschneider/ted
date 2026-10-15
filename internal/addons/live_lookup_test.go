package addons

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
)

// Opt-in, read-only lookup diagnosis in the deployed network. Private collection
// URLs stay in memory; only public names, timings and classified results are logged.
func TestLiveAddonLookup(t *testing.T) {
	if os.Getenv("TED_LIVE_ADDON_LOOKUP") != "1" {
		t.Skip("live lookup not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if err != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	var raw []byte
	err = pool.QueryRow(ctx, `SELECT payload FROM server_addon_collections ORDER BY observed_at DESC LIMIT 1`).Scan(&raw)
	if err != nil {
		t.Fatal("collection unavailable")
	}
	var wire []json.RawMessage
	if json.Unmarshal(raw, &wire) != nil {
		t.Fatal("invalid collection")
	}
	descriptors, err := parseCollection(wire)
	if err != nil {
		t.Fatal("invalid descriptors")
	}
	disabled, err := disabledProviders(ctx, pool)
	if err != nil {
		t.Fatal("policy unavailable")
	}
	budgets := []time.Duration{4 * time.Second, 20 * time.Second}
	if os.Getenv("TED_LIVE_LOOKUP_FIXED") == "1" {
		budgets = []time.Duration{addonLookupTimeout, addonLookupTimeout, addonLookupTimeout}
	}
	for _, budget := range budgets {
		for _, d := range descriptors {
			if disabled[d.PolicyKey()] || !d.Supports(mediaIdentity("episode", "tt14976292:1:4")) {
				continue
			}
			client := publicClient(budget, budget)
			if os.Getenv("TED_LIVE_LOOKUP_FIXED") == "1" {
				client = PublicClient()
			}
			client.Transport = lookupProbeTransport{base: client.Transport, t: t}
			s := &Service{http: client}
			bounded, stop := context.WithTimeout(ctx, budget)
			start := time.Now()
			sources, unsupported, e := s.fetchStreams(bounded, d, "episode", "tt14976292:1:4")
			stop()
			client.CloseIdleConnections()
			t.Logf("addon=%q budget=%s elapsed=%s sources=%d unsupported=%d result=%s", label(d.Manifest.Name, 256), budget, time.Since(start), len(sources), unsupported, probeResult(e))
		}
	}
}

type lookupProbeTransport struct {
	base http.RoundTripper
	t    *testing.T
}

func (p lookupProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	trace := &httptrace.ClientTrace{
		DNSDone: func(i httptrace.DNSDoneInfo) {
			p.t.Logf("dns elapsed=%s addresses=%d error=%t", time.Since(start), len(i.Addrs), i.Err != nil)
		},
		ConnectStart: func(_, addr string) {
			host, _, _ := net.SplitHostPort(addr)
			p.t.Logf("connect elapsed=%s ipv4=%t", time.Since(start), net.ParseIP(host).To4() != nil)
		},
		ConnectDone: func(_, _ string, e error) {
			p.t.Logf("connect done elapsed=%s result=%s", time.Since(start), probeResult(e))
		},
		GotConn:              func(httptrace.GotConnInfo) { p.t.Logf("connected elapsed=%s", time.Since(start)) },
		GotFirstResponseByte: func() { p.t.Logf("first byte elapsed=%s", time.Since(start)) },
	}
	res, err := p.base.RoundTrip(r.WithContext(httptrace.WithClientTrace(r.Context(), trace)))
	if res != nil {
		p.t.Logf("HTTP status=%d length=%d", res.StatusCode, res.ContentLength)
	}
	return res, err
}
