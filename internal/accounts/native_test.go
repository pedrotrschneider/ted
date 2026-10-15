package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
)

const nativePassword = "native-test-password"

func nativeFixture(t *testing.T) (*NativeAuth, User) {
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
	passwords, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	service := New(pool, passwords)
	u, err := service.Bootstrap(ctx, "admin", nativePassword)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewNativeAuth(pool, service, DefaultNativePolicy())
	if err != nil {
		t.Fatal(err)
	}
	return s, u
}

func nativeSignIn(t *testing.T, s *NativeAuth, name string) NativeTokens {
	t.Helper()
	pair, err := s.Login(context.Background(), name, nativePassword, "127.0.0.1", "device-1", "Test player")
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func assertNativeAccess(t *testing.T, s *NativeAuth, token string, allowed bool) {
	t.Helper()
	_, err := s.Authenticate(context.Background(), token)
	if allowed && err != nil || !allowed && !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("access allowed=%t error=%v", allowed, err)
	}
}

func TestNativeRotationLineageConstraintAndRestart(t *testing.T) {
	s, _ := nativeFixture(t)
	ctx := context.Background()
	original := nativeSignIn(t, s, "admin")
	pair := original
	for range 4 {
		next, err := s.Refresh(ctx, pair.RefreshToken)
		if err != nil {
			t.Fatal(err)
		}
		if next.FamilyID != original.FamilyID || next.RefreshToken == pair.RefreshToken || next.AccessToken == pair.AccessToken || !next.FamilyExpiresAt.Equal(original.FamilyExpiresAt) {
			t.Fatal("rotation changed family/deadline or reused credentials")
		}
		pair = next
	}
	var total, current, predecessors int
	err := s.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE consumed_at IS NULL),count(previous_digest) FROM native_refresh_tokens WHERE family_id=$1`, pair.FamilyID).Scan(&total, &current, &predecessors)
	if err != nil || total != 5 || current != 1 || predecessors != 4 {
		t.Fatal(total, current, predecessors, err)
	}
	_, digest := opaqueToken("ted_r_")
	_, err = s.pool.Exec(ctx, `INSERT INTO native_refresh_tokens(digest,family_id,generation,previous_digest,created_at,expires_at)
 SELECT $1,family_id,6,digest,clock_timestamp(),expires_at FROM native_refresh_tokens WHERE family_id=$2 AND consumed_at IS NULL`, digest, pair.FamilyID)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		t.Fatal("database allowed two current refresh tokens", err)
	}
	restarted, err := NewNativeAuth(s.pool, s.accounts, DefaultNativePolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, restarted, pair.AccessToken, true)
	if _, err = restarted.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatal("persisted rotation lost across service restart", err)
	}
}

func TestNativeExpiryPurposeAbsoluteCapAndReplayIsolation(t *testing.T) {
	s, _ := nativeFixture(t)
	ctx := context.Background()
	pair := nativeSignIn(t, s, "admin")
	other := nativeSignIn(t, s, "admin")
	assertNativeAccess(t, s, pair.RefreshToken, false)
	if _, err := s.Refresh(ctx, pair.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("wrong purpose accepted", err)
	}
	unknown, _ := opaqueToken("ted_r_")
	if _, err := s.Refresh(ctx, unknown); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, pair.AccessToken, true)
	// Expired access is distinct from a usable refresh token.
	if _, err := s.pool.Exec(ctx, `UPDATE native_access_tokens SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE family_id=$1`, pair.FamilyID); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, pair.AccessToken, false)
	var absolute time.Time
	if err := s.pool.QueryRow(ctx, `UPDATE native_token_families SET absolute_expires_at=clock_timestamp()+interval '30 seconds',idle_expires_at=clock_timestamp()+interval '20 seconds' WHERE id=$1 RETURNING absolute_expires_at`, pair.FamilyID).Scan(&absolute); err != nil {
		t.Fatal(err)
	}
	next, err := s.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if !next.FamilyExpiresAt.Equal(absolute) || next.RefreshExpiresAt.After(absolute) || next.AccessExpiresAt.After(absolute) {
		t.Fatal("rotation extended absolute deadline")
	}
	assertNativeAccess(t, s, next.AccessToken, true)
	// Known spent tokens revoke even when their original access grant expired.
	if _, err = s.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, next.AccessToken, false)
	if _, err = s.Refresh(ctx, next.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, other.AccessToken, true)
	var reason string
	if err = s.pool.QueryRow(ctx, `SELECT revocation_reason FROM native_token_families WHERE id=$1`, pair.FamilyID).Scan(&reason); err != nil || reason != "refresh_reuse" {
		t.Fatal(reason, err)
	}
	// Expired inactivity is checked by both authentication and refresh.
	if _, err = s.pool.Exec(ctx, `UPDATE native_token_families SET created_at=clock_timestamp()-interval '2 days',idle_expires_at=clock_timestamp()-interval '1 day' WHERE id=$1`, other.FamilyID); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, other.AccessToken, false)
	if _, err = s.Refresh(ctx, other.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestNativeFailedCommitPreservesOldTokenAndLostResponseRevokes(t *testing.T) {
	s, _ := nativeFixture(t)
	ctx := context.Background()
	pair := nativeSignIn(t, s, "admin")
	_, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_native_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'native-secret-diagnostic'; END $$;
 CREATE CONSTRAINT TRIGGER fail_native_commit AFTER INSERT ON native_access_tokens DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_native_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.Refresh(ctx, pair.RefreshToken)
	if err == nil || failed.AccessToken != "" || failed.RefreshToken != "" {
		t.Fatal("failed commit returned credentials")
	}
	failed, err = s.Login(ctx, "admin", nativePassword, "127.0.0.1", "device-2", "Other player")
	if err == nil || failed.AccessToken != "" {
		t.Fatal("failed login commit returned credentials")
	}
	var current, families int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM native_refresh_tokens WHERE consumed_at IS NULL),(SELECT count(*) FROM native_token_families)`).Scan(&current, &families); err != nil || current != 1 || families != 1 {
		t.Fatal(current, families, err)
	}
	assertNativeAccess(t, s, pair.AccessToken, true)
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER fail_native_commit ON native_access_tokens`); err != nil {
		t.Fatal(err)
	}
	// Pretend the successful rotation response was lost. Retrying the spent
	// credential must revoke, not issue a second successor or restore the old row.
	lost, err := s.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatal("failed commit consumed previous token", err)
	}
	if _, err = s.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, lost.AccessToken, false)
	if _, err = s.Refresh(ctx, lost.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	fresh := nativeSignIn(t, s, "admin")
	if fresh.FamilyID == pair.FamilyID {
		t.Fatal("fresh login resurrected revoked family")
	}
	assertNativeAccess(t, s, fresh.AccessToken, true)
}

func waitNativeBlocked(t *testing.T, s *NativeAuth, heldPID int, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		// PostgreSQL may queue the second row-lock waiter behind the first,
		// rather than directly behind our transaction. Inspect the whole chain.
		if err := s.pool.QueryRow(ctx, `WITH RECURSIVE blocked AS (
 SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))
 UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
 ) SELECT count(*) FROM blocked`, heldPID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("requests did not reach held PostgreSQL user lock")
		case <-ticker.C:
		}
	}
}

func TestNativeConcurrentRefreshRevokesSuccessfulWinner(t *testing.T) {
	s, u := nativeFixture(t)
	ctx := context.Background()
	pair := nativeSignIn(t, s, "admin")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type result struct {
		pair NativeTokens
		err  error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() { p, e := s.Refresh(ctx, pair.RefreshToken); results <- result{p, e} }()
	}
	waitNativeBlocked(t, s, pid, 2)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	winners, rejected := 0, 0
	var winner NativeTokens
	for range 2 {
		r := <-results
		if r.err == nil {
			winners++
			winner = r.pair
		} else if errors.Is(r.err, ErrUnauthorized) {
			rejected++
		} else {
			t.Fatal(r.err)
		}
	}
	if winners != 1 || rejected != 1 {
		t.Fatal(winners, rejected)
	}
	assertNativeAccess(t, s, winner.AccessToken, false)
	if _, err = s.Refresh(ctx, winner.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("winner survived spent-token reuse", err)
	}
	var total int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM native_refresh_tokens WHERE family_id=$1`, pair.FamilyID).Scan(&total); err != nil || total != 2 {
		t.Fatal("multiple successors issued", total, err)
	}
}

