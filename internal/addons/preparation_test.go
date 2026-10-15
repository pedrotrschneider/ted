package addons

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
)

func TestSelectedPreparationCancellationAndRevocationBeforeTailPublication(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller cancellation", true: "family revoked"}[revoke], func(t *testing.T) {
			s, auth, grant, _, _ := fixture(t)
			choice := choose(t, s, auth, grant, fixtureMedia)
			entered, release := make(chan struct{}), make(chan struct{})
			s.media = &http.Client{Transport: heldPreparationTail{s.mediaHTTP().Transport, entered, release}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := s.CreateSession(ctx, auth, grant, fixtureMedia, choice)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("tail preparation did not start")
			}
			want := error(context.Canceled)
			if revoke {
				if err := auth.Logout(context.Background(), grant, false); err != nil {
					t.Fatal(err)
				}
				want = accounts.ErrUnauthorized
				close(release)
			} else {
				cancel()
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("preparation lost cancellation or published a revoked session", err)
			}
			var effects, sources int
			if err := s.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM native_playback_sessions)+(SELECT count(*) FROM native_addon_playback)+(SELECT count(*) FROM watch_history),(SELECT count(*) FROM native_addon_sources)`).Scan(&effects, &sources); err != nil || effects != 0 || sources != 1 {
				t.Fatal("failed preparation changed retained source or native history", effects, sources, err)
			}
		})
	}
}

type heldPreparationTail struct {
	base             http.RoundTripper
	entered, release chan struct{}
}

func (h heldPreparationTail) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Range") != "bytes=0-4095" {
		close(h.entered)
		select {
		case <-h.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return h.base.RoundTrip(r)
}
