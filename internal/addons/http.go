package addons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/streaming"
)

var excludedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}
var globalIPv6 = netip.MustParsePrefix("2000::/3")

// Installed add-ons may reject Go's default user-agent. Identify TED on both
// lookup and media requests, while retaining headers explicitly supplied for a file.
const addonUserAgent = "TED/1.0 (Stremio add-on client)"

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.Is6() && !globalIPv6.Contains(ip) {
		return false
	}
	for _, prefix := range excludedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve at dial time and connect to a checked IP. Proxy environment variables
// cannot bypass address checks; redirects repeat them on every new connection.
func PublicClient() *http.Client {
	return checkedPublicClient(8*time.Second, addonLookupTimeout, true)
}

// Discovery has short JSON deadlines; an already selected media URL may redirect
// to a debrid CDN whose TCP startup exceeds them. Keep the same checked-IP and
// redirect policy, and bound preparation independently from the file transfer.
func mediaClient() *http.Client {
	return checkedPublicClient(12*time.Second, 15*time.Second, true)
}

func publicClient(connectTimeout, headerTimeout time.Duration) *http.Client {
	return checkedPublicClient(connectTimeout, headerTimeout, false)
}

func checkedPublicClient(connectTimeout, headerTimeout time.Duration, staggerConnections bool) *http.Client {
	dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: headerTimeout, TLSHandshakeTimeout: 4 * time.Second, MaxIdleConns: 16, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrUnsupported
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				return nil, ErrUnsupported
			}
		}
		if staggerConnections {
			// Both add-on hosts and media CDNs can stall a TCP handshake.
			// Race one fresh connection after 300ms, within
			// the original deadline. Only the winner sends an HTTP request.
			bounded, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			checked := make([]string, len(addresses))
			for i, ip := range interleaveIPFamilies(addresses) {
				checked[i] = net.JoinHostPort(ip.String(), port)
			}
			conn, err := dialMediaAddresses(bounded, network, checked, dialer.DialContext, 300*time.Millisecond)
			if err != nil {
				return nil, err
			}
			return streaming.MediaConnection(conn), nil
		}
		for _, ip := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return streaming.MediaConnection(conn), nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, ErrUnavailable
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 {
			return ErrUnsupported
		}
		if _, err := httpURL(req.URL.String()); err != nil {
			return err
		}
		previous := via[len(via)-1]
		if previous.URL.Scheme == "https" && req.URL.Scheme != "https" {
			return ErrUnsupported
		}
		if req.URL.Host != previous.URL.Host || req.URL.Scheme != previous.URL.Scheme {
			// Only generic headers cross origins; configured credentials/referrers do
			// not follow a provider redirect to another host.
			for key := range req.Header {
				if key != "Accept" && key != "User-Agent" && key != "Range" && key != "Accept-Encoding" {
					req.Header.Del(key)
				}
			}
		}
		return nil
	}}
}
func (s *Service) fetchStreams(ctx context.Context, d Descriptor, mediaKind, mediaID string) (result []Source, rejected int, failure error) {
	started := time.Now()
	status := 0
	defer func() {
		if failure == nil || s.logger == nil || errors.Is(failure, context.Canceled) {
			return
		}
		code := "unavailable"
		switch {
		case errors.Is(failure, context.DeadlineExceeded):
			code = "timeout"
		case errors.Is(failure, ErrIncomplete):
			code = "invalid_response"
		case errors.Is(failure, ErrUnsupported):
			code = "unsupported"
		}
		s.logger.WarnContext(ctx, "add-on lookup failed", "operation", "addon_lookup", "addon_key", d.Key(), "media", mediaID, "status", status, "reason", code, "elapsed_ms", time.Since(started).Milliseconds())
	}()
	target, err := d.StreamURL(mediaIdentity(mediaKind, mediaID))
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, 0, ErrUnsupported
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", addonUserAgent)
	res, err := s.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if errors.Is(err, ErrUnsupported) {
			return nil, 0, ErrUnsupported
		}
		return nil, 0, ErrUnavailable
	}
	defer res.Body.Close()
	status = res.StatusCode
	if res.StatusCode != 200 {
		return nil, 0, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	var wire struct {
		Streams *[]json.RawMessage `json:"streams"`
	}
	if len(data) > 2<<20 || json.Unmarshal(data, &wire) != nil || wire.Streams == nil || len(*wire.Streams) > 200 {
		return nil, 0, ErrIncomplete
	}
	sources := []Source{}
	unsupported := 0
	for _, raw := range *wire.Streams {
		source, err := parseSource(raw)
		if err != nil {
			unsupported++
			continue
		}
		sources = append(sources, source)
	}
	return sources, unsupported, nil
}
func sourceRequest(ctx context.Context, source Source, rangeValue string) (*http.Request, error) {
	if _, err := httpURL(source.URL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, ErrUnsupported
	}
	for key, value := range source.Headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", addonUserAgent)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if rangeValue != "" {
		req.Header.Set("Range", rangeValue)
	}
	return req, nil
}
func contentRange(raw string) (start, end, size int64, err error) {
	var tail string
	n, e := fmt.Sscanf(raw, "bytes %d-%d/%d%s", &start, &end, &size, &tail)
	if e != nil && e != io.EOF || n != 3 || start < 0 || end < start || size <= end || size > 32<<40 {
		return 0, 0, 0, ErrUnsupported
	}
	return start, end, size, nil
}
func fileContentType(value string) bool {
	mime := strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return strings.HasPrefix(mime, "video/") || mime == "application/octet-stream" || mime == "binary/octet-stream" || mime == "application/x-matroska" || mime == "application/force-download"
}

