package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func individualTorrentFile(d Detail, want FileRequest) (File, error) {
	for _, file := range d.Files {
		if file.Path == want.Path && file.Bytes == want.Bytes && file.Video {
			return file, nil
		}
	}
	return File{}, ErrMismatch
}

// Called only after the ordinary all-files resolution confirmed a RAR pack.
// The caller holds lockSource. Existing entries are read-only; only a separately
// journaled TED-owned entry may receive the individual-file selection.
func (s *Inventory) resolveIndividualFile(ctx context.Context, conn *pgxpool.Conn, c *Client, scope string, want FileRequest, allowAdd bool) (FileRequest, Resolved, error) {
	if _, err := conn.Exec(ctx, `INSERT INTO rd_addon_file_torrents(scope,hash,path,bytes) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope, want.Hash, want.Path, want.Bytes); err != nil {
		return want, Resolved{}, err
	}
	var owned string
	var attempted bool
	if err := conn.QueryRow(ctx, `SELECT owned_id,add_attempted FROM rd_addon_file_torrents WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes).Scan(&owned, &attempted); err != nil {
		return want, Resolved{}, err
	}
	finish := func(d Detail) (FileRequest, Resolved, error) {
		if d.Hash != want.Hash {
			return want, Resolved{}, ErrMismatch
		}
		file, err := individualTorrentFile(d, want)
		if err != nil {
			return want, Resolved{}, err
		}
		if err = sourceReady(d); err != nil {
			return want, Resolved{}, err
		}
		if !file.Selected {
			return want, Resolved{}, ErrPending
		}
		pinned := want
		pinned.TorrentID = d.ID
		result, err := c.ResolveFile(ctx, pinned)
		if err != nil {
			return pinned, Resolved{}, err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return pinned, Resolved{}, err
		}
		defer database.Rollback(tx)
		raw, _ := json.Marshal(d)
		if _, err = tx.Exec(ctx, `UPDATE rd_addon_file_torrents SET detail=$5,verified_at=now(),updated_at=now() WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes, raw); err == nil {
			_, err = s.queue.InsertTx(ctx, tx, inventoryArgs{}, inventoryOpts("rd_inventory"))
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		return pinned, result, err
	}
	prepareOwned := func(d Detail) (FileRequest, Resolved, error) {
		if d.Hash != want.Hash {
			return want, Resolved{}, ErrMismatch
		}
		file, err := individualTorrentFile(d, want)
		if err != nil {
			return want, Resolved{}, err
		}
		if d.Status == "waiting_files_selection" {
			if !allowAdd {
				return want, Resolved{}, ErrPending
			}
			_, err = c.request(ctx, "POST", "torrents/selectFiles/"+url.PathEscape(d.ID), url.Values{"files": {strconv.FormatInt(file.ID, 10)}}, nil)
			if err != nil {
				return want, Resolved{}, err
			}
			d, err = c.Torrent(ctx, d.ID)
			if err != nil {
				return want, Resolved{}, err
			}
		}
		return finish(d)
	}
	if owned != "" {
		d, err := c.Torrent(ctx, owned)
		if err == nil {
			return prepareOwned(d)
		}
		if !errors.Is(err, ErrNotFound) {
			return want, Resolved{}, err
		}
		// Confirmed deletion allows recovery. Transport failure never does.
		if _, err = conn.Exec(ctx, `UPDATE rd_addon_file_torrents SET owned_id='',add_attempted=false,detail=NULL,verified_at=NULL WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes); err != nil {
			return want, Resolved{}, err
		}
		attempted = false
	}
	if !allowAdd {
		return want, Resolved{}, ErrPending
	}
	// Reconcile a complete current account listing before a possible add,
	// including after an earlier uncertain response. Never reselect originals.
	fresh, _, err := c.scanInventory(ctx, nil, true)
	if err != nil {
		return want, Resolved{}, err
	}
	seen := map[string]bool{}
	pending := false
	for _, torrent := range fresh {
		seen[torrent.ID] = true
		if torrent.Hash != want.Hash || torrent.ID == want.TorrentID {
			continue
		}
		d, err := c.Torrent(ctx, torrent.ID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return want, Resolved{}, err
		}
		if d.Hash != want.Hash {
			return want, Resolved{}, ErrMismatch
		}
		file, err := individualTorrentFile(d, want)
		if err != nil || !file.Selected {
			if sourceReady(d) == ErrPending {
				pending = true
			}
			continue
		}
		pinned, result, err := finish(d)
		if errors.Is(err, ErrArchive) {
			continue
		}
		if errors.Is(err, ErrPending) {
			pending = true
			continue
		}
		return pinned, result, err
	}
	if pending || attempted {
		return want, Resolved{}, ErrPending
	}
	if _, err = conn.Exec(ctx, `UPDATE rd_addon_file_torrents SET add_attempted=true,updated_at=now() WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes); err != nil {
		return want, Resolved{}, err
	}
	var added struct {
		ID string `json:"id"`
	}
	_, err = c.request(ctx, "POST", "torrents/addMagnet", url.Values{"magnet": {"magnet:?xt=urn:btih:" + want.Hash}}, &added)
	if err != nil {
		if errors.Is(err, ErrInfringing) || errors.Is(err, ErrRejected) || errors.Is(err, ErrCredentials) || errors.Is(err, ErrLimited) {
			_, _ = conn.Exec(ctx, `UPDATE rd_addon_file_torrents SET add_attempted=false WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes)
		}
		return want, Resolved{}, err
	}
	if !idPattern.MatchString(added.ID) || seen[added.ID] || added.ID == want.TorrentID {
		return want, Resolved{}, ErrIncomplete
	}
	// Even cancellation cannot lose known ownership between add and selection.
	save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = conn.Exec(save, `UPDATE rd_addon_file_torrents SET owned_id=$5,updated_at=now() WHERE scope=$1 AND hash=$2 AND path=$3 AND bytes=$4`, scope, want.Hash, want.Path, want.Bytes, added.ID)
	cancel()
	if err != nil {
		return want, Resolved{}, err
	}
	d, err := c.Torrent(ctx, added.ID)
	if err != nil {
		return want, Resolved{}, err
	}
	return prepareOwned(d)
}
