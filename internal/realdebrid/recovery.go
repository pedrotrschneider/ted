package realdebrid

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// PrepareFile preserves existing account selections. Only an entry TED created
// and durably recorded may select the requested file. Original IDs remain provenance.
// An ambiguous add response is not retried blindly: a subsequent pass reads the
// account again, and pending entries require waiting rather than reselection.
func (c *Client) PrepareFile(ctx context.Context, want FileRequest, owned string, remember func(string) error) (FileRequest, error) {
	if c.store != nil {
		v, err := c.Snapshot(ctx)
		if err != nil {
			return FileRequest{}, err
		}
		return v.PrepareFile(ctx, want, owned, remember)
	}
	account, err := c.AccountID(ctx)
	if err != nil {
		return want, err
	}
	if account != want.AccountID {
		return want, ErrMismatch
	}
	inspect := func(id string) (Detail, error) { return c.Torrent(ctx, id) }
	matches := func(d Detail) bool {
		for _, f := range d.Files {
			if f.Path == want.Path && f.Bytes == want.Bytes && f.Selected {
				return true
			}
		}
		return false
	}
	if owned != "" {
		d, e := inspect(owned)
		if e == nil {
			if d.Hash != want.Hash {
				return want, ErrMismatch
			}
			if d.Status == "waiting_files_selection" {
				if e = c.selectFile(ctx, d, want); e != nil {
					return want, e
				}
				want.TorrentID = owned
				return want, nil
			}
			if matches(d) {
				want.TorrentID = owned
				return want, nil
			}
		}
		if e != nil && !errors.Is(e, ErrNotFound) {
			return want, e
		}
	}
	d, err := inspect(want.TorrentID)
	if err == nil {
		if d.Hash != want.Hash {
			return want, ErrMismatch
		}
		if matches(d) {
			return want, nil
		}
		if d.Status != "downloaded" {
			return want, ErrPending
		}
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return want, err
	}
	// A complete bounded account scan is required before considering an add.
	seen := map[string]bool{}
	pending := false
	expectedTotal := -1
	for page := 1; page <= 100; page++ {
		var raw []struct {
			rawTorrent
			Hash string `json:"hash"`
		}
		headers, e := c.get(ctx, "torrents?limit=100&page="+strconv.Itoa(page), &raw)
		if e != nil {
			return want, e
		}
		if raw == nil || len(raw) > 100 {
			return want, ErrIncomplete
		}
		total, e := strconv.Atoi(headers.Get("X-Total-Count"))
		if e != nil || total < 0 || total > (100*100) || len(raw) != min(100, max(0, total-(page-1)*100)) {
			return want, ErrIncomplete
		}
		if expectedTotal < 0 {
			expectedTotal = total
		}
		if expectedTotal != total {
			return want, ErrIncomplete
		}
		for _, item := range raw {
			if _, e = item.public(); e != nil || seen[item.ID] || !hashPattern.MatchString(item.Hash) {
				return want, ErrIncomplete
			}
			seen[item.ID] = true
			if !strings.EqualFold(item.Hash, want.Hash) {
				continue
			}
			found, e := inspect(item.ID)
			if e != nil {
				return want, e
			}
			if found.Hash != want.Hash {
				return want, ErrIncomplete
			}
			if matches(found) {
				want.TorrentID = item.ID
				return want, nil
			}
			if found.Status != "downloaded" {
				pending = true
			}
		}
		if page*100 >= total {
			break
		}
		if page == 100 {
			return want, ErrIncomplete
		}
	}
	if pending {
		return want, ErrPending
	}
	var added struct {
		ID string `json:"id"`
	}
	if _, err = c.request(ctx, "POST", "torrents/addMagnet", url.Values{"magnet": {"magnet:?xt=urn:btih:" + want.Hash}}, &added); err != nil {
		return want, err
	}
	if !idPattern.MatchString(added.ID) || seen[added.ID] {
		return want, ErrIncomplete
	}
	// Commit the ownership reference BEFORE another remote mutation. If this
	// fails, preserve the remote entry and report failure rather than selecting it.
	if err = remember(added.ID); err != nil {
		return want, err
	}
	recovered, err := inspect(added.ID)
	if err != nil {
		return want, err
	}
	if recovered.Hash != want.Hash {
		return want, ErrMismatch
	}
	if recovered.Status == "waiting_files_selection" {
		if err = c.selectFile(ctx, recovered, want); err != nil {
			return want, err
		}
	}
	want.TorrentID = added.ID
	return want, nil
}
func (c *Client) selectFile(ctx context.Context, d Detail, want FileRequest) error {
	if d.Hash != want.Hash {
		return ErrMismatch
	}
	file, err := individualTorrentFile(d, want)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, "POST", "torrents/selectFiles/"+url.PathEscape(d.ID), url.Values{"files": {strconv.FormatInt(file.ID, 10)}}, nil)
	return err
}
