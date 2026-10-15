package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"errors"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"testing"
	"time"
)

type remoteStub struct {
	records []Record
	err     error
	entered chan struct{}
	release chan struct{}
}

func (r *remoteStub) Login(context.Context, string, string) (Session, error) {
	return Session{"remote", "test@example.com", "private-key-never-return"}, nil
}
func (r *remoteStub) Snapshot(ctx context.Context, _ string) ([]Record, error) {
	if r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.records, r.err
}

type catalogStub struct{}

func (catalogStub) Series(context.Context, string) (cinemeta.Series, error) {
	return cinemeta.Series{Movie: cinemeta.Movie{ID: "tt2934286", Type: "series", Name: "Show"}, Episodes: []cinemeta.Episode{{ID: "tt2934286:1:1", Name: "One", Season: 1, Number: 1}, {ID: "tt2934286:1:2", Name: "Two", Season: 1, Number: 2}}}, nil
}
func storeFixture(t *testing.T) (*Store, accounts.Identity, *remoteStub) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var id int64
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('admin','Admin','','admin') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	record, err := parseRecord([]byte(recordJSON))
	if err != nil {
		t.Fatal(err)
	}
	remote := &remoteStub{records: []Record{record}}
	return NewStore(pool, remote, catalogStub{}), accounts.Identity{ID: id, Version: 1}, remote
}
func TestStremioLinkIsolationCredentialsAndRevocation(t *testing.T) {
	s, who, _ := storeFixture(t)
	ctx := context.Background()
	connectSync(t, s, who)
	var other int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES('other','Other','','user') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	second := accounts.Identity{ID: other, Version: 1}
	v, err := s.Status(ctx, second)
	if err != nil || v.Linked || v.Sync != nil {
		t.Fatal(v, err)
	}
	connectSync(t, s, second)
	if err = s.Disconnect(ctx, who); err != nil {
		t.Fatal(err)
	}
	var key string
	if err = s.pool.QueryRow(ctx, `SELECT auth_key FROM stremio_accounts`).Scan(&key); err != nil || key == "" {
		t.Fatal("shared key", err)
	}
	if err = s.Disconnect(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT auth_key FROM stremio_accounts`).Scan(&key); err != nil || key != "" {
		t.Fatal("orphan key", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET session_version=session_version+1 WHERE id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Status(ctx, who); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
	if err = s.Connect(ctx, who, "test@example.com", "password"); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
}
func TestStremioAccountLockCancellationAndRiverExecution(t *testing.T) {
	s, who, remote := storeFixture(t)
	ctx := context.Background()
	connectSync(t, s, who)
	var schema string
	s.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema)
	workers := river.NewWorkers()
	river.AddWorker(workers, &pollWorker{store: s})
	river.AddWorker(workers, &deliveryWorker{store: s})
	river.AddWorker(workers, &dispatchWorker{store: s})
	river.AddWorker(workers, &outgoingWorker{store: s})
	q, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{Schema: schema, Workers: workers, Queues: map[string]river.QueueConfig{"stremio": {MaxWorkers: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	remote.entered = make(chan struct{})
	remote.release = make(chan struct{})
	if err = q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		q.StopAndCancel(stop)
	})
	select {
	case <-remote.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not reached")
	}
	bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err = s.pool.Exec(bounded, `UPDATE stremio_accounts SET updated_at=now() WHERE id='remote'`); err == nil {
		t.Fatal("account lock bypassed")
	}
	close(remote.release)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.Status(ctx, who)
		if err != nil {
			t.Fatal(err)
		}
		if v.Sync != nil && v.Sync.LastSuccess != nil && !v.Sync.Pending {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("persistent sync did not finish")
}
func TestStremioEpisodePreviewDoesNotImportPositions(t *testing.T) {
	encoded, err := EncodeWatched([]string{"tt2934286:1:1", "tt2934286:1:2"}, map[string]bool{"tt2934286:1:2": true})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(encoded)
	record := Record{ID: "tt2934286", Type: "series", State: map[string]json.RawMessage{"watched": value, "timeOffset": json.RawMessage(`100000`)}}
	entries, ids, err := Resolve(context.Background(), record, catalogStub{})
	if err != nil || len(entries) != 1 || entries[0].ID != "tt2934286:1:2" || len(ids) != 2 {
		t.Fatal(entries, err)
	}
}
