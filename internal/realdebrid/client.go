// Package realdebrid owns the provider protocol. Public types omit all provider URLs and credentials.
package realdebrid

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"entertainmentdistrict/internal/settings"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotConfigured = errors.New("Real-Debrid is not configured")
	ErrCredentials   = errors.New("Real-Debrid rejected the configured credential")
	ErrUnavailable   = errors.New("Real-Debrid is unavailable")
	ErrLimited       = errors.New("Real-Debrid rate limit reached")
	ErrIncomplete    = errors.New("Real-Debrid returned incomplete or invalid data")
	ErrNotFound      = errors.New("Real-Debrid torrent was not found")
	ErrInvalid       = errors.New("invalid Real-Debrid request")
	ErrInfringing    = errors.New("Real-Debrid rejected an infringing file")
	ErrRejected      = errors.New("Real-Debrid rejected the torrent or file")
)

const PageSize = 100
const maxBody = 8 << 20

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

type Torrent struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Bytes    int64     `json:"bytes"`
	Status   string    `json:"status"`
	Progress float64   `json:"progress"`
	Added    time.Time `json:"added"`
	Hash     string    `json:"hash,omitempty"`
}
type Page struct {
	Torrents []Torrent `json:"torrents"`
	Page     int       `json:"page"`
	Total    *int      `json:"total,omitempty"`
	NextPage *int      `json:"next_page,omitempty"`
}
type File struct {
	ID                 int64  `json:"id"`
	Path               string `json:"path"`
	Bytes              int64  `json:"bytes"`
	Selected           bool   `json:"selected"`
	Video              bool   `json:"video"`
	SuggestedExclusion bool   `json:"suggested_exclusion"`
}
type Detail struct {
	Torrent
	Hash  string `json:"hash"`
	Files []File `json:"files"`
}
type rawTorrent struct {
	Hash     string   `json:"hash"`
	ID       string   `json:"id"`
	Filename string   `json:"filename"`
	Bytes    *int64   `json:"bytes"`
	Status   string   `json:"status"`
	Progress *float64 `json:"progress"`
	Added    string   `json:"added"`
}