func TestNativeAccountAndFamilyRevocationFenceQueuedRefreshAndWrites(t *testing.T) {
	s, u := nativeFixture(t)
	ctx := context.Background()
	pair := nativeSignIn(t, s, "admin")
	grant, err := s.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Rollback(tx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { _, e := s.Refresh(ctx, pair.RefreshToken); results <- e }()
	go func() {
		write, e := s.pool.Begin(ctx)
		if e == nil {
			defer database.Rollback(write)
			e = s.AuthorizeMutation(ctx, write, grant)
		}
		results <- e
	}()
	waitNativeBlocked(t, s, pid, 2)
	if _, err = tx.Exec(ctx, `UPDATE native_token_families SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE id=$1`, pair.FamilyID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = <-results; !errors.Is(err, ErrUnauthorized) {
			t.Fatal("queued operation ignored family revocation", err)
		}
	}
	assertNativeAccess(t, s, pair.AccessToken, false)
	// Account revocation invalidates all other families as well.
	other := nativeSignIn(t, s, "admin")
	if err = s.accounts.Revoke(ctx, Identity{u.ID, u.Version}); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, other.AccessToken, false)
	if _, err = s.Refresh(ctx, other.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestNativeLogoutScopesPasswordAndDisable(t *testing.T) {
	s, admin := nativeFixture(t)
	ctx := context.Background()
	u, err := s.accounts.Create(ctx, Identity{admin.ID, admin.Version}, "viewer", "Viewer", nativePassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	a, b := nativeSignIn(t, s, "viewer"), nativeSignIn(t, s, "viewer")
	g, err := s.Authenticate(ctx, a.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, a.AccessToken, false)
	assertNativeAccess(t, s, b.AccessToken, true)
	if err = s.accounts.SetDisabled(ctx, Identity{admin.ID, admin.Version}, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.SetDisabled(ctx, Identity{admin.ID, admin.Version}, u.ID, false); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, b.AccessToken, false)
	if _, err = s.Refresh(ctx, b.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	fresh := nativeSignIn(t, s, "viewer")
	g, err = s.Authenticate(ctx, fresh.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.ChangePassword(ctx, Identity{g.User.ID, g.User.Version}, nativePassword, "replacement-password"); err != nil {
		t.Fatal(err)
	}
	assertNativeAccess(t, s, fresh.AccessToken, false)
	if _, err = s.Refresh(ctx, fresh.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}
