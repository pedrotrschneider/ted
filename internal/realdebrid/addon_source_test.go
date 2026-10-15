package realdebrid

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/settings"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddonTorrentSelectsAllOnceAndPreservesPartialOriginal(t *testing.T) {
	s, _, actor := newInventoryFixture(t, 0)
	inspectionPool := s.pool
	cfg := s.pool.Config()
	cfg.MaxConns = 1
	tiny, e := pgxpool.NewWithConfig(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer tiny.Close()
	s.pool = tiny
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hash := strings.Repeat("a", 40)
	var mu sync.Mutex
	added := 0
	selected := false
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			w.Write([]byte(`{"id":1}`))
			return
		}
		if r.URL.Path == "/torrents/addMagnet" {
			_ = r.ParseForm()
			if r.Form.Get("magnet") != "magnet:?xt=urn:btih:"+hash {
				t.Error("wrong torrent added")
			}
			mu.Lock()
			added++
			mu.Unlock()
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"OWN"}`))
			return
		}
		if r.URL.Path == "/torrents/selectFiles/OWN" {
			var owned string
			if e := inspectionPool.QueryRow(ctx, `SELECT owned_id FROM rd_addon_torrents WHERE hash=$1`, hash).Scan(&owned); e != nil || owned != "OWN" {
				t.Error("ownership was not committed before selecting files", e)
			}
			_ = r.ParseForm()
			if r.Form.Get("files") != "all" {
				t.Error("TED did not select all files")
			}
			close(entered)
			<-release
			mu.Lock()
			selected = true
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		mu.Lock()
		count, all := added, selected
		mu.Unlock()
		torrent := func(id string) map[string]any { v := wireTorrent(testTorrent(id)); v["hash"] = hash; return v }
		if r.URL.Path == "/torrents" {
			rows := []any{torrent("ORIGINAL")}
			if count > 0 {
				rows = append([]any{torrent("OWN")}, rows...)
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			w.Header().Set("X-Total-Count", strconv.Itoa(len(rows)))
			_ = json.NewEncoder(w).Encode(rows[min(offset, len(rows)):min(offset+limit, len(rows))])
			return
		}
		if strings.HasPrefix(r.URL.Path, "/torrents/info/") {
			id := strings.TrimPrefix(r.URL.Path, "/torrents/info/")
			if id != "OWN" && id != "ORIGINAL" {
				w.WriteHeader(404)
				return
			}
			v := torrent(id)
			if id == "OWN" && !all {
				v["status"] = "waiting_files_selection"
				v["progress"] = 0
			}
			select2 := 0
			if id == "OWN" && all {
				select2 = 1
			}
			v["files"] = []any{map[string]any{"id": 1, "path": "/ep1.mkv", "bytes": 1000, "selected": 1}, map[string]any{"id": 2, "path": "/ep2.mkv", "bytes": 1000, "selected": select2}}
			links := []string{"https://real-debrid.com/d/" + id + "-1"}
			if select2 == 1 {
				links = append(links, "https://real-debrid.com/d/"+id+"-2")
			}
			v["links"] = links
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		if r.URL.Path == "/unrestrict/link" {
			_ = r.ParseForm()
			if !strings.HasSuffix(r.Form.Get("link"), "OWN-2") {
				t.Error("wrong selected link")
			}
			w.Write([]byte(`{"download":"https://fixture.download.real-debrid.com/media","filename":"ep2.mkv","filesize":1000}`))
			return
		}
		t.Error("unexpected provider mutation or route", r.URL.Path)
		w.WriteHeader(400)
	}))
	defer server.Close()
	s.Client.base = server.URL + "/"
	index := int64(1)
	want := TorrentSelection{Hash: hash, Index: &index, Filename: "ep2.mkv", Bytes: 1000}
	done := make(chan error, 2)
	go func() { _, _, e := s.ResolveSelection(ctx, want); done <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("preparation starved its own database connection")
	}
	go func() { _, _, e := s.ResolveSelection(ctx, want); done <- e }()
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	mu.Lock()
	count := added
	mu.Unlock()
	if count != 1 {
		t.Fatal("concurrent preparation added duplicates", count)
	}
	owned, res, e := s.ResolveSelection(ctx, want)
	if e != nil || owned.TorrentID != "OWN" || res.Filename != "ep2.mkv" {
		t.Fatal("durable owned torrent not reused", e)
	}
	flags, _, e := s.ReadyFor(ctx, []TorrentSelection{want, {Hash: hash, Filename: "other.mkv"}})
	if e != nil || !flags[0] || flags[1] {
		t.Fatal("verified account badge wrong", flags, e)
	}
	oldScope := New("fixture-key").CredentialScope()
	key := "rotated-fixture-key"
	if _, e = s.Client.store.Update(ctx, actor, settings.Patch{Revision: 1, RDKey: &key}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.ResolvePinned(ctx, oldScope, owned); e != nil {
		t.Fatal("same-account credential rotation lost the pinned file", e)
	}
	var rotatedOwned string
	if e = s.pool.QueryRow(ctx, `SELECT owned_id FROM rd_addon_torrents WHERE scope=$1 AND hash=$2`, New(key).CredentialScope(), hash).Scan(&rotatedOwned); e != nil || rotatedOwned != "OWN" {
		t.Fatal("ownership did not survive key rotation", e)
	}
}

func TestRDStructuredInfringementAndProviderRejection(t *testing.T) {
	for _, tc := range []struct {
		code int
		want error
	}{{35, ErrInfringing}, {30, ErrRejected}, {34, ErrLimited}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "private body never returned", "error_code": tc.code})
		}))
		c := New("fixture")
		c.base = server.URL + "/"
		_, e := c.request(context.Background(), "POST", "torrents/addMagnet", nil, nil)
		server.Close()
		if !errors.Is(e, tc.want) || strings.Contains(e.Error(), "private") {
			t.Fatal("provider failure was lost or leaked", e)
		}
	}
	d := Detail{Torrent: Torrent{Status: "queued"}}
	if !errors.Is(sourceReady(d), ErrPending) {
		t.Fatal("queued torrent accepted as media")
	}
	d.Status = "dead"
	if !errors.Is(sourceReady(d), ErrRejected) {
		t.Fatal("dead torrent accepted as media")
	}
}

func TestTorrentFileIdentityRejectsAmbiguousNamesAndMismatchedIndex(t *testing.T) {
	d := Detail{Files: []File{{ID: 1, Path: "/a/ep.mkv", Bytes: 1000, Video: true, Selected: true}, {ID: 2, Path: "/b/ep.mkv", Bytes: 2000, Video: true, Selected: true}}}
	if _, e := selectedTorrentFile(d, TorrentSelection{Filename: "ep.mkv"}); !errors.Is(e, ErrMismatch) {
		t.Fatal("ambiguous pack file guessed", e)
	}
	i := int64(0)
	if _, e := selectedTorrentFile(d, TorrentSelection{Index: &i, Filename: "ep.mkv", Bytes: 2000}); !errors.Is(e, ErrMismatch) {
		t.Fatal("index and size disagreement ignored", e)
	}
	f, e := selectedTorrentFile(d, TorrentSelection{Filename: "b/ep.mkv", Bytes: 2000})
	if e != nil || f.ID != 2 {
		t.Fatal("exact path unavailable", e)
	}
}

func TestBackgroundSelectionNeverAddsOrSelectsFiles(t *testing.T) {
	s, _, _ := newInventoryFixture(t, 0)
	ctx := context.Background()
	hash := strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			w.Write([]byte(`{"id":1}`))
		case "/torrents/info/OWN":
			v := wireTorrent(testTorrent("OWN"))
			v["hash"] = hash
			v["status"] = "waiting_files_selection"
			v["files"] = []any{}
			v["links"] = []string{}
			json.NewEncoder(w).Encode(v)
		default:
			t.Error("background preparation made a provider write or library scan", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	s.Client.base = server.URL + "/"
	want := TorrentSelection{Hash: hash, Filename: "episode.mkv"}
	if _, _, e := s.ResolveAvailableSelection(ctx, want); !errors.Is(e, ErrPending) {
		t.Fatal("missing torrent should remain unavailable", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE rd_addon_torrents SET owned_id='OWN' WHERE hash=$1`, hash); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.ResolveAvailableSelection(ctx, want); !errors.Is(e, ErrPending) {
		t.Fatal("unselected torrent should remain unavailable", e)
	}
}
