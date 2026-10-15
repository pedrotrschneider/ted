package realdebrid

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
)

var ErrPending = errors.New("RD source is not ready")
var ErrMismatch = errors.New("RD source identity does not match saved mapping")

// Resolved never crosses a public JSON boundary. The URL is a secret.
type Resolved struct {
	URL      string `json:"-"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
}
type FileRequest struct {
	AccountID, TorrentID, Hash, Path string
	Bytes                            int64
}

func (c *Client) ResolveFile(ctx context.Context, want FileRequest) (Resolved, error) {
	if c.store != nil {
		v, err := c.Snapshot(ctx)
		if err != nil {
			return Resolved{}, err
		}
		return v.ResolveFile(ctx, want)
	}
	account, err := c.AccountID(ctx)
	if err != nil {
		return Resolved{}, err
	}
	if account != want.AccountID {
		return Resolved{}, ErrMismatch
	}
	if !idPattern.MatchString(want.TorrentID) || !hashPattern.MatchString(want.Hash) || want.Bytes <= 0 {
		return Resolved{}, ErrInvalid
	}
	var raw rawDetail
	if _, err = c.get(ctx, "torrents/info/"+url.PathEscape(want.TorrentID), &raw); err != nil {
		return Resolved{}, err
	}
	detail, err := raw.public(want.TorrentID)
	if err != nil {
		return Resolved{}, err
	}
	if detail.Hash != want.Hash {
		return Resolved{}, ErrMismatch
	}
	if detail.Status != "downloaded" || detail.Progress != 100 {
		return Resolved{}, ErrPending
	}
	// Links correspond to selected files, not raw numeric file IDs. Validate both
	// the selected-list shape and unrestrict's returned filename/size before use.
	index, chosen, count := -1, false, 0
	for _, f := range detail.Files {
		if f.Path == want.Path && f.Bytes == want.Bytes {
			if !f.Selected {
				return Resolved{}, ErrPending
			}
			index = count
			chosen = true
		}
		if f.Selected {
			count++
		}
	}
	if !chosen {
		return Resolved{}, ErrMismatch
	}
	if len(raw.Links) != count || index < 0 || index >= len(raw.Links) {
		return Resolved{}, ErrIncomplete
	}
	link, err := url.Parse(raw.Links[index])
	if err != nil || link.Scheme != "https" || link.Host != "real-debrid.com" || link.User != nil || !strings.HasPrefix(link.Path, "/d/") {
		return Resolved{}, ErrIncomplete
	}
	var result struct {
		Download string `json:"download"`
		Filename string `json:"filename"`
		Filesize int64  `json:"filesize"`
	}
	if _, err = c.request(ctx, "POST", "unrestrict/link", url.Values{"link": {link.String()}}, &result); err != nil {
		return Resolved{}, err
	}
	if result.Filename != path.Base(want.Path) || result.Filesize != want.Bytes {
		return Resolved{}, ErrMismatch
	}
	if !ValidDownloadURL(result.Download) {
		return Resolved{}, ErrIncomplete
	}
	return Resolved{URL: result.Download, Bytes: result.Filesize, Filename: result.Filename}, nil
}

func ValidDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && len(raw) < 8192 && strings.HasSuffix(u.Hostname(), ".download.real-debrid.com")
}
