// Package streaming owns authenticated, ranged delivery of explicitly published movies.
package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/realdebrid"
)

var ErrUnavailable = errors.New("stream temporarily unavailable")
var ErrInvalid = errors.New("invalid streaming request")
var ErrNotReady = errors.New("movie is not published for streaming")

func MediaClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second, DisableCompression: true, MaxIdleConnsPerHost: 8,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, ErrUnavailable
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, ErrUnavailable
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || !ip.IP.IsGlobalUnicast() || ip.IP.IsLinkLocalUnicast() {
					return nil, ErrUnavailable
				}
			}
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return &idleConnection{Conn: conn}, nil
				}
			}
			return nil, ErrUnavailable
		}}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !realdebrid.ValidDownloadURL(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

type byteRange struct {
	start, end int64
	partial    bool
}

func parseRange(raw string, size int64) (byteRange, error) {
	result := byteRange{0, size - 1, false}
	if raw == "" {
		return result, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return result, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 {
		return result, ErrInvalid
	}
	result.partial = true
	if parts[0] == "" {
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return result, ErrInvalid
		}
		result.start = max(0, size-n)
		return result, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return result, ErrInvalid
	}
	result.start = start
	if parts[1] != "" {
		end, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return result, ErrInvalid
		}
		result.end = min(end, size-1)
	}
	return result, nil
}

// A larger upstream range amortizes RD's per-request startup cost without
// buffering the range in memory. Local download checkpoints remain independent.
const remoteStreamChunkSize int64 = 1 << 30

// sendMedia requests bounded chunks upstream. Rejected links are renewed before
// copying the next chunk. Interrupted bodies require a client range reconnect.
func (s *Service) sendMedia(w http.ResponseWriter, r *http.Request, m Media) {
	want, err := parseRange(r.Header.Get("Range"), m.Bytes)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", m.Bytes))
		http.Error(w, "invalid range", 416)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-TED-Source", "remote")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(want.end-want.start+1, 10))
	if want.partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", want.start, want.end, m.Bytes))
	}
	// HEAD exposes persisted length, never a temporary RD link or provider header.
	if r.Method == "HEAD" {
		if want.partial {
			w.WriteHeader(206)
		} else {
			w.WriteHeader(200)
		}
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	wrote := false
	position := want.start
	renewals := 0
	for position <= want.end {
		end := min(position+remoteStreamChunkSize-1, want.end)
		link, err := s.link(r.Context(), m, "")
		if err != nil {
			recordProbeEvent(r.Context(), probeRelayEvent{Stage: "resolve", Code: probeTransportCode(err), Start: position, End: end})
			if !wrote {
				w.Header().Del("Content-Length")
				w.Header().Del("Content-Range")
				http.Error(w, "source unavailable; retry playback", 503)
			}
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), "GET", link, nil)
		if err != nil {
			return
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", position, end))
		req.Header.Set("Accept-Encoding", "identity")
		response, err := s.mediaHTTP.Do(req)
		if err != nil {
			recordProbeEvent(r.Context(), probeRelayEvent{Stage: "upstream_request", Code: probeTransportCode(err), Start: position, End: end})
			if !wrote {
				w.Header().Del("Content-Length")
				w.Header().Del("Content-Range")
				http.Error(w, "source unavailable; retry playback", 503)
			}
			return
		}
		recordProbeEvent(r.Context(), probeRelayEvent{Stage: "upstream_response", Status: response.StatusCode, Start: position, End: end, Length: response.ContentLength, RangeMatches: response.Header.Get("Content-Range") == fmt.Sprintf("bytes %d-%d/%d", position, end, m.Bytes)})
		if response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 410 {
			response.Body.Close()
			if renewals < 2 {
				renewals++
				if _, err = s.link(r.Context(), m, link); err == nil {
					continue
				}
			}
			if !wrote {
				w.Header().Del("Content-Length")
				w.Header().Del("Content-Range")
				http.Error(w, "source link unavailable", 503)
			}
			return
		}
		expected := fmt.Sprintf("bytes %d-%d/%d", position, end, m.Bytes)
		if response.StatusCode != 206 || response.Header.Get("Content-Range") != expected || response.ContentLength != end-position+1 || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
			response.Body.Close()
			if !wrote {
				w.Header().Del("Content-Length")
				w.Header().Del("Content-Range")
				http.Error(w, "invalid media response", 502)
			}
			return
		}
		if !wrote {
			if want.partial {
				w.WriteHeader(206)
			} else {
				w.WriteHeader(200)
			}
			wrote = true
		}
		// A downstream write deadline bounds slow/disconnected clients; each copy
		// chunk gets a fresh deadline rather than a total movie-length timeout.
		n, copyErr := copyChunk(controller, w, response.Body, end-position+1)
		response.Body.Close()
		position += n
		if copyErr != nil {
			recordProbeEvent(r.Context(), probeRelayEvent{Stage: "body_copy", Code: probeTransportCode(copyErr), Start: position - n, End: end, Length: n})
			return
		} // Client reconnects at its confirmed offset after transport failure.
	}
}

// CopyMedia bounds each downstream write rather than the whole media response.
// Add-on relays share the mapped/local playback copy and short-body semantics.
func CopyMedia(w http.ResponseWriter, reader io.Reader, length int64) (int64, error) {
	return copyChunk(http.NewResponseController(w), w, reader, length)
}

func copyChunk(controller *http.ResponseController, w io.Writer, reader io.Reader, length int64) (int64, error) {
	var copied int64
	buf := make([]byte, 64<<10)
	for copied < length {
		n, err := reader.Read(buf[:min(int64(len(buf)), length-copied)])
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			written, e := w.Write(buf[:n])
			copied += int64(written)
			if e != nil {
				return copied, e
			}
			if written != n {
				return copied, io.ErrShortWrite
			}
		}
		if err != nil {
			if err == io.EOF && copied == length {
				return copied, nil
			}
			return copied, err
		}
	}
	return copied, nil
}

type idleConnection struct{ net.Conn }

// MediaConnection bounds stalled upstream reads while allowing a full movie's
// transfer to exceed the JSON API's operation deadlines.
func MediaConnection(conn net.Conn) net.Conn { return &idleConnection{conn} }

func (c *idleConnection) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	return c.Conn.Read(p)
}