func identityEncoding(value string) bool {
	return value == "" || strings.EqualFold(strings.TrimSpace(value), "identity")
}
func containerPrefix(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}) || len(data) >= 12 && (string(data[4:8]) == "ftyp" || string(data[:4]) == "RIFF" && string(data[8:12]) == "AVI ") || bytes.HasPrefix(data, []byte("OggS")) || bytes.HasPrefix(data, []byte{0, 0, 1, 0xba}) || len(data) > 376 && data[0] == 0x47 && data[188] == 0x47 && data[376] == 0x47
}

type prepared struct {
	Source            Source
	Size              int64
	Fingerprint, ETag string
	startup           *startupBytes
}

func (s *Service) prepare(ctx context.Context, source Source) (prepared, error) {
	req, err := sourceRequest(ctx, source, "bytes=0-4095")
	if err != nil {
		return prepared{}, err
	}
	res, err := s.mediaHTTP().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return prepared{}, ctx.Err()
		}
		if errors.Is(err, ErrUnsupported) {
			return prepared{}, ErrUnsupported
		}
		return prepared{}, ErrUnavailable
	}
	defer res.Body.Close()
	start, end, size, err := contentRange(res.Header.Get("Content-Range"))
	if res.StatusCode != 206 || err != nil || start != 0 || end != min(size-1, 4095) || !fileContentType(res.Header.Get("Content-Type")) || !identityEncoding(res.Header.Get("Content-Encoding")) {
		return prepared{}, ErrUnsupported
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || int64(len(data)) != end+1 {
		if ctx.Err() != nil {
			return prepared{}, ctx.Err()
		}
		return prepared{}, ErrUnsupported
	}
	if err := validateMediaPrefix(data, res.Header.Get("Content-Type")); err != nil {
		return prepared{}, err
	}
	if source.VideoSize > 0 && source.VideoSize != size {
		return prepared{}, ErrSelection
	}
	// Pin both ends of the selected file; renewal compares these bytes, size and
	// explicit add-on release identity before replacing its private URL.
	if size > 4096 {
		from := max(4096, size-4096)
		tailReq, _ := sourceRequest(ctx, source, fmt.Sprintf("bytes=%d-%d", from, size-1))
		tailRes, e := s.mediaHTTP().Do(tailReq)
		if e != nil {
			if ctx.Err() != nil {
				return prepared{}, ctx.Err()
			}
			if errors.Is(e, ErrUnsupported) {
				return prepared{}, ErrUnsupported
			}
			return prepared{}, ErrUnavailable
		}
		defer tailRes.Body.Close()
		a, b, n, e := contentRange(tailRes.Header.Get("Content-Range"))
		if tailRes.StatusCode != 206 || e != nil || a != from || b != size-1 || n != size || !fileContentType(tailRes.Header.Get("Content-Type")) || !identityEncoding(tailRes.Header.Get("Content-Encoding")) || res.Header.Get("ETag") != tailRes.Header.Get("ETag") {
			return prepared{}, ErrUnsupported
		}
		tail, e := io.ReadAll(io.LimitReader(tailRes.Body, 4097))
		if e != nil || int64(len(tail)) != b-a+1 {
			if ctx.Err() != nil {
				return prepared{}, ctx.Err()
			}
			return prepared{}, ErrUnsupported
		}
		data = append(data, tail...)
	}
	sum := sha256.Sum256(data)
	etag := res.Header.Get("ETag")
	if len(etag) > 1024 || strings.HasPrefix(etag, "W/") {
		etag = ""
	}
	return prepared{Source: source, Size: size, Fingerprint: hex.EncodeToString(sum[:]), ETag: etag}, nil
}
func parseRange(raw string, size int64) (start, end int64, err error) {
	if raw == "" {
		return 0, size - 1, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, ErrUnsupported
	}
	left, right, ok := strings.Cut(strings.TrimPrefix(raw, "bytes="), "-")
	if !ok {
		return 0, 0, ErrUnsupported
	}
	end = size - 1
	if left == "" {
		n, e := strconv.ParseInt(right, 10, 64)
		if e != nil || n <= 0 {
			return 0, 0, ErrUnsupported
		}
		return max(0, size-n), end, nil
	}
	start, e := strconv.ParseInt(left, 10, 64)
	if e != nil || start < 0 || start >= size {
		return 0, 0, ErrUnsupported
	}
	if right != "" {
		end, e = strconv.ParseInt(right, 10, 64)
		if e != nil || end < start {
			return 0, 0, ErrUnsupported
		}
		end = min(end, size-1)
	}
	return start, end, nil
}

