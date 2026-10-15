package realdebrid

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/database"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"path"
	"strings"
	"time"
)

type TorrentSelection struct {
	Hash     string
	Index    *int64
	Filename string
	Bytes    int64
}

// Provider indexes are zero-based; RD file IDs are obtained from its file list.
// Filename and size, when supplied, are independent checks. With no index we
// require a unique filename match, or a single video for a movie-like torrent.
func selectedTorrentFile(d Detail, w TorrentSelection) (File, error) {
	matches := []File{}
	for _, f := range d.Files {
		if !f.Video || f.Bytes <= 0 {
			continue
		}
		if w.Index != nil && f.ID != *w.Index+1 {
			continue
		}
		if w.Filename != "" {
			wanted := strings.TrimPrefix(strings.ReplaceAll(w.Filename, `\`, "/"), "/")
			if strings.Contains(wanted, "/") {
				if strings.TrimPrefix(f.Path, "/") != wanted {
					continue
				}
			} else if path.Base(f.Path) != wanted {
				continue
			}
		}
		if w.Bytes > 0 && f.Bytes != w.Bytes {
			continue
		}
		matches = append(matches, f)
	}
	if len(matches) != 1 {
		return File{}, ErrMismatch
	}
	return matches[0], nil
}
func sourceReady(d Detail) error {
	switch d.Status {
	case "downloaded":
		if d.Progress == 100 {
			return nil
		}
		return ErrPending
	case "error", "dead", "virus", "magnet_error":
		return ErrRejected
	default:
		return ErrPending
	}
}

func (s *Inventory) ResolveSelection(ctx context.Context, w TorrentSelection) (FileRequest, Resolved, error) {
	return s.resolveSelection(ctx, w, true)
}

// Background preparation must never add a torrent or change file selection.
func (s *Inventory) ResolveAvailableSelection(ctx context.Context, w TorrentSelection) (FileRequest, Resolved, error) {
	return s.resolveSelection(ctx, w, false)
}
func (s *Inventory) resolveSelection(ctx context.Context, w TorrentSelection, allowAdd bool) (FileRequest, Resolved, error) {
	if !hashPattern.MatchString(w.Hash) || w.Index != nil && (*w.Index < 0 || *w.Index >= 20000) {
		return FileRequest{}, Resolved{}, ErrInvalid
	}
	c, e := s.Snapshot(ctx)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	if !c.Configured() {
		return FileRequest{}, Resolved{}, ErrNotConfigured
	}
	scope := c.CredentialScope()
	conn, unlock, e := s.lockSource(ctx, scope, w.Hash)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	defer unlock()
	account, e := c.AccountID(ctx)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	if _, e = conn.Exec(ctx, `INSERT INTO rd_addon_torrents(scope,hash) VALUES($1,$2) ON CONFLICT DO NOTHING`, scope, w.Hash); e != nil {
		return FileRequest{}, Resolved{}, e
	}
	var owned string
	var attempted bool
	if e = conn.QueryRow(ctx, `SELECT owned_id,add_attempted FROM rd_addon_torrents WHERE scope=$1 AND hash=$2`, scope, w.Hash).Scan(&owned, &attempted); e != nil {
		return FileRequest{}, Resolved{}, e
	}
	finish := func(d Detail) (FileRequest, Resolved, error) {
		if d.Hash != w.Hash {
			return FileRequest{}, Resolved{}, ErrMismatch
		}
		if e := sourceReady(d); e != nil {
			return FileRequest{}, Resolved{}, e
		}
		f, e := selectedTorrentFile(d, w)
		if e != nil {
			return FileRequest{}, Resolved{}, e
		}
		if !f.Selected {
			return FileRequest{}, Resolved{}, ErrPending
		}
		want := FileRequest{AccountID: account, TorrentID: d.ID, Hash: w.Hash, Path: f.Path, Bytes: f.Bytes}
		resolved, e := c.ResolveFile(ctx, want)
		if e == nil {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return want, resolved, err
			}
			defer database.Rollback(tx)
			data, _ := json.Marshal(d)
			if _, e = tx.Exec(ctx, `UPDATE rd_addon_torrents SET detail=$3,verified_at=now() WHERE scope=$1 AND hash=$2`, scope, w.Hash, data); e != nil {
				return want, resolved, e
			}
			if _, e = s.queue.InsertTx(ctx, tx, inventoryArgs{}, inventoryOpts("rd_inventory")); e != nil {
				return want, resolved, e
			}
			e = tx.Commit(ctx)
		}
		return want, resolved, e
	}
	if owned != "" {
		d, err := c.Torrent(ctx, owned)
		if err == nil {
			if d.Hash != w.Hash {
				return FileRequest{}, Resolved{}, ErrMismatch
			}
			if d.Status == "waiting_files_selection" {
				if !allowAdd {
					return FileRequest{}, Resolved{}, ErrPending
				}
				if e = c.selectAll(ctx, owned); e != nil {
					return FileRequest{}, Resolved{}, e
				}
				d, e = c.Torrent(ctx, owned)
				if e != nil {
					return FileRequest{}, Resolved{}, e
				}
			}
			return finish(d)
		}
		if !errors.Is(err, ErrNotFound) {
			return FileRequest{}, Resolved{}, err
		}
		// Confirmed removal permits recovery; a transport failure does not.
		if _, e = conn.Exec(ctx, `UPDATE rd_addon_torrents SET owned_id='',add_attempted=false WHERE scope=$1 AND hash=$2`, scope, w.Hash); e != nil {
			return FileRequest{}, Resolved{}, e
		}
		attempted = false
	}
	ids := []string{}
	rows, e := conn.Query(ctx, `SELECT id FROM rd_inventory_torrents WHERE scope=$1 AND hash=$2 ORDER BY ordinal`, scope, w.Hash)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	if e = rows.Err(); e != nil {
		return FileRequest{}, Resolved{}, e
	}
	checked := map[string]bool{}
	pending := false
	inspect := func(id string) (FileRequest, Resolved, bool, error) {
		if checked[id] {
			return FileRequest{}, Resolved{}, false, nil
		}
		checked[id] = true
		d, e := c.Torrent(ctx, id)
		if errors.Is(e, ErrNotFound) {
			return FileRequest{}, Resolved{}, false, nil
		}
		if e != nil {
			return FileRequest{}, Resolved{}, false, e
		}
		if d.Hash != w.Hash {
			return FileRequest{}, Resolved{}, false, ErrMismatch
		}
		f, e := selectedTorrentFile(d, w)
		if e != nil {
			if len(d.Files) == 0 && sourceReady(d) == ErrPending {
				pending = true
			}
			return FileRequest{}, Resolved{}, false, nil
		}
		if !f.Selected {
			if d.Status != "downloaded" {
				pending = true
			}
			return FileRequest{}, Resolved{}, false, nil
		}
		want, res, e := finish(d)
		if errors.Is(e, ErrPending) {
			pending = true
			return want, res, false, nil
		}
		return want, res, e == nil, e
	}
	for _, id := range ids {
		want, res, ok, e := inspect(id)
		if e != nil || ok {
			return want, res, e
		}
	}
	if !allowAdd {
		return FileRequest{}, Resolved{}, ErrPending
	}
	// Absence in a cache is not proof of absence in RD. Before adding, reconcile
	// a complete live listing, including an earlier ambiguous add response.
	fresh, _, e := c.scanInventory(ctx, nil, true)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	for _, t := range fresh {
		if t.Hash == w.Hash {
			want, res, ok, e := inspect(t.ID)
			if e != nil || ok {
				return want, res, e
			}
		}
	}
	if pending || attempted {
		return FileRequest{}, Resolved{}, ErrPending
	}
	if _, e = conn.Exec(ctx, `UPDATE rd_addon_torrents SET add_attempted=true,updated_at=now() WHERE scope=$1 AND hash=$2`, scope, w.Hash); e != nil {
		return FileRequest{}, Resolved{}, e
	}
	var added struct {
		ID string `json:"id"`
	}
	_, e = c.request(ctx, "POST", "torrents/addMagnet", url.Values{"magnet": {"magnet:?xt=urn:btih:" + w.Hash}}, &added)
	if e != nil {
		if errors.Is(e, ErrInfringing) || errors.Is(e, ErrRejected) || errors.Is(e, ErrCredentials) || errors.Is(e, ErrLimited) {
			_, _ = conn.Exec(ctx, `UPDATE rd_addon_torrents SET add_attempted=false WHERE scope=$1 AND hash=$2`, scope, w.Hash)
		}
		return FileRequest{}, Resolved{}, e
	}
	if !idPattern.MatchString(added.ID) {
		return FileRequest{}, Resolved{}, ErrIncomplete
	}
	// Retain known ownership even if the caller disconnects immediately after RD
	// returns. This independent commit must precede selectFiles(all).
	save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, e = conn.Exec(save, `UPDATE rd_addon_torrents SET owned_id=$3,updated_at=now() WHERE scope=$1 AND hash=$2`, scope, w.Hash, added.ID)
	cancel()
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	d, e := c.Torrent(ctx, added.ID)
	if e != nil {
		return FileRequest{}, Resolved{}, e
	}
	if d.Hash != w.Hash {
		return FileRequest{}, Resolved{}, ErrMismatch
	}
	if d.Status == "waiting_files_selection" {
		if e = c.selectAll(ctx, added.ID); e != nil {
			return FileRequest{}, Resolved{}, e
		}
		d, e = c.Torrent(ctx, added.ID)
		if e != nil {
			return FileRequest{}, Resolved{}, e
		}
	}
	return finish(d)
}

func (s *Inventory) ResolvePinned(ctx context.Context, scope string, w FileRequest) (FileRequest, Resolved, error) {
	c, e := s.Snapshot(ctx)
	if e != nil {
		return w, Resolved{}, e
	}
	if !c.Configured() {
		return w, Resolved{}, ErrNotConfigured
	}
	if c.CredentialScope() != scope {
		account, e := c.AccountID(ctx)
		if e != nil {
			return w, Resolved{}, e
		}
		if account != w.AccountID {
			return w, Resolved{}, ErrMismatch
		}
	}
	conn, unlock, e := s.lockSource(ctx, c.CredentialScope(), w.Hash)
	if e != nil {
		return w, Resolved{}, e
	}
	defer unlock()
	var owned string
	e = conn.QueryRow(ctx, `SELECT owned_id FROM rd_addon_torrents WHERE scope=$1 AND hash=$2`, scope, w.Hash).Scan(&owned)
	if e != nil {
		return w, Resolved{}, e
	}
	currentScope := c.CredentialScope()
	if currentScope != scope {
		if _, e = conn.Exec(ctx, `INSERT INTO rd_addon_torrents(scope,hash,owned_id,add_attempted,detail,verified_at) SELECT $3,hash,owned_id,add_attempted,detail,verified_at FROM rd_addon_torrents WHERE scope=$1 AND hash=$2 ON CONFLICT DO NOTHING`, scope, w.Hash, currentScope); e != nil {
			return w, Resolved{}, e
		}
		scope = currentScope
	}
	prepared, e := c.PrepareFile(ctx, w, owned, func(id string) error {
		_, e := conn.Exec(ctx, `UPDATE rd_addon_torrents SET owned_id=$3,updated_at=now() WHERE scope=$1 AND hash=$2`, scope, w.Hash, id)
		return e
	})
	if e != nil {
		return w, Resolved{}, e
	}
	result, e := c.ResolveFile(ctx, prepared)
	return prepared, result, e
}

func (s *Inventory) InAccount(ctx context.Context, w TorrentSelection) (bool, error) {
	values, _, e := s.ReadyFor(ctx, []TorrentSelection{w})
	if e != nil {
		return false, e
	}
	return values[0], nil
}

func (s *Inventory) ReadyFor(ctx context.Context, selections []TorrentSelection) ([]bool, string, error) {
	values := make([]bool, len(selections))
	c, e := s.Snapshot(ctx)
	if e != nil {
		return values, "", e
	}
	if !c.Configured() {
		return values, "", nil
	}
	hashes := []string{}
	for _, w := range selections {
		hashes = append(hashes, w.Hash)
	}
	rows, e := s.pool.Query(ctx, `SELECT detail FROM rd_inventory_torrents WHERE scope=$1 AND hash=ANY($2::text[]) AND detail_generation=generation AND detail IS NOT NULL UNION ALL SELECT detail FROM rd_addon_torrents WHERE scope=$1 AND hash=ANY($2::text[]) AND detail IS NOT NULL AND verified_at>now()-interval '1 minute'`, c.CredentialScope(), hashes)
	if e != nil {
		return values, "", e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var d Detail
		if e = rows.Scan(&raw); e != nil {
			return values, "", e
		}
		if json.Unmarshal(raw, &d) != nil {
			return values, "", ErrIncomplete
		}
		if sourceReady(d) != nil {
			continue
		}
		for i, w := range selections {
			if w.Hash != d.Hash {
				continue
			}
			f, e := selectedTorrentFile(d, w)
			if e == nil && f.Selected {
				values[i] = true
			}
		}
	}
	return values, c.CredentialScope(), rows.Err()
}

// Waiting contenders release their pool connection. The winning workflow uses
// its locked connection for durable ownership, avoiding pool starvation when
// several device downloads prepare different torrents concurrently.
func (s *Inventory) lockSource(ctx context.Context, scope, hash string) (*pgxpool.Conn, func(), error) {
	for {
		conn, e := s.pool.Acquire(ctx)
		if e != nil {
			return nil, nil, e
		}
		var locked bool
		e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,84051201))`, scope+":"+hash).Scan(&locked)
		if e != nil {
			conn.Release()
			return nil, nil, e
		}
		if locked {
			return conn, func() {
				op, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, e := conn.Exec(op, `SELECT pg_advisory_unlock(hashtextextended($1,84051201))`, scope+":"+hash); e != nil {
					_ = conn.Conn().Close(op)
				}
				conn.Release()
			}, nil
		}
		conn.Release()
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}
