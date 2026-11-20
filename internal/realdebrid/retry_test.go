package realdebrid

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRateLimitKeepsProviderRetryAfterAndSanitizedIdentity(t *testing.T) {
	client := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "125")
		w.WriteHeader(429)
		w.Write([]byte("private-provider-response"))
	})
	_, err := client.List(context.Background(), 1)
	var delayed interface{ RetryAfter() time.Duration }
	if !errors.Is(err, ErrLimited) || !errors.As(err, &delayed) || delayed.RetryAfter() != 125*time.Second || err.Error() != ErrLimited.Error() {
		t.Fatal("lost retry delay or error identity", err)
	}
}