func (r rawTorrent) public() (Torrent, error) {
	added, err := time.Parse(time.RFC3339, r.Added)
	if err != nil || !idPattern.MatchString(r.ID) || !validText(r.Filename, 2048) || r.Bytes == nil || *r.Bytes < 0 || !validText(r.Status, 64) || r.Progress == nil || *r.Progress < 0 || *r.Progress > 100 {
		return Torrent{}, ErrIncomplete
	}
	if r.Hash != "" && !hashPattern.MatchString(r.Hash) {
		return Torrent{}, ErrIncomplete
	}
	return Torrent{ID: r.ID, Name: r.Filename, Bytes: *r.Bytes, Status: r.Status, Progress: *r.Progress, Added: added, Hash: strings.ToLower(r.Hash)}, nil
}
func validText(s string, max int) bool {
	if strings.TrimSpace(s) == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type Client struct {
	key   string
	http  *http.Client
	base  string
	store *settings.Store
}

func New(key string) *Client {
	return &Client{key: key, base: "https://api.real-debrid.com/rest/1.0/", http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func NewStored(store *settings.Store) *Client { c := New(""); c.store = store; return c }

// Snapshot pins the credential for a whole operation, including recovered torrents and link-cache writes.
func (c *Client) Snapshot(ctx context.Context) (*Client, error) {
	if c.store == nil {
		return c, nil
	}
	v, err := c.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{key: v.RDKey, http: c.http, base: c.base}, nil
}
func (c *Client) Configuration(ctx context.Context) (bool, error) {
	v, err := c.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	return v.key != "", nil
}
func (c *Client) Configured() bool {
	if c.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		v, err := c.Snapshot(ctx)
		return err == nil && v.Configured()
	}
	return c.key != ""
}
func (c *Client) CredentialScope() string {
	if c.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		v, err := c.Snapshot(ctx)
		if err != nil {
			return ""
		}
		return v.CredentialScope()
	}
	sum := sha256.Sum256([]byte(c.key))
	return hex.EncodeToString(sum[:])
}

// AccountID is durable across API key rotation and distinguishes different RD accounts.
// No profile information or credentials leave this adapter.
func (c *Client) AccountID(ctx context.Context) (string, error) {
	if c.store != nil {
		v, err := c.Snapshot(ctx)
		if err != nil {
			return "", err
		}
		return v.AccountID(ctx)
	}
	var raw struct {
		ID int64 `json:"id"`
	}
	if _, err := c.get(ctx, "user", &raw); err != nil {
		return "", err
	}
	if raw.ID <= 0 {
		return "", ErrIncomplete
	}
	return strconv.FormatInt(raw.ID, 10), nil
}

// get never returns raw transport errors or provider bodies: both may contain secrets or URLs.
func (c *Client) get(ctx context.Context, route string, dst any) (http.Header, error) {
	return c.request(ctx, http.MethodGet, route, nil, dst)
}
func (c *Client) request(ctx context.Context, method, route string, form url.Values, dst any) (http.Header, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+route, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var failure struct {
			Code int `json:"error_code"`
		}
		if readErr == nil && len(data) <= maxBody && json.Unmarshal(data, &failure) == nil {
			switch failure.Code {
			case 35:
				return nil, ErrInfringing
			case 24:
				return nil, ErrNotFound
			case 28, 29, 30:
				return nil, ErrRejected
			case 5, 34:
				return nil, ErrLimited
			}
		}
	}
	switch resp.StatusCode {
	case 200, 201:
	case 204:
		if dst == nil {
			return resp.Header, nil
		}
		return nil, ErrIncomplete
	case 401, 403:
		return nil, ErrCredentials
	case 404:
		return nil, ErrNotFound
	case 429:
		return nil, ErrLimited
	default:
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(data) > maxBody || (dst != nil && json.Unmarshal(data, dst) != nil) {
		return nil, ErrIncomplete
	}
	return resp.Header, nil
}
func (c *Client) List(ctx context.Context, page int) (Page, error) {
	if c.store != nil {
		v, err := c.Snapshot(ctx)
		if err != nil {
			return Page{}, err
		}
		return v.List(ctx, page)
	}
	if page < 1 || page > 10000 {
		return Page{}, ErrInvalid
	}
	var raw []rawTorrent
	headers, err := c.get(ctx, "torrents?limit="+strconv.Itoa(PageSize)+"&page="+strconv.Itoa(page), &raw)
	if err != nil {
		return Page{}, err
	}
	if raw == nil || len(raw) > PageSize {
		return Page{}, ErrIncomplete
	}
	result := Page{Torrents: make([]Torrent, 0, len(raw)), Page: page}
	if count := headers.Get("X-Total-Count"); count != "" {
		total, err := strconv.Atoi(count)
		if err != nil || total < 0 {
			return Page{}, ErrIncomplete
		}
		result.Total = &total
		remaining := total - (page-1)*PageSize
		if remaining < 0 || len(raw) != min(PageSize, remaining) {
			return Page{}, ErrIncomplete
		}
	}
	seen := map[string]bool{}
	for _, item := range raw {
		torrent, err := item.public()
		if err != nil || seen[torrent.ID] {
			return Page{}, ErrIncomplete
		}
		seen[torrent.ID] = true
		result.Torrents = append(result.Torrents, torrent)
	}
	if (result.Total != nil && page*PageSize < *result.Total) || (result.Total == nil && len(raw) == PageSize) {
		next := page + 1
		result.NextPage = &next
	}
	return result, nil
}

type rawDetail struct {
	rawTorrent
	Files []struct {
		ID       int64  `json:"id"`
		Path     string `json:"path"`
		Bytes    *int64 `json:"bytes"`
		Selected *int   `json:"selected"`
	} `json:"files"`
	Links []string `json:"links"`
}

func (c *Client) Torrent(ctx context.Context, id string) (Detail, error) {
	if c.store != nil {
		v, err := c.Snapshot(ctx)
		if err != nil {
			return Detail{}, err
		}
		return v.Torrent(ctx, id)
	}
	if !idPattern.MatchString(id) {
		return Detail{}, ErrInvalid
	}
	var raw rawDetail
	_, err := c.get(ctx, "torrents/info/"+url.PathEscape(id), &raw)
	if err != nil {
		return Detail{}, err
	}
	return raw.public(id)
}
func (raw rawDetail) public(id string) (Detail, error) {
	torrent, err := raw.rawTorrent.public()
	if err != nil || torrent.ID != id || !hashPattern.MatchString(raw.Hash) || raw.Files == nil || len(raw.Files) > 20000 {
		return Detail{}, ErrIncomplete
	}
	detail := Detail{Torrent: torrent, Hash: strings.ToLower(raw.Hash), Files: make([]File, 0, len(raw.Files))}
	seen := map[int64]bool{}
	paths := map[string]bool{}
	for _, file := range raw.Files {
		if file.ID <= 0 || seen[file.ID] || paths[file.Path] || file.Bytes == nil || *file.Bytes < 0 || file.Selected == nil || (*file.Selected != 0 && *file.Selected != 1) || !validText(file.Path, 4096) || !strings.HasPrefix(file.Path, "/") || strings.Contains(file.Path, "\\") || path.Clean(file.Path) != file.Path || file.Path == "/" {
			return Detail{}, ErrIncomplete
		}
		seen[file.ID] = true
		paths[file.Path] = true
		ext := strings.ToLower(path.Ext(file.Path))
		video := ext == ".mkv" || ext == ".mp4" || ext == ".avi" || ext == ".m4v" || ext == ".ts" || ext == ".webm" || ext == ".mov"
		excluded := false
		parts := strings.Split(strings.TrimPrefix(file.Path, "/"), "/")
		for _, part := range parts[:len(parts)-1] {
			if strings.EqualFold(part, "extra") || strings.EqualFold(part, "extras") || strings.EqualFold(part, "sample") {
				excluded = true
			}
		}
		detail.Files = append(detail.Files, File{ID: file.ID, Path: file.Path, Bytes: *file.Bytes, Selected: *file.Selected == 1, Video: video, SuggestedExclusion: excluded})
	}
	return detail, nil
}
