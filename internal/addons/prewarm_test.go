package addons

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"testing"
	"time"
)

func TestPreparedSelectionConsumedOnceWithFreshAuthorization(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "consume", true: "revoked"}[revoke], func(t *testing.T) {
			s, auth, g, _, provider := fixture(t)
			ctx := context.Background()
			choice := choose(t, s, auth, g, fixtureMedia)
			v, e := s.selectedForPlayback(ctx, g, fixtureMedia, choice)
			if e != nil {
				t.Fatal(e)
			}
			p, e := s.prepare(ctx, v.source)
			if e != nil {
				t.Fatal(e)
			}
			s.warm.entries = map[string]*warmEntry{warmKey(g, choice): {source: startupKey(prepared{Source: v.source}), expires: time.Now().Add(time.Hour), ready: true, p: p, cancel: func() {}}}
			other := g
			other.DeviceID = "different-device"
			if _, ok := s.takeWarm(other, choice, v.source); ok {
				t.Fatal("preparation escaped device scope")
			}
			provider.mu.Lock()
			before := provider.requests
			provider.mu.Unlock()
			if revoke {
				if e = auth.Logout(ctx, g, false); e != nil {
					t.Fatal(e)
				}
			}
			_, e = s.CreateSession(ctx, auth, g, fixtureMedia, choice)
			if revoke {
				if !errors.Is(e, accounts.ErrUnauthorized) {
					t.Fatal("revoked preparation published", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			provider.mu.Lock()
			after := provider.requests
			provider.mu.Unlock()
			if after != before {
				t.Fatal("prepared play repeated provider validation")
			}
			if _, ok := s.takeWarm(g, choice, v.source); ok {
				t.Fatal("preparation consumed twice")
			}
			var history int
			if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&history); e != nil || history != 0 {
				t.Fatal("preparation fabricated viewing", history, e)
			}
		})
	}
}
func TestBackgroundPreparationRejectsDirectResolvers(t *testing.T) {
	s, auth, g, _, provider := fixture(t)
	ctx := context.Background()
	choice := choose(t, s, auth, g, fixtureMedia)
	provider.mu.Lock()
	before := provider.requests
	provider.mu.Unlock()
	if e := s.PrepareSource(ctx, auth, g, fixtureMedia, choice); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.requests != before {
		t.Fatal("background work invoked a direct resolver")
	}
}

func TestCachedPickerLifetimeRetainsAuthorizationFences(t *testing.T) {
	s, auth, g, _, _ := fixture(t)
	ctx := context.Background()
	choice := choose(t, s, auth, g, fixtureMedia)
	var hours float64
	if err := s.pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-now())/3600 FROM native_addon_sources WHERE id=$1`, choice).Scan(&hours); err != nil || hours < 12 || hours > 13 {
		t.Fatal("picker does not cover client cache", hours, err)
	}
	if _, err := s.selectedForPlayback(ctx, g, fixtureMedia, choice); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE server_addon_accounts SET active=false,auth_key='' WHERE active`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.selectedForPlayback(ctx, g, fixtureMedia, choice); !errors.Is(err, ErrSelection) {
		t.Fatal("cached selection survived unlink", err)
	}
}
