package realdebrid

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/settings"
	"entertainmentdistrict/internal/testdb"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type inventoryFixture struct {
	mu               sync.Mutex
	rows             []Torrent
	fail             bool
	detailBytes      int64
	limits           []int
	entered, release chan struct{}
}

func testTorrent(id string) Torrent {
	sum := sha1.Sum([]byte(id))
	return Torrent{ID: id, Name: id + ".mkv", Hash: hex.EncodeToString(sum[:]), Bytes: 1000, Status: "downloaded", Progress: 100, Added: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}
}
func wireTorrent(t Torrent) map[string]any {
	return map[string]any{"id": t.ID, "filename": t.Name, "hash": t.Hash, "bytes": t.Bytes, "status": t.Status, "progress": t.Progress, "added": t.Added.Format(time.RFC3339)}
}
func (f *inventoryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/user" {
		f.mu.Lock()
		entered, release := f.entered, f.release
		f.mu.Unlock()
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write([]byte(`{"id":1}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		w.WriteHeader(503)
		return
	}
	if r.URL.Path == "/torrents" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		offset := (page - 1) * limit
		if page < 1 || r.URL.Query().Has("offset") {
			w.WriteHeader(204)
			return
		}
		f.limits = append(f.limits, limit)
		w.Header().Set("X-Total-Count", strconv.Itoa(len(f.rows)))
		out := []any{}
		for _, t := range f.rows[min(offset, len(f.rows)):min(offset+limit, len(f.rows))] {
			out = append(out, wireTorrent(t))
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	for _, t := range f.rows {
		if r.URL.Path == "/torrents/info/"+t.ID {
			out := wireTorrent(t)
			if f.detailBytes > 0 {
				out["bytes"] = f.detailBytes
			}
			out["files"] = []any{map[string]any{"id": 1, "path": "/" + t.Name, "bytes": 1000, "selected": 1}}
			out["links"] = []string{"https://real-debrid.com/d/fixture"}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
	}
	w.WriteHeader(404)
}
func newInventoryFixture(t *testing.T, n int) (*Inventory, *inventoryFixture, accounts.Identity) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.New(t)
	if e := database.Migrate(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	var actor accounts.Identity
	if e = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('cache_admin','Cache admin','unused','admin') RETURNING id,session_version`).Scan(&actor.ID, &actor.Version); e != nil {
		t.Fatal(e)
	}
	store := settings.New(pool)
	if e = store.Initialize(ctx, settings.Credentials{RDKey: "fixture-key"}); e != nil {
		t.Fatal(e)
	}
	var schema string
	if e = pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		t.Fatal(e)
	}
	migrate, e := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = migrate.Migrate(ctx, rivermigrate.DirectionUp, nil); e != nil {
		t.Fatal(e)
	}
	provider := &inventoryFixture{}
	for i := 0; i < n; i++ {
		provider.rows = append(provider.rows, testTorrent(fmt.Sprintf("T%04d", i)))
	}
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	c := NewStored(store)
	c.base = server.URL + "/"
	s, e := NewInventory(pool, c, nil)
	if e != nil {
		t.Fatal(e)
	}
	return s, provider, actor
}
func TestInventoryIncrementalTailRemovalDailyReconciliationAndFailedRead(t *testing.T) {
	s, p, _ := newInventoryFixture(t, 20)
	ctx := context.Background()
	if e := s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	first, e := s.List(ctx, 1)
	if e != nil || len(first.Torrents) != 20 {
		t.Fatal("initial cache incomplete", e)
	}
	p.mu.Lock()
	p.rows = append(p.rows[:18], p.rows[19:]...)
	p.limits = nil
	p.mu.Unlock()
	if e = s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	page, e := s.List(ctx, 1)
	if e != nil || len(page.Torrents) != 19 {
		t.Fatal("unchanged head hid tail deletion", e)
	}
	p.mu.Lock()
	limits := append([]int{}, p.limits...)
	p.rows[15].Status = "dead"
	p.mu.Unlock()
	if len(limits) < 3 || limits[0] != 10 || limits[1] != 100 {
		t.Fatal("incremental window did not expand", limits)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE rd_inventory_state SET full_scan_at=now()-interval '25 hours'`); e != nil {
		t.Fatal(e)
	}
	if e = s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	page, e = s.List(ctx, 1)
	if e != nil || page.Torrents[15].Status != "dead" {
		t.Fatal("daily full scan missed a same-count tail change", e)
	}
	p.mu.Lock()
	p.fail = true
	p.mu.Unlock()
	if e = s.reconcile(ctx, true); !errors.Is(e, ErrUnavailable) {
		t.Fatal("failed read became success", e)
	}
	retained, e := s.List(ctx, 1)
	if e != nil || len(retained.Torrents) != 19 || retained.Torrents[15].Status != "dead" {
		t.Fatal("failed rebuild discarded complete cache", e)
	}
}
func TestInventoryFileDetailsReadinessAndKeyRotationFence(t *testing.T) {
	s, p, actor := newInventoryFixture(t, 1)
	ctx := context.Background()
	if e := s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Torrent(ctx, "T0000"); !errors.Is(e, ErrCachePending) {
		t.Fatal("unfetched files appeared ready", e)
	}
	var gen int64
	if e := s.pool.QueryRow(ctx, `SELECT generation FROM rd_inventory_torrents LIMIT 1`).Scan(&gen); e != nil {
		t.Fatal(e)
	}
	worker := inventoryFileWorker{s: s}
	if e := worker.Work(ctx, &river.Job[inventoryFileArgs]{Args: inventoryFileArgs{Scope: New("fixture-key").CredentialScope(), ID: "T0000", Generation: gen}}); e != nil {
		t.Fatal(e)
	}
	d, e := s.Torrent(ctx, "T0000")
	if e != nil || len(d.Files) != 1 {
		t.Fatal("verified files missing", e)
	}
	yes, e := s.InAccount(ctx, TorrentSelection{Hash: d.Hash, Filename: d.Name})
	if e != nil || !yes {
		t.Fatal("ready selected file not recognized", e)
	}
	no, e := s.InAccount(ctx, TorrentSelection{Hash: d.Hash, Filename: "different.mkv"})
	if e != nil || no {
		t.Fatal("hash presence was used as file readiness", e)
	}
	status, e := s.Status(ctx)
	if e != nil || status.FullAt == nil || status.Pending != 0 {
		t.Fatal("full rebuild did not settle", status, e)
	}
	p.mu.Lock()
	p.entered = make(chan struct{}, 1)
	p.release = make(chan struct{})
	entered, release := p.entered, p.release
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- s.reconcile(ctx, true) }()
	<-entered
	key := "replacement-key"
	if _, e = s.Client.store.Update(ctx, actor, settings.Patch{Revision: 1, RDKey: &key}); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; !errors.Is(e, ErrMismatch) {
		t.Fatal("stale credential published a cache", e)
	}
	if _, e = s.List(ctx, 1); !errors.Is(e, ErrCachePending) {
		t.Fatal("new credential exposed old account inventory", e)
	}
}

func TestInventoryDetailBytesDoNotInvalidateUnchangedListing(t *testing.T) {
	s, p, _ := newInventoryFixture(t, 20)
	ctx := context.Background()
	p.detailBytes = 700
	if e := s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	var gen int64
	if e := s.pool.QueryRow(ctx, `SELECT generation FROM rd_inventory_torrents WHERE id='T0000'`).Scan(&gen); e != nil {
		t.Fatal(e)
	}
	w := inventoryFileWorker{s: s}
	if e := w.Work(ctx, &river.Job[inventoryFileArgs]{Args: inventoryFileArgs{Scope: New("fixture-key").CredentialScope(), ID: "T0000", Generation: gen}}); e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	p.limits = nil
	p.mu.Unlock()
	if e := s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	limits := append([]int{}, p.limits...)
	p.mu.Unlock()
	if len(limits) != 2 || limits[0] != 10 || limits[1] != 10 {
		t.Fatal("unchanged head expanded after detail fetch", limits)
	}
	var current, detailGen, listingBytes, detailBytes int64
	if e := s.pool.QueryRow(ctx, `SELECT generation,detail_generation,(summary->>'bytes')::bigint,(detail->>'bytes')::bigint FROM rd_inventory_torrents WHERE id='T0000'`).Scan(&current, &detailGen, &listingBytes, &detailBytes); e != nil {
		t.Fatal(e)
	}
	if current != gen || detailGen != gen || listingBytes != 1000 || detailBytes != 700 {
		t.Fatal("detail contaminated listing snapshot", current, detailGen, listingBytes, detailBytes)
	}
	p.mu.Lock()
	p.rows[0].Bytes = 1100
	p.mu.Unlock()
	if e := s.reconcile(ctx, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Torrent(ctx, "T0000"); !errors.Is(e, ErrCachePending) {
		t.Fatal("real listing change did not invalidate detail", e)
	}
}
func TestInventoryUnchangedOverlapCannotProveAChangedTotal(t *testing.T) {
	old := []Torrent{}
	for i := 0; i < 1000; i++ {
		old = append(old, testTorrent(fmt.Sprintf("T%d", i)))
	}
	if _, ok := mergeInventoryPrefix(old, old[:10], 999); ok {
		t.Fatal("deleted tail accepted")
	}
	if _, ok := mergeInventoryPrefix(old, old[:10], 1000); !ok {
		t.Fatal("unchanged bounded check rejected")
	}
	head := append([]Torrent{testTorrent("new")}, old[:99]...)
	merged, ok := mergeInventoryPrefix(old, head, 1001)
	if !ok || len(merged) != 1001 || merged[1].ID != old[0].ID {
		t.Fatal("new head could not reuse unchanged tail")
	}
}

func TestInventoryPagesUseFixedSizeAndDoNotTreatOffset204AsEmpty(t *testing.T) {
	p := &inventoryFixture{}
	for i := 0; i < 2100; i++ {
		p.rows = append(p.rows, testTorrent(fmt.Sprintf("T%d", i)))
	}
	server := httptest.NewServer(p)
	defer server.Close()
	c := New("fixture")
	c.base = server.URL + "/"
	values, total, e := c.inventoryPrefix(context.Background(), 1600)
	if e != nil || total != 2100 || len(values) != 2000 || values[1000].ID != "T1000" {
		t.Fatal("fixed-size pagination skipped or repeated a window", len(values), total, e)
	}
	full, complete, e := c.scanInventory(context.Background(), nil, true)
	if e != nil || !complete || len(full) != 2100 {
		t.Fatal("full pagination was incomplete", len(full), e)
	}
}
