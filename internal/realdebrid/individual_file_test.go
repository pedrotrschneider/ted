package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/settings"
)

func TestResolveFileRecognizesRARWithoutGuessingMismatchedLinks(t *testing.T) {
	for _, name := range []string{"season.rar", "season.RAR", "episode.mkv"} {
		t.Run(name, func(t *testing.T) {
			want := FileRequest{"123", "T1", strings.Repeat("a", 40), "/episode.mkv", 1000}
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"id":123}`)
				case "/torrents/info/T1":
					fmt.Fprint(w, strings.TrimSuffix(item("T1"), "}")+`,"hash":"`+want.Hash+`","files":[{"id":1,"path":"/episode.mkv","bytes":1000,"selected":1},{"id":2,"path":"/other.mkv","bytes":1000,"selected":1}],"links":["https://real-debrid.com/d/pack"]}`)
				case "/unrestrict/link":
					fmt.Fprintf(w, `{"filename":%q,"filesize":1000,"download":"https://fixture.download.real-debrid.com/private"}`, name)
				default:
					t.Error("unexpected route", r.URL.Path)
				}
			})
			_, err := c.ResolveFile(context.Background(), want)
			expected := ErrArchive
			if name == "episode.mkv" {
				expected = ErrIncomplete
			}
			if !errors.Is(err, expected) {
				t.Fatal("mismatched link shape was accepted or misclassified", err)
			}
		})
	}
}

type individualFixture struct {
	mu                sync.Mutex
	adds, selections  int
	first, individual int
	firstStatus       string
	archive           bool
	empty             bool
	ambiguous         bool
	reuseID           bool
	wrongPath         bool
	entered, release  chan struct{}
	steps             []string
	hash              string
	t                 *testing.T
	s                 *Inventory
}

func newIndividualFixture(t *testing.T) (*individualFixture, settings.Patch, func(settings.Patch)) {
	s, _, actor := newInventoryFixture(t, 0)
	f := &individualFixture{t: t, s: s, hash: strings.Repeat("b", 40)}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	s.Client.base = server.URL + "/"
	return f, settings.Patch{Revision: 1}, func(p settings.Patch) {
		if _, err := s.Client.store.Update(context.Background(), actor, p); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *individualFixture) want() TorrentSelection {
	return TorrentSelection{Hash: f.hash, Filename: "/ep2.mkv", Bytes: 1000}
}

func (f *individualFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	summary := func(id string) map[string]any {
		v := wireTorrent(testTorrent(id))
		v["hash"] = f.hash
		return v
	}
	switch r.URL.Path {
	case "/user":
		fmt.Fprint(w, `{"id":1}`)
	case "/torrents":
		rows := []any{summary("ORIGINAL")}
		if f.empty {
			rows = []any{}
		}
		if f.adds > 0 {
			rows = append(rows, summary("FIRST"))
		}
		if f.adds > 1 {
			rows = append(rows, summary("FILE"))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(len(rows)))
		_ = json.NewEncoder(w).Encode(rows)
	case "/torrents/addMagnet":
		_ = r.ParseForm()
		if r.Form.Get("magnet") != "magnet:?xt=urn:btih:"+f.hash {
			f.t.Error("changed torrent identity")
		}
		f.adds++
		id := "FIRST"
		if f.adds == 2 {
			id = "FILE"
		}
		if f.adds > 2 {
			f.t.Error("duplicate remote add")
		}
		f.steps = append(f.steps, "add_"+id)
		if f.ambiguous {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(201)
		if f.reuseID {
			fmt.Fprint(w, `{"id":"ORIGINAL"}`)
			return
		}
		fmt.Fprintf(w, `{"id":%q}`, id)
	case "/torrents/selectFiles/FIRST":
		var owned bool
		if err := f.s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM rd_addon_torrents WHERE hash=$1 AND owned_id='FIRST' UNION ALL SELECT 1 FROM rd_addon_file_torrents WHERE hash=$1 AND owned_id='FIRST')`, f.hash).Scan(&owned); err != nil || !owned {
			f.t.Error("ownership not durable before selection", err)
		}
		_ = r.ParseForm()
		if r.Form.Get("files") != "2" {
			f.t.Error("first add did not select only the requested file")
		}
		f.selectFile(w, r, false)
	case "/torrents/selectFiles/FILE":
		var owned string
		if err := f.s.pool.QueryRow(r.Context(), `SELECT owned_id FROM rd_addon_file_torrents WHERE hash=$1`, f.hash).Scan(&owned); err != nil || owned != "FILE" {
			f.t.Error("file ownership not durable before selection", err)
		}
		_ = r.ParseForm()
		if r.Form.Get("files") != "2" && r.Form.Get("files") != "3" {
			f.t.Error("not the exact requested file")
		}
		f.selectFile(w, r, true)
	case "/unrestrict/link":
		_ = r.ParseForm()
		switch r.Form.Get("link") {
		case "https://real-debrid.com/d/archive":
			f.steps = append(f.steps, "archive")
			fmt.Fprint(w, `{"filename":"season.rar","filesize":2010,"download":"https://fixture.download.real-debrid.com/private-pack"}`)
		case "https://real-debrid.com/d/ep2":
			fmt.Fprint(w, `{"filename":"ep2.mkv","filesize":1000,"download":"https://fixture.download.real-debrid.com/private-episode"}`)
		case "https://real-debrid.com/d/ep3":
			fmt.Fprint(w, `{"filename":"ep3.mkv","filesize":1000,"download":"https://fixture.download.real-debrid.com/private-episode3"}`)
		default:
			f.t.Error("wrong file link resolved")
		}
	default:
		id := strings.TrimPrefix(r.URL.Path, "/torrents/info/")
		if id != "ORIGINAL" && id != "FIRST" && id != "FILE" {
			f.t.Error("mutated an original or unknown route", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		v := summary(id)
		a, b, n := 0, 0, 0
		if id == "ORIGINAL" {
			a = 1
		}
		if id == "ORIGINAL" && f.archive {
			a, b, n = 1, 1, 1
		}
		selected := f.first
		if id == "FILE" {
			selected = f.individual
		}
		if id != "ORIGINAL" {
			if selected == 0 {
				v["status"] = "waiting_files_selection"
				v["progress"] = 0
			} else if selected == 2 {
				b = 1
			} else if selected == 3 {
				n = 1
			}
		}
		if id == "FIRST" && f.firstStatus != "" {
			v["status"], v["progress"] = f.firstStatus, 50
		}
		filePath := "/ep2.mkv"
		if id != "ORIGINAL" && f.wrongPath {
			filePath = "/other/ep2.mkv"
		}
		v["files"] = []any{map[string]any{"id": 1, "path": "/ep1.mkv", "bytes": 1000, "selected": a}, map[string]any{"id": 2, "path": filePath, "bytes": 1000, "selected": b}, map[string]any{"id": 3, "path": "/ep3.mkv", "bytes": 1000, "selected": n}}
		links := []string{}
		if id == "ORIGINAL" && f.archive {
			links = []string{"https://real-debrid.com/d/archive"}
		} else if id == "ORIGINAL" {
			links = []string{"https://real-debrid.com/d/ep1"}
		} else if selected > 0 {
			links = []string{fmt.Sprintf("https://real-debrid.com/d/ep%d", selected)}
		}
		v["links"] = links
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (f *individualFixture) selectFile(w http.ResponseWriter, r *http.Request, individual bool) {
	f.selections++
	if f.entered != nil && f.selections == 1 {
		close(f.entered)
		f.mu.Unlock()
		select {
		case <-f.release:
		case <-r.Context().Done():
			f.mu.Lock()
			return
		}
		f.mu.Lock()
	}
	id, _ := strconv.Atoi(r.Form.Get("files"))
	if individual {
		f.individual = id
	} else {
		f.first = id
	}
	f.steps = append(f.steps, fmt.Sprintf("select_%d", id))
	w.WriteHeader(204)
}

func TestExactFilePreparationRejectsDifferentPathBeforeSelecting(t *testing.T) {
	f, _, _ := newIndividualFixture(t)
	f.wrongPath, f.archive = true, true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scope := New("fixture-key").CredentialScope()
	if _, err := f.s.pool.Exec(ctx, `INSERT INTO rd_addon_torrents(scope,hash,owned_id) VALUES($1,$2,'ORIGINAL')`, scope, f.hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.ResolveSelection(ctx, f.want()); !errors.Is(err, ErrMismatch) {
		t.Fatal("a basename match replaced the pinned path", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adds != 1 || f.selections != 0 {
		t.Fatal("wrong file was selected", f.adds, f.selections)
	}
}

func TestExactFilePreparationColdStartAndExistingArchive(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(fmt.Sprint("archive=", archive), func(t *testing.T) {
			f, _, _ := newIndividualFixture(t)
			f.archive, f.empty = archive, !archive
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if archive {
				scope := New("fixture-key").CredentialScope()
				if _, err := f.s.pool.Exec(ctx, `INSERT INTO rd_addon_torrents(scope,hash,owned_id) VALUES($1,$2,'ORIGINAL')`, scope, f.hash); err != nil {
					t.Fatal(err)
				}
			}
			w, result, err := f.s.ResolveSelection(ctx, f.want())
			if err != nil || w.TorrentID != "FIRST" || result.Filename != "ep2.mkv" {
				t.Fatal("exact file could not be prepared", err)
			}
			if _, _, err = f.s.ResolveAvailableSelection(ctx, f.want()); err != nil {
				t.Fatal("ready file was not reusable in the background", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			expected := "add_FIRST,select_2"
			if archive {
				expected = "archive," + expected
			}
			if f.adds != 1 || strings.Join(f.steps, ",") != expected {
				t.Fatal("extra add, whole-pack selection or repeated archive attempt", f.adds, f.steps)
			}
		})
	}
}

func TestExactFilePreparationDoesNotClaimAnExistingEntryReturnedByAdd(t *testing.T) {
	f, _, _ := newIndividualFixture(t)
	f.reuseID = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := f.s.ResolveSelection(ctx, f.want()); !errors.Is(err, ErrIncomplete) {
		t.Fatal("existing account entry was claimed as a new owned entry", err)
	}
	var owned string
	if err := f.s.pool.QueryRow(ctx, `SELECT owned_id FROM rd_addon_torrents WHERE hash=$1`, f.hash).Scan(&owned); err != nil || owned != "" {
		t.Fatal("original entry was recorded as owned", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adds != 1 || f.selections != 0 {
		t.Fatal("existing account selection was changed", f.adds, f.selections)
	}
}

func TestExactFilePreparationPreservesOwnedTorrentRejection(t *testing.T) {
	f, _, _ := newIndividualFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := f.s.ResolveSelection(ctx, f.want()); err != nil {
		t.Fatal(err)
	}
	f.firstStatus = "dead"
	want := f.want()
	want.Filename = "/ep3.mkv"
	if _, _, err := f.s.ResolveSelection(ctx, want); !errors.Is(err, ErrRejected) {
		t.Fatal("rejected owned torrent created another add", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adds != 1 {
		t.Fatal("rejection lost its failure ordering", f.adds)
	}
}

func TestExactFilePreparationAddsOnceConcurrentlyAndKeepsEpisodesSeparate(t *testing.T) {
	f, patch, update := newIndividualFixture(t)
	f.entered, f.release = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { _, _, e := f.s.ResolveSelection(ctx, f.want()); done <- e }()
	select {
	case <-f.entered:
	case <-ctx.Done():
		t.Fatal("individual selection not reached")
	}
	go func() { _, _, e := f.s.ResolveSelection(ctx, f.want()); done <- e }()
	close(f.release)
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	w, result, e := f.s.ResolveSelection(ctx, f.want())
	if e != nil || w.TorrentID != "FIRST" || result.Filename != "ep2.mkv" {
		t.Fatal("wrong requested file", e)
	}
	f.mu.Lock()
	adds, steps := f.adds, strings.Join(f.steps, ",")
	f.mu.Unlock()
	if adds != 1 || steps != "add_FIRST,select_2" {
		t.Fatal("wrong mutation order", adds, steps)
	}
	f.firstStatus = "queued"
	next := TorrentSelection{Hash: f.hash, Filename: "/ep3.mkv", Bytes: 1000}
	w, result, e = f.s.ResolveSelection(ctx, next)
	if e != nil || w.TorrentID != "FILE" || result.Filename != "ep3.mkv" {
		t.Fatal("next episode did not get its own selection", e)
	}
	var all, file string
	if e = f.s.pool.QueryRow(ctx, `SELECT a.owned_id,f.owned_id FROM rd_addon_torrents a JOIN rd_addon_file_torrents f USING(scope,hash)`).Scan(&all, &file); e != nil || all != "FIRST" || file != "FILE" {
		t.Fatal("ownership was overwritten", e)
	}
	oldScope := New("fixture-key").CredentialScope()
	key := "rotated-key"
	patch.RDKey = &key
	update(patch)
	w, result, e = f.s.ResolvePinned(ctx, oldScope, w)
	if e != nil || w.TorrentID != "FILE" || result.Filename != "ep3.mkv" {
		t.Fatal("renewal lost exact file after rotation", e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adds != 2 {
		t.Fatal("renewal duplicated fallback")
	}
}

func TestExactFilePreparationRetainsOwnershipOnCancellationBeforeSelection(t *testing.T) {
	for _, second := range []bool{false, true} {
		t.Run(fmt.Sprint("second=", second), func(t *testing.T) {
			f, _, _ := newIndividualFixture(t)
			want := f.want()
			if second {
				if _, _, err := f.s.ResolveSelection(context.Background(), want); err != nil {
					t.Fatal(err)
				}
				want.Filename = "/ep3.mkv"
				f.selections = 0
			}
			f.entered, f.release = make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, _, e := f.s.ResolveSelection(ctx, want); done <- e }()
			select {
			case <-f.entered:
			case <-time.After(10 * time.Second):
				cancel()
				t.Fatal("selection not reached")
			}
			cancel()
			if e := <-done; !errors.Is(e, context.Canceled) {
				t.Fatal("cancellation lost", e)
			}
			ctx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer retryCancel()
			w, _, e := f.s.ResolveSelection(ctx, want)
			expected, adds := "FIRST", 1
			if second {
				expected, adds = "FILE", 2
			}
			if e != nil || w.TorrentID != expected {
				t.Fatal("known add could not recover", e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.adds != adds {
				t.Fatal("cancelled caller caused duplicate add", f.adds)
			}
		})
	}
}

func TestExactFilePreparationDoesNotDuplicateAnAmbiguousAddOrMutateItInBackground(t *testing.T) {
	for _, second := range []bool{false, true} {
		t.Run(fmt.Sprint("second=", second), func(t *testing.T) {
			f, _, _ := newIndividualFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			want := f.want()
			adds, selections := 1, 0
			if second {
				if _, _, err := f.s.ResolveSelection(ctx, want); err != nil {
					t.Fatal(err)
				}
				want.Filename = "/ep3.mkv"
				adds, selections = 2, 1
			}
			f.ambiguous = true
			if _, _, e := f.s.ResolveSelection(ctx, want); !errors.Is(e, ErrUnavailable) {
				t.Fatal("expected uncertain add", e)
			}
			if _, _, e := f.s.ResolveSelection(ctx, want); !errors.Is(e, ErrPending) {
				t.Fatal("uncertain add was retried", e)
			}
			if _, _, e := f.s.ResolveAvailableSelection(ctx, want); !errors.Is(e, ErrPending) {
				t.Fatal("background preparation mutated uncertain entry", e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.adds != adds || f.selections != selections {
				t.Fatal("uncertain add was duplicated or claimed as owned", f.adds, f.selections)
			}
		})
	}
}
