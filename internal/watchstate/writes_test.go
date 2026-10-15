package watchstate

import (
	"context"
	"entertainmentdistrict/internal/accounts"
	"testing"
)

func current(t *testing.T, s *Store, who accounts.Identity) Current {
	t.Helper()
	h, err := s.History(context.Background(), who, 0)
	if err != nil || len(h.Current) != 1 {
		t.Fatalf("state: %+v %v", h, err)
	}
	return h.Current[0]
}
