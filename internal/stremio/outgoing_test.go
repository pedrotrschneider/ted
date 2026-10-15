package stremio

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/watchstate"
	"testing"
	"time"
)

type outgoingRemote struct {
	*remoteStub
	puts      int
	lose      bool
	readError error
}

func (r *outgoingRemote) ReadRecord(_ context.Context, _ string, id string) (Record, error) {
	if r.readError != nil {
		return Record{}, r.readError
	}
	for _, v := range r.records {
		if v.ID == id {
			return v, nil
		}
	}
	return Record{}, ErrNotFound
}
func (r *outgoingRemote) PutRecord(_ context.Context, _ string, raw json.RawMessage) error {
	r.puts++
	v, err := parseRecord(raw)
	if err != nil {
		return err
	}
	found := false
	for i := range r.records {
		if r.records[i].ID == v.ID {
			r.records[i] = v
			found = true
		}
	}
	if !found {
		r.records = append(r.records, v)
	}
	if r.lose {
		r.lose = false
		return ErrUnavailable
	}
	return nil
}
func enqueueOutgoing(t *testing.T, s *Store, user int64, played bool) int64 {
	t.Helper()
	var id int64
	err := s.pool.QueryRow(context.Background(), `INSERT INTO stremio_outgoing(link_id,account_id,user_id,media_id,identity,title_id,played) SELECT l.id,l.account_id,l.user_id,c.id,c.cinemeta_id,c.cinemeta_id,$2 FROM stremio_links l CROSS JOIN catalog_media c WHERE l.user_id=$1 AND l.retired_at IS NULL AND c.cinemeta_id='tt1234567' RETURNING id`, user, played).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func sendStep(t *testing.T, s *Store) {
	t.Helper()
	if more, err := s.SendStep(context.Background(), "remote"); err != nil || !more {
		t.Fatal(more, err)
	}
}
func outgoingPhase(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var phase string
	if err := s.pool.QueryRow(context.Background(), `SELECT phase FROM stremio_outgoing WHERE id=$1`, id).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	return phase
}
func TestStremioOutgoingRecoveryAndSharedEcho(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	ctx := context.Background()
	remote := &outgoingRemote{remoteStub: stub}
	s.remote = remote
	var other int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,role) VALUES('other','','Other','user') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	second := accounts.Identity{ID: other, Version: 1}
	connectSync(t, s, second)
	pollAndDeliver(t, s)
	id := enqueueOutgoing(t, s, who.ID, false)
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "prepared" || remote.puts != 0 {
		t.Fatal("Intent must commit before effect")
	}
	remote.lose = true
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "prepared" || remote.puts != 1 {
		t.Fatal("Lost reply discarded durable intent")
	}
	// Polling an uncertain effect must not fabricate shared-account history.
	pollAndDeliver(t, s)
	h, err := watchstate.New(s.pool).History(ctx, second, 0)
	if err != nil || len(h.Entries) != 1 || !h.Current[0].State.Played {
		t.Fatal(h, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE stremio_outgoing SET next_attempt=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// Retiring the origin must still allow read-only recovery for shared users.
	if err = s.Disconnect(ctx, who); err != nil {
		t.Fatal(err)
	}
	s = NewStore(s.pool, remote, catalogStub{})
	sendStep(t, s)
	if remote.puts != 1 || outgoingPhase(t, s, id) != "applied" {
		t.Fatal("Restart repeated successful write")
	}
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, second, 0)
	if err != nil || len(h.Entries) != 1 || h.Current[0].State.Played {
		t.Fatal("Own echo fabricated history", h, err)
	}
	// Once the acknowledgment is consumed, a genuinely external rewatch is new.
	remote.records = []Record{pollMovie(t, true, 8)}
	pollAndDeliver(t, s)
	h, err = watchstate.New(s.pool).History(ctx, second, 0)
	if err != nil || len(h.Entries) != 2 || !h.Current[0].State.Played {
		t.Fatal(h, err)
	}
}
func TestStremioOutgoingFreshReadConflictAndSupersession(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	remote := &outgoingRemote{remoteStub: stub}
	s.remote = remote
	id := enqueueOutgoing(t, s, who.ID, false)
	sendStep(t, s)
	remote.records = []Record{pollMovie(t, true, 2)}
	sendStep(t, s)
	if remote.puts != 0 || outgoingPhase(t, s, id) != "conflict" {
		t.Fatal("Changed remote record overwritten")
	}
	old := enqueueOutgoing(t, s, who.ID, false)
	newest := enqueueOutgoing(t, s, who.ID, true)
	sendStep(t, s)
	sendStep(t, s)
	if outgoingPhase(t, s, old) != "cancelled" || outgoingPhase(t, s, newest) != "applied" || remote.puts != 0 {
		t.Fatal("Older decision won")
	}
	status, err := s.Status(context.Background(), who)
	if err != nil || len(status.Sync.Outgoing) != 0 {
		t.Fatal(status, err)
	}
}
func TestStremioOutgoingAccountLockAndRetirement(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	remote := &outgoingRemote{remoteStub: stub}
	s.remote = remote
	id := enqueueOutgoing(t, s, who.ID, false)
	ctx := context.Background()
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT id FROM stremio_accounts WHERE id='remote' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err = s.SendStep(bounded, "remote"); err == nil || remote.puts != 0 {
		t.Fatal("Account serialization bypassed")
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	sendStep(t, s)
	if err = s.Disconnect(ctx, who); err != nil {
		t.Fatal(err)
	}
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "cancelled" || remote.puts != 0 {
		t.Fatal("Retired link wrote remotely")
	}
}

func TestStremioOutgoingDisabledPreparedWorkDoesNotBlockSharedPolls(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	remote := &outgoingRemote{remoteStub: stub}
	s.remote = remote
	ctx := context.Background()
	id := enqueueOutgoing(t, s, who.ID, false)
	sendStep(t, s)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET disabled=true WHERE id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	sendStep(t, s)
	if remote.puts != 0 || outgoingPhase(t, s, id) != "pending" {
		t.Fatal("Disabled user wrote or retained an unresolved remote effect")
	}
	if more, err := s.SendStep(ctx, "remote"); err != nil || more {
		t.Fatal("Disabled pending intent did not pause", more, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE users SET disabled=false WHERE id=$1`, who.ID); err != nil {
		t.Fatal(err)
	}
	sendStep(t, s)
	sendStep(t, s)
	if remote.puts != 1 || outgoingPhase(t, s, id) != "applied" {
		t.Fatal("Re-enabled intent did not resume")
	}
}

func TestStremioOutgoingCreatesMissingRecordAndRecoversLostResponse(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE catalog_media SET metadata='{"name":"Fixture movie","poster":""}' WHERE cinemeta_id='tt1234567'`); err != nil {
		t.Fatal(err)
	}
	remote := &outgoingRemote{remoteStub: stub}
	remote.records = nil
	s.remote = remote
	id := enqueueOutgoing(t, s, who.ID, true)
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "prepared" || remote.puts != 0 {
		t.Fatal("creation not durably prepared")
	}
	var absent bool
	if err := s.pool.QueryRow(ctx, `SELECT before_record='null'::jsonb FROM stremio_outgoing WHERE id=$1`, id).Scan(&absent); err != nil || !absent {
		t.Fatal(absent, err)
	}
	remote.lose = true
	sendStep(t, s)
	if remote.puts != 1 || outgoingPhase(t, s, id) != "prepared" {
		t.Fatal("lost response not recoverable")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE stremio_outgoing SET next_attempt=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	s = NewStore(s.pool, remote, catalogStub{})
	sendStep(t, s)
	if remote.puts != 1 || outgoingPhase(t, s, id) != "applied" {
		t.Fatal("replayed uncertain creation")
	}
	var fields map[string]any
	json.Unmarshal(remote.records[0].Raw, &fields)
	if fields["temp"] != true || fields["removed"] != true || fields["name"] != "Fixture movie" {
		t.Fatal("wrong temporary record")
	}
	watched, err := MovieWatched(remote.records[0])
	if err != nil || !watched {
		t.Fatal(watched, err)
	}
}
func TestStremioOutgoingCreationDoesNotOverwriteAppearingRecord(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	if _, err := s.pool.Exec(context.Background(), `UPDATE catalog_media SET metadata='{"name":"Fixture"}' WHERE cinemeta_id='tt1234567'`); err != nil {
		t.Fatal(err)
	}
	remote := &outgoingRemote{remoteStub: stub}
	remote.records = nil
	s.remote = remote
	id := enqueueOutgoing(t, s, who.ID, true)
	sendStep(t, s)
	remote.records = []Record{pollMovie(t, false, 2)}
	before := remote.records[0].Raw
	sendStep(t, s)
	if remote.puts != 0 || outgoingPhase(t, s, id) != "conflict" || !sameRecord(before, remote.records[0].Raw, false) {
		t.Fatal("new remote record overwritten")
	}
}
func TestStremioOutgoingMissingUnwatchAndIncompleteReadNeverCreate(t *testing.T) {
	s, who, stub := linkedPollFixture(t, true)
	remote := &outgoingRemote{remoteStub: stub}
	remote.records = nil
	s.remote = remote
	id := enqueueOutgoing(t, s, who.ID, false)
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "applied" || remote.puts != 0 {
		t.Fatal("unwatch created a record")
	}
	remote.readError = ErrIncomplete
	id = enqueueOutgoing(t, s, who.ID, true)
	sendStep(t, s)
	if outgoingPhase(t, s, id) != "pending" || remote.puts != 0 {
		t.Fatal("incomplete read allowed creation")
	}
}
