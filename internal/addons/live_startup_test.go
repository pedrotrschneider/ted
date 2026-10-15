package addons

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
)

// Read-only diagnosis against one already prepared session. Never prints URLs,
// provider headers or IPs, and never reports viewing or creates an RD torrent.
func TestLiveStartupConnections(t *testing.T) {
	id := os.Getenv("TED_LIVE_STARTUP_SESSION")
	if id == "" && os.Getenv("TED_LIVE_STARTUP_STDIN") != "1" {
		t.Skip("no diagnostic session")
	}
	if id != "" && !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var raw []byte
	if os.Getenv("TED_LIVE_STARTUP_STDIN") == "1" {
		raw, _ = io.ReadAll(io.LimitReader(os.Stdin, 65536))
	} else {
		pool, err := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
		if err != nil {
			t.Fatal("database unavailable")
		}
		defer pool.Close()
		if pool.QueryRow(ctx, `SELECT source FROM native_addon_playback WHERE session_id=$1`, id).Scan(&raw) != nil {
			t.Fatal("session unavailable")
		}
	}
	var source Source
	if json.Unmarshal(raw, &source) != nil || !realdebrid.ValidDownloadURL(source.URL) {
		t.Fatal("invalid source")
	}
	u, _ := httpURL(source.URL)
	ips, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	t.Logf("RD endpoint scheme=%s public_host=%s address_count=%d", u.Scheme, u.Hostname(), len(ips))
	for round := 0; round < 3; round++ {
		if os.Getenv("TED_LIVE_STARTUP_STDIN") == "1" {
			cmd := exec.CommandContext(ctx, "curl", "--config", "-")
			config := "url = \"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(source.URL) + "\"\nrange = \"0-4095\"\noutput = \"/dev/null\"\nsilent\nmax-time = 15\nwrite-out = \"curl status=%{http_code} dns=%{time_namelookup} connect=%{time_connect} tls=%{time_appconnect} firstbyte=%{time_starttransfer} total=%{time_total}\\n\"\n"
			cmd.Stdin = strings.NewReader(config)
			out, err := cmd.Output()
			t.Logf("CURL %s result=%s", out, probeResult(err))
		}
		for _, mode := range []string{"production", "standard", "ipv4", "ipv6"} {
			client := mediaClient()
			if mode != "production" {
				transport := client.Transport.(*http.Transport)
				dialer := &net.Dialer{Timeout: 8 * time.Second}
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(address)
					if err != nil {
						return nil, ErrUnsupported
					}
					ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
					if err != nil {
						return nil, err
					}
					for _, ip := range ips {
						if !publicAddress(ip) {
							return nil, ErrUnsupported
						}
					}
					// Diagnostic comparison with the standard hostname dialer's address
					// ordering. Restricted to an existing, server-validated RD session.
					if mode == "ipv4" {
						network = "tcp4"
					}
					if mode == "ipv6" {
						network = "tcp6"
					}
					return dialer.DialContext(ctx, network, address)
				}
			}
			client.Transport = preparationProbeTransport{base: client.Transport, t: t}
			req, _ := sourceRequest(ctx, source, "bytes=0-4095")
			started := time.Now()
			t.Logf("START round=%d mode=%s", round, mode)
			response, err := client.Do(req)
			status, n := 0, int64(0)
			if response != nil {
				status = response.StatusCode
				n, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
			}
			t.Logf("RESULT round=%d mode=%s status=%d bytes=%d elapsed=%s result=%s", round, mode, status, n, time.Since(started), probeResult(err))
			client.CloseIdleConnections()
		}
	}
}
