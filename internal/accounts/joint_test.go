package accounts

import (
	"context"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"errors"
	"testing"
)

func TestJointAccountsReadOnlyMembershipAndColors(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	passwords, _ := NewPasswords(0)
	s := New(pool, passwords)
	home, err := s.Bootstrap(ctx, "home", "")
	if err != nil {
		t.Fatal(err)
	}
	h := Identity{home.ID, home.Version}
	pedro, err := s.Create(ctx, h, "pedro", "Pedro", "", "user")
	if err != nil {
		t.Fatal(err)
	}
	p := Identity{pedro.ID, pedro.Version}
	clara, err := s.Create(ctx, h, "clara", "Clara", "", "user")
	if err != nil {
		t.Fatal(err)
	}
	c := Identity{clara.ID, clara.Version}
	if err = s.ChangeJoint(ctx, h, "add", p.ID, ""); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		who    Identity
		target int64
	}{{p, c.ID}, {c, h.ID}, {h, p.ID}} {
		if err = s.ChangeJoint(ctx, v.who, "add", v.target, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("nested/duplicate membership", err)
		}
	}
	if err = s.ChangeJoint(ctx, c, "add", p.ID, ""); err != nil {
		t.Fatal("multiple joint accounts", err)
	}
	if err = s.ChangeJoint(ctx, p, "color", h.ID, "#123ABC"); err != nil {
		t.Fatal(err)
	}
	v, err := s.JointSettings(ctx, p)
	if err != nil || len(v.Memberships) != 2 || len(v.Candidates) != 0 {
		t.Fatal(v, err)
	}
	if err = s.ChangeJoint(ctx, h, "color", p.ID, "#FFFFFF"); !errors.Is(err, ErrNotFound) {
		t.Fatal("joint changed member preference", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO stremio_accounts(id,email,auth_key) VALUES('shared','test@example.com','private');INSERT INTO stremio_links(user_id,account_id) SELECT id,'shared' FROM users WHERE username IN ('home','pedro')`); err != nil {
		t.Fatal(err)
	}
	v, err = s.JointSettings(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	warning := false
	for _, m := range v.Memberships {
		if m.ID == h.ID {
			warning = m.SharedStremio && m.Color == "#123ABC"
		}
	}
	if !warning {
		t.Fatal("shared external history warning missing")
	}
	if err = s.ChangeJoint(ctx, p, "leave", h.ID, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.JointSettings(ctx, h)
	if len(v.Members) != 0 {
		t.Fatal("leave failed")
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM watch_current`).Scan(&n); err != nil || n != 0 {
		t.Fatal("membership fabricated history", err)
	}
}
func TestJointConcurrentOppositeMemberships(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pw, _ := NewPasswords(0)
	s := New(pool, pw)
	a, err := s.Bootstrap(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	ai := Identity{a.ID, a.Version}
	b, err := s.Create(ctx, ai, "bobby", "Bob", "", "user")
	if err != nil {
		t.Fatal(err)
	}
	bi := Identity{b.ID, b.Version}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, MutationLockID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- s.ChangeJoint(ctx, ai, "add", b.ID, "") }()
	go func() { results <- s.ChangeJoint(ctx, bi, "add", a.ID, "") }()
	waitBlocked(t, pool.QueryRow, 2)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	success, conflict := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
