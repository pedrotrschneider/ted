// Package addons owns installed Stremio add-ons and direct-file playback.
// Configured URLs and provider headers are private persistence, never DTOs.
package addons

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/realdebrid"
)

var (
	ErrNotLinked   = errors.New("link a Stremio account in TED to find sources")
	ErrChanged     = errors.New("Stremio link changed; find sources again")
	ErrCredentials = errors.New("Stremio session rejected; relink the account in TED")
	ErrUnavailable = errors.New("add-on unavailable")
	ErrPreparation = errors.New("selected media preparation unavailable")
	ErrIncomplete  = errors.New("incomplete add-on response")
	ErrUnsupported = errors.New("source is not a supported direct media file")
	ErrSelection   = errors.New("source expired or changed; select a source again")
	ErrNotAddon    = errors.New("session uses a mapped source")
	ErrPlaceholder = errors.New("source returned a short status video instead of the requested media")
)

type Descriptor struct {
	Transport string `json:"transportUrl"`
	Manifest  struct {
		ID        string            `json:"id"`
		Name      string            `json:"name"`
		Types     []string          `json:"types"`
		Prefixes  []string          `json:"idPrefixes"`
		Resources []json.RawMessage `json:"resources"`
	} `json:"manifest"`
}

func (d Descriptor) String() string   { return "Stremio add-on [configuration redacted]" }
func (d Descriptor) GoString() string { return d.String() }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (d Descriptor) Key() string { return digest(d.Manifest.ID + "\x00" + d.Transport) }
func textOK(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

var labelURL = regexp.MustCompile(`(?i)(?:https?://|stremio://)\S+`)
var nativeUUID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
var headerName = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

func stringsBeforeColon(value string) string { return strings.SplitN(value, ":", 2)[0] }
func label(value string, limit int) string {
	if !textOK(value, limit) {
		return ""
	}
	return labelURL.ReplaceAllString(value, "[link]")
}
func parseDescriptor(raw []byte) (Descriptor, error) {
	var d Descriptor
	if json.Unmarshal(raw, &d) != nil || !textOK(d.Manifest.ID, 256) || d.Manifest.ID == "" || !textOK(d.Manifest.Name, 256) || d.Manifest.Name == "" || len(d.Manifest.Types) > 32 || len(d.Manifest.Prefixes) > 64 || len(d.Manifest.Resources) > 32 || len(d.Transport) > 8192 {
		return d, ErrIncomplete
	}
	return d, nil
}
func (d Descriptor) Supports(media catalog.MediaIdentity) bool {
	kind := media.Kind
	if kind == "episode" {
		kind = "series"
	}
	matches := func(types, prefixes []string) bool {
		if !slices.Contains(types, kind) {
			return false
		}
		if prefixes == nil {
			return true
		}
		for _, p := range prefixes {
			if strings.HasPrefix(media.ID, p) {
				return true
			}
		}
		return false
	}
	for _, r := range d.Manifest.Resources {
		var short string
		if json.Unmarshal(r, &short) == nil {
			if short == "stream" && matches(d.Manifest.Types, d.Manifest.Prefixes) {
				return true
			}
			continue
		}
		var full struct {
			Name     string   `json:"name"`
			Types    []string `json:"types"`
			Prefixes []string `json:"idPrefixes"`
		}
		if json.Unmarshal(r, &full) == nil && full.Name == "stream" && matches(full.Types, full.Prefixes) {
			return true
		}
	}
	return false
}
func httpURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, ErrUnsupported
	}
	return u, nil
}
func (d Descriptor) StreamURL(media catalog.MediaIdentity) (string, error) {
	u, err := httpURL(d.Transport)
	if err != nil || !strings.HasSuffix(u.Path, "/manifest.json") {
		return "", ErrUnsupported
	}
	kind := media.Kind
	if kind == "episode" {
		kind = "series"
	}
	u.Path = strings.TrimSuffix(u.Path, "manifest.json") + "stream/" + kind + "/" + media.ID + ".json"
	u.RawPath = ""
	return u.String(), nil
}

