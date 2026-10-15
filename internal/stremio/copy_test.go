package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type copyRemote struct {
	mu          sync.Mutex
	source      []Record
	destination map[string]Record
	snapshots   int
	puts        int
	lose        bool
	transient   bool
}

func (r *copyRemote) Login(_ context.Context, email, _ string) (Session, error) {
	if email == "source@example.com" {
		return Session{"source", email, "private-source-key"}, nil
	}
	if email == "same@example.com" {
		return Session{"source", email, "private-source-key"}, nil
	}
	return Session{"destination", email, "private-destination-key"}, nil
}
func (r *copyRemote) Snapshot(_ context.Context, key string) ([]Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != "private-source-key" {
		return nil, ErrInvalid
	}
	r.snapshots++
	if r.transient {
		r.transient = false
		return nil, ErrUnavailable
	}
	return append([]Record(nil), r.source...), nil
}
func (r *copyRemote) ReadRecord(_ context.Context, key, id string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != "private-destination-key" {
		return Record{}, ErrInvalid
	}
	v, ok := r.destination[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return v, nil
}
func (r *copyRemote) PutRecord(_ context.Context, key string, raw json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key != "private-destination-key" {
		return ErrInvalid
	}
	v, err := parseRecord(raw)
	if err != nil {
		return err
	}
	r.puts++
	r.destination[v.ID] = v
	if r.lose {
		r.lose = false
		return ErrUnavailable
	}
	return nil
}

type copyCatalog struct{}

func (copyCatalog) Series(context.Context, string) (cinemeta.Series, error) {
	return cinemeta.Series{Movie: cinemeta.Movie{ID: "tt2934286", Name: "Show", Type: "series"}, Episodes: []cinemeta.Episode{{ID: "tt2934286:1:1", Name: "One", Season: 1, Number: 1}, {ID: "tt2934286:1:2", Name: "Two", Season: 1, Number: 2}, {ID: "tt2934286:1:3", Name: "Three", Season: 1, Number: 3}}}, nil
}
func copySeries(t *testing.T, played ...string) Record {
	t.Helper()
	ids := []string{"tt2934286:1:1", "tt2934286:1:2", "tt2934286:1:3"}
	flags := map[string]bool{}
	for _, id := range played {
		flags[id] = true
	}
	encoded, err := EncodeWatched(ids, flags)
	if err != nil {
		t.Fatal(err)
	}
	r := writeRecord(t, "series", map[string]any{"watched": encoded, "timeOffset": 456, "video_id": ids[0], "futureField": "retain", "timesWatched": 9})
	var value map[string]json.RawMessage
	json.Unmarshal(r.Raw, &value)
	value["_id"] = json.RawMessage(`"tt2934286"`)
	raw, _ := json.Marshal(value)
	r, err = parseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func copyFixture(t *testing.T) (*Store, accounts.Identity, *copyRemote, string) {
	t.Helper()
	s, who, _ := storeFixture(t)
	remote := &copyRemote{destination: map[string]Record{}}
	s.remote = remote
	s.catalog = copyCatalog{}
	id, err := s.StartCopy(context.Background(), who, CopyRequest{CopyCredentials{"source@example.com", "source-password"}, CopyCredentials{"destination@example.com", "destination-password"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, who, remote, id
}
func copyStep(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.CopyStep(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
func copyUntilPrepared(t *testing.T, s *Store, id string) {
	t.Helper()
	for n := 0; n < 30; n++ {
		var prepared bool
		s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM temporary_stremio_copy_titles WHERE copy_id=$1 AND desired_record IS NOT NULL)`, id).Scan(&prepared)
		if prepared {
			return
		}
		copyStep(t, s, id)
	}
	t.Fatal("copy intent was not prepared")
}
func finishCopy(t *testing.T, s *Store, who accounts.Identity, id string) *CopyStatus {
	t.Helper()
	for n := 0; n < 60; n++ {
		v, e := s.CopyStatus(context.Background(), who)
		if e != nil {
			t.Fatal(e)
		}
		if v.Phase == "complete" {
			return v
		}
		if v.Phase == "failed" || v.Phase == "retrying" {
			t.Fatalf("copy stopped: %+v", v)
		}
		copyStep(t, s, id)
	}
	t.Fatal("copy did not finish")
	return nil
}

func TestAccountCopyIsAdditiveAndSourceReadOnly(t *testing.T) {
	s, who, remote, id := copyFixture(t)
	sourceShow := copySeries(t, "tt2934286:1:2")
	sourceUnwatched := writeRecord(t, "movie", map[string]any{"timesWatched": 0})
	sourceWatched := writeRecord(t, "movie", map[string]any{"timesWatched": 1})
	sourceWatched.ID = "tt9999999"
	var raw map[string]any
	json.Unmarshal(sourceWatched.Raw, &raw)
	raw["_id"] = sourceWatched.ID
	sourceWatched.Raw, _ = json.Marshal(raw)
	remote.source = []Record{sourceShow, sourceUnwatched, sourceWatched}
	destinationShow := copySeries(t, "tt2934286:1:1")
	destinationMovie := writeRecord(t, "movie", map[string]any{"timesWatched": 4, "timeOffset": 321, "lastWatched": "2025-01-01T00:00:00Z"})
	remote.destination[sourceShow.ID] = destinationShow
	remote.destination[sourceUnwatched.ID] = destinationMovie
	destinationOnly := writeRecord(t, "movie", map[string]any{"timesWatched": 7})
	var only map[string]any
	json.Unmarshal(destinationOnly.Raw, &only)
	only["_id"] = "tt8888888"
	onlyRaw, _ := json.Marshal(only)
	destinationOnly, _ = parseRecord(onlyRaw)
	remote.destination[destinationOnly.ID] = destinationOnly
	sourceBefore, _ := json.Marshal(remote.source)
	movieBefore := append([]byte(nil), destinationMovie.Raw...)
	v := finishCopy(t, s, who, id)
	if v.WatchedTitles != 2 || v.WatchedItems != 2 || len(v.Skipped) != 0 {
		t.Fatal(v)
	}
	flags, err := copyFlags(remote.destination[sourceShow.ID], []string{"tt2934286:1:1", "tt2934286:1:2", "tt2934286:1:3"})
	if err != nil || !flags["tt2934286:1:1"] || !flags["tt2934286:1:2"] || flags["tt2934286:1:3"] {
		t.Fatal(flags, err)
	}
	if string(remote.destination[sourceShow.ID].State["timeOffset"]) != "456" || string(remote.destination[sourceShow.ID].State["futureField"]) != `"retain"` {
		t.Fatal("destination fields changed")
	}
	if !sameRecord(remote.destination[sourceUnwatched.ID].Raw, movieBefore, false) {
		t.Fatal("source unwatch changed destination")
	}
	if !sameRecord(remote.destination[destinationOnly.ID].Raw, destinationOnly.Raw, false) {
		t.Fatal("destination-only history changed")
	}
	created := remote.destination[sourceWatched.ID]
	var wire map[string]any
	json.Unmarshal(created.Raw, &wire)
	if played, e := MovieWatched(created); e != nil || !played || wire["temp"] != true || wire["removed"] != true {
		t.Fatal("missing destination history not created safely")
	}
	sourceAfter, _ := json.Marshal(remote.source)
	if string(sourceBefore) != string(sourceAfter) || remote.snapshots != 1 || remote.puts != 2 {
		t.Fatal("source changed or writes repeated")
	}
	var keys string
	s.pool.QueryRow(context.Background(), `SELECT source_key||destination_key FROM temporary_stremio_copies WHERE id=$1`, id).Scan(&keys)
	if keys != "" {
		t.Fatal("terminal credentials retained")
	}
	var linked int
	s.pool.QueryRow(context.Background(), `SELECT count(*) FROM stremio_links`).Scan(&linked)
	if linked != 0 {
		t.Fatal("copy linked an account to normal sync")
	}
}

func TestAccountCopyRestartAndFreshDestinationRebase(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent_change", true: "lost_write_reply"}[lose], func(t *testing.T) {
			s, who, remote, id := copyFixture(t)
			remote.source = []Record{copySeries(t, "tt2934286:1:2")}
			remote.destination["tt2934286"] = copySeries(t, "tt2934286:1:1")
			copyUntilPrepared(t, s, id)
			if lose {
				remote.lose = true
				copyStep(t, s, id)
				if err := s.ControlCopy(context.Background(), who, id, false); err != nil {
					t.Fatal(err)
				}
			} else {
				remote.destination["tt2934286"] = copySeries(t, "tt2934286:1:3")
			}
			restarted := NewStore(s.pool, remote, copyCatalog{})
			finishCopy(t, restarted, who, id)
			flags, err := copyFlags(remote.destination["tt2934286"], []string{"tt2934286:1:1", "tt2934286:1:2", "tt2934286:1:3"})
			if err != nil || !flags["tt2934286:1:1"] || !flags["tt2934286:1:2"] || (!lose && !flags["tt2934286:1:3"]) {
				t.Fatal("retained destination history was lost", flags, err)
			}
			if remote.puts != 1 || remote.snapshots != 1 {
				t.Fatal("unknown outcome was replayed or source reread")
			}
		})
	}
}

func TestAccountCopySkipsRetryAndCancellation(t *testing.T) {
	s, who, remote, id := copyFixture(t)
	unsupported := writeRecord(t, "movie", map[string]any{"timesWatched": 1})
	var raw map[string]any
	json.Unmarshal(unsupported.Raw, &raw)
	raw["_id"] = "kitsu:1444"
	unsupported.Raw, _ = json.Marshal(raw)
	unsupported, _ = parseRecord(unsupported.Raw)
	remote.source = []Record{unsupported, writeRecord(t, "movie", map[string]any{"timesWatched": 1})}
	remote.transient = true
	copyStep(t, s, id)
	v, err := s.CopyStatus(context.Background(), who)
	if err != nil || v.Phase != "retrying" || remote.puts != 0 {
		t.Fatal(v, err)
	}
	if err = s.ControlCopy(context.Background(), who, id, false); err != nil {
		t.Fatal(err)
	}
	v = finishCopy(t, s, who, id)
	if len(v.Skipped) != 1 || v.Skipped[0].ID != "kitsu:1444" || remote.puts != 1 {
		t.Fatal(v)
	}
	second, err := s.StartCopy(context.Background(), who, CopyRequest{CopyCredentials{"source@example.com", "password"}, CopyCredentials{"destination@example.com", "password"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ControlCopy(context.Background(), who, second, true); err != nil {
		t.Fatal(err)
	}
	copyStep(t, s, second)
	v, err = s.CopyStatus(context.Background(), who)
	if err != nil || v.Phase != "cancelled" || v.Error != "" || remote.puts != 1 {
		t.Fatal(v, err)
	}
	if err = s.ControlCopy(context.Background(), who, second, false); !errors.Is(err, ErrChanged) {
		t.Fatal("cancelled credentials were resurrected", err)
	}
}

func TestAccountCopySameAccountAndAdminBoundaries(t *testing.T) {
	s, who, remote, _ := copyFixture(t)
	_, err := s.StartCopy(context.Background(), who, CopyRequest{CopyCredentials{"source@example.com", "password"}, CopyCredentials{"same@example.com", "password"}})
	if !errors.Is(err, ErrCopySameAccount) {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.StartCopy(context.Background(), who, CopyRequest{CopyCredentials{"source@example.com", "password"}, CopyCredentials{"destination@example.com", "password"}})
	if !errors.Is(err, accounts.ErrForbidden) {
		t.Fatal(err)
	}
	var id string
	s.pool.QueryRow(context.Background(), `SELECT id::text FROM temporary_stremio_copies WHERE user_id=$1`, who.ID).Scan(&id)
	copyStep(t, s, id)
	var phase, key string
	s.pool.QueryRow(context.Background(), `SELECT phase,source_key||destination_key FROM temporary_stremio_copies WHERE id=$1`, id).Scan(&phase, &key)
	if phase != "cancelled" || key != "" || remote.puts != 0 {
		t.Fatal("revoked permission did not fence copy")
	}
}

func TestAccountCopyUsesAccountLockAndFreshRead(t *testing.T) {
	s, who, remote, id := copyFixture(t)
	remote.source = []Record{copySeries(t, "tt2934286:1:2")}
	remote.destination["tt2934286"] = copySeries(t, "tt2934286:1:1")
	copyUntilPrepared(t, s, id)
	held, err := s.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	if _, err = held.Exec(context.Background(), `UPDATE stremio_accounts SET auth_key='existing-linked-key' WHERE id='destination'`); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.CopyStep(context.Background(), id); finished <- err }()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err = s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM stremio_accounts WHERE id=$1 FOR UPDATE%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("copy did not wait on the normal account row lock")
	}
	remote.mu.Lock()
	remote.destination["tt2934286"] = copySeries(t, "tt2934286:1:1", "tt2934286:1:3")
	remote.mu.Unlock()
	if err = held.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if remote.puts != 0 {
		t.Fatal("changed destination was written without new durable intent")
	}
	finishCopy(t, s, who, id)
	flags, _ := copyFlags(remote.destination["tt2934286"], []string{"tt2934286:1:1", "tt2934286:1:2", "tt2934286:1:3"})
	if !flags["tt2934286:1:1"] || !flags["tt2934286:1:2"] || !flags["tt2934286:1:3"] {
		t.Fatal(flags)
	}
	var key string
	s.pool.QueryRow(context.Background(), `SELECT auth_key FROM stremio_accounts WHERE id='destination'`).Scan(&key)
	if key != "existing-linked-key" {
		t.Fatal("ordinary integration credentials replaced")
	}
}

func TestAccountCopyRunsThroughPersistentRiverQueue(t *testing.T) {
	s, who, remote, id := copyFixture(t)
	remote.source = []Record{writeRecord(t, "movie", map[string]any{"timesWatched": 1})}
	var schema string
	if err := s.pool.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &copyWorker{store: s})
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema, Workers: workers, Queues: map[string]river.QueueConfig{"stremio": {MaxWorkers: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := q.StopAndCancel(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.CopyStatus(context.Background(), who)
		if err != nil {
			t.Fatal(err)
		}
		if v.Phase == "complete" {
			var state string
			err = s.pool.QueryRow(context.Background(), `SELECT state FROM river_job WHERE kind='temporary_stremio_copy' AND args->>'id'=$1 ORDER BY id DESC LIMIT 1`, id).Scan(&state)
			if err == nil && state == "completed" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queued copy did not finish")
}