func validateMediaPrefix(data []byte, contentType string) error {
	if strings.HasPrefix(strings.ToLower(contentType), "application/force-download") && !containerPrefix(data) {
		return ErrUnsupported
	}
	prefix := strings.ToLower(strings.TrimSpace(string(data[:min(128, len(data))])))
	archive := bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("Rar!\x1a\x07")) || bytes.HasPrefix(data, []byte("7z\xbc\xaf\x27\x1c")) || bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || bytes.HasPrefix(data, []byte("BZh")) || bytes.HasPrefix(data, []byte{0xfd, '7', 'z', 'X', 'Z', 0}) || len(data) > 262 && string(data[257:262]) == "ustar"
	if strings.HasPrefix(prefix, "#extm3u") || strings.HasPrefix(prefix, "<!doctype") || strings.HasPrefix(prefix, "<html") || strings.HasPrefix(prefix, "<?xml") || strings.HasPrefix(prefix, "<mpd") || archive {
		return ErrUnsupported
	}
	return nil
}

// Give each IP family an early lane instead of spending the connection budget
// on multiple IPv4 addresses before trying a working IPv6 route (or vice versa).
func interleaveIPFamilies(addresses []netip.Addr) []netip.Addr {
	if len(addresses) < 2 {
		return addresses
	}
	first, other := []netip.Addr{}, []netip.Addr{}
	for _, ip := range addresses {
		if ip.Is4() == addresses[0].Is4() {
			first = append(first, ip)
		} else {
			other = append(other, ip)
		}
	}
	out := make([]netip.Addr, 0, len(addresses))
	for i := 0; i < len(first) || i < len(other); i++ {
		if i < len(first) {
			out = append(out, first[i])
		}
		if i < len(other) {
			out = append(out, other[i])
		}
	}
	return out
}