type Subtitle struct {
	ID       string            `json:"id"`
	URL      string            `json:"url"`
	Language string            `json:"lang"`
	Label    string            `json:"label"`
	Headers  map[string]string `json:"request_headers,omitempty"`
}
type Source struct {
	InfoHash    string                  `json:"info_hash,omitempty"`
	FileIndex   *int64                  `json:"file_index,omitempty"`
	RDFile      *realdebrid.FileRequest `json:"rd_file,omitempty"`
	RDScope     string                  `json:"rd_scope,omitempty"`
	URL         string                  `json:"url"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Filename    string                  `json:"filename"`
	VideoHash   string                  `json:"video_hash"`
	VideoSize   int64                   `json:"video_size"`
	BingeGroup  string                  `json:"binge_group"`
	Headers     map[string]string       `json:"headers"`
	Subtitles   []Subtitle              `json:"subtitles"`
}

func (s Source) String() string   { return "Add-on source [URL and headers redacted]" }
func (s Source) GoString() string { return s.String() }

// Renewal requires explicit file identity, not only a quality label/group or a
// URL with its query stripped. Selected bytes are verified again before reuse.
func (s Source) ReleaseKey() string {
	if s.InfoHash != "" {
		index := ""
		if s.FileIndex != nil {
			index = jsonNumber(*s.FileIndex)
		}
		return digest(s.InfoHash + "\x00" + index + "\x00" + s.Filename + "\x00" + jsonNumber(s.VideoSize))
	}
	if s.Filename == "" || s.VideoHash == "" || s.VideoSize <= 0 {
		return ""
	}
	return digest(s.Filename + "\x00" + strings.ToLower(s.VideoHash) + "\x00" + jsonNumber(s.VideoSize))
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
func parseSource(raw json.RawMessage) (Source, error) {
	var wire struct {
		InfoHash    string     `json:"infoHash"`
		FileIndex   *int64     `json:"fileIdx"`
		URL         string     `json:"url"`
		Name        string     `json:"name"`
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Subtitles   []Subtitle `json:"subtitles"`
		Hints       struct {
			Filename   string `json:"filename"`
			VideoHash  string `json:"videoHash"`
			VideoSize  int64  `json:"videoSize"`
			BingeGroup string `json:"bingeGroup"`
			Proxy      struct {
				Request map[string]string `json:"request"`
			} `json:"proxyHeaders"`
		} `json:"behaviorHints"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &wire) != nil {
		return Source{}, ErrIncomplete
	}
	for _, key := range []string{"ytId", "externalUrl", "nzbUrl", "rarUrls", "zipUrls", "7zipUrls", "tgzUrls", "tarUrls"} {
		if v, ok := fields[key]; ok && string(v) != "null" {
			return Source{}, ErrUnsupported
		}
	}
	originalURL, _ := httpURL(wire.URL)
	hash := strings.ToLower(wire.InfoHash)
	if hash != "" && !torrentHash.MatchString(hash) || wire.FileIndex != nil && (hash == "" || *wire.FileIndex < 0 || *wire.FileIndex >= 20000) {
		return Source{}, ErrIncomplete
	}
	if hash == "" {
		hash = providerBingeHash(wire.Hints.BingeGroup)
	}
	if hash == "" {
		u, err := httpURL(wire.URL)
		if err != nil {
			return Source{}, err
		}
		if slices.Contains([]string{".m3u8", ".mpd", ".zip", ".rar", ".7z", ".gz", ".tgz", ".tar", ".bz2", ".xz", ".torrent", ".html", ".htm"}, strings.ToLower(pathExtension(u.Path))) {
			return Source{}, ErrUnsupported
		}
	} else {
		// Never retain an add-on's resolver URL as a fallback for identified RD media.
		wire.URL = ""
	}
	if len(wire.Subtitles) > 64 || len(wire.Hints.Proxy.Request) > 32 || !textOK(wire.Hints.Filename, 1024) || !textOK(wire.Hints.VideoHash, 128) || !textOK(wire.Hints.BingeGroup, 512) || wire.Hints.VideoSize < 0 || wire.Hints.VideoSize > 32<<40 {
		return Source{}, ErrIncomplete
	}
	headers := map[string]string{}
	for k, v := range wire.Hints.Proxy.Request {
		k = http.CanonicalHeaderKey(k)
		if !headerName.MatchString(k) || len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") || strings.Contains(v, "ted_a_") || strings.Contains(v, "ted_r_") {
			return Source{}, ErrUnsupported
		}
		switch k {
		case "Host", "Connection", "Content-Length", "Transfer-Encoding", "Range", "If-Range", "Accept-Encoding", "Proxy-Authorization", "Proxy-Connection", "Trailer", "Upgrade":
			return Source{}, ErrUnsupported
		}
		if _, duplicate := headers[k]; duplicate {
			return Source{}, ErrUnsupported
		}
		headers[k] = v
	}
	subtitles := []Subtitle{}
	seen := map[string]bool{}
	for _, sub := range wire.Subtitles {
		sub.Headers = nil // provider wire fields never bypass header validation
		if sub.ID == "" || !textOK(sub.ID, 256) || !textOK(sub.Language, 64) || !textOK(sub.Label, 256) || seen[sub.ID] {
			return Source{}, ErrIncomplete
		}
		if _, err := httpURL(sub.URL); err != nil {
			return Source{}, ErrUnsupported
		}
		seen[sub.ID] = true
		if hash != "" && originalURL != nil {
			u, _ := httpURL(sub.URL)
			if u.Scheme == originalURL.Scheme && strings.EqualFold(u.Host, originalURL.Host) {
				sub.Headers = headers
			}
		}
		sub.Label = label(sub.Label, 256)
		subtitles = append(subtitles, sub)
	}
	description := wire.Description
	if description == "" {
		description = wire.Title
	}
	if hash != "" {
		headers = map[string]string{}
	}
	return Source{InfoHash: hash, FileIndex: wire.FileIndex, URL: wire.URL, Name: label(wire.Name, 256), Description: label(description, 4096), Filename: wire.Hints.Filename, VideoHash: wire.Hints.VideoHash, VideoSize: wire.Hints.VideoSize, BingeGroup: wire.Hints.BingeGroup, Headers: headers, Subtitles: subtitles}, nil
}

var torrentHash = regexp.MustCompile(`^[a-f0-9]{40}$`)

// These are observed provider formats, not the general Stremio binge contract.
// Quality-only/movie groups intentionally retain their direct URL fallback.
func providerBingeHash(group string) string {
	p := strings.Split(strings.ToLower(group), "|")
	if len(p) == 2 && p[0] == "torrentio" && torrentHash.MatchString(p[1]) {
		return p[1]
	}
	if len(p) == 3 && p[0] == "comet" && p[1] != "" && torrentHash.MatchString(p[2]) {
		return p[2]
	}
	// Debrid Torrentio variants retain the torrentio prefix and add the service.
	if len(p) == 3 && p[0] == "torrentio" && (p[1] == "realdebrid" || p[1] == "rd") && torrentHash.MatchString(p[2]) {
		return p[2]
	}
	return ""
}
func pathExtension(path string) string {
	last := strings.LastIndex(path, ".")
	if last < 0 || strings.Contains(path[last:], "/") {
		return ""
	}
	return path[last:]
}
