package watchstate

import (
	"context"
	"errors"
	"testing"
)

func TestStremioPollDoesNotInventViewingDate(t *testing.T) {
	s, who := stremioFixture(t)
	observeFlag(t, s, who.ID, true)
	h, err := s.History(context.Background(), who, 0)
	if err != nil || len(h.Entries) != 1 || h.Entries[0].Kind != "stremio_observed" || h.Entries[0].At.IsZero() || h.Entries[0].State.LastPlayed != nil || h.Current[0].State.LastPlayed != nil {
		t.Fatal(h, err)
	}
}

func TestWatchDateModeValidation(t *testing.T) {
	s, who := fixture(t)
	v := readTitle(t, s, who, "tt1234567")
	ctx := context.Background()
	for _, mode := range []string{"yesterday", "NOW"} {
		if err := s.SetTitle(ctx, who, "tt1234567", v.Version, "", nil, true, mode); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.SetTitle(ctx, who, "tt1234567", v.Version, "", nil, false, "now"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Edit(ctx, who, mediaID, WriteRequest{Revision: 1, Action: "reset", DateMode: "undated"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if len(readTitle(t, s, who, "tt1234567").Items) != 1 {
		t.Fatal("unexpected catalog mutation")
	}
}
