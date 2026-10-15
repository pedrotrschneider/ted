package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/riverqueue/river"
)

func TestTVDBEpisodeGuestCreditsUseActorType(t *testing.T) {
	for _, tc := range []struct{ name, label, want string }{
		{"explicit guest star", "GuestStar", "Actor"},
		{"type lookup fallback", "", "Actor"},
		{"unrelated crew", "Showrunner", "Unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creditType := 4
			if tc.want == "Unknown" {
				creditType = 5
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var data any
				switch r.URL.Path {
				case "/login":
					data = map[string]string{"token": "fixture-token"}
				case "/episodes/10/extended":
					data = map[string]any{"id": 10, "seriesId": 73244, "seasonNumber": 1, "number": 1, "name": "Episode", "characters": []any{
						map[string]any{"peopleId": 8, "personName": "Guest performer", "peopleType": tc.label, "type": creditType, "name": "Guest role"},
						map[string]any{"peopleId": 9, "personName": "Writer", "peopleType": "Writer"},
					}}
				case "/artwork/types":
					data = []any{}
				case "/people/types":
					data = []any{map[string]any{"id": 4, "name": "Guest Star"}, map[string]any{"id": 5, "name": "Showrunner"}}
				default:
					t.Errorf("unexpected provider lookup: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
			}))
			defer server.Close()
			p := NewProviders(ProviderConfig{TVDBKey: "fixture-key", Language: "en"})
			p.tvdbURL = server.URL
			doc, err := p.Fetch(context.Background(), "Tvdb", Request{Kind: "episode", TitleID: "tt0386676", ProviderID: "10", ParentProviderID: "73244", Season: 1, Number: 1})
			if err != nil || len(doc.People) != 2 || doc.People[0].Type != tc.want || doc.People[0].Role != "Guest role" || doc.People[1].Type != "Writer" || !doc.TVDBCastTypesChecked {
				t.Fatal("episode credits lost/misclassified", doc, err)
			}
		})
	}
}

func TestTVDBCachedCastRepairCoalescesAndKeepsFailedData(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	req := Request{Kind: "episode", TitleID: "tt0386676", Season: 3, Number: 13}
	v, err := s.Ensure(ctx, "Tvdb", req, Item{Type: "Episode", Name: "Retained episode"})
	if err != nil {
		t.Fatal(err)
	}
	old := Item{ID: v.Item.ID, Name: "Retained episode", Type: "Episode", People: []Person{{ID: guid("fixture-person"), Name: "Retained guest", Type: "Unknown"}}}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET status='ready',applied_generation=generation,item=$2 WHERE id=$1`, v.Item.ID, old); err != nil {
		t.Fatal(err)
	}
	gate, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT id FROM metadata_entities WHERE id=$1 FOR UPDATE`, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 6)
	started := make(chan struct{}, 6)
	for range 6 {
		go func() { started <- struct{}{}; _, e := s.Ensure(ctx, "Tvdb", req, Item{}); done <- e }()
	}
	for range 6 {
		<-started
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	v, err = s.Get(ctx, v.Item.ID)
	if err != nil || v.Status != "queued" || v.Generation != 2 || len(v.Item.People) != 1 {
		t.Fatal(v, err)
	}
	var jobs int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE args->>'id'=$1`, v.Item.ID).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatal("repair was not coalesced", jobs, err)
	}
	err = (&worker{s: s}).Work(ctx, &river.Job[Args]{Args: Args{ID: v.Item.ID, Generation: 2}})
	if !errors.Is(err, ErrProvider) {
		t.Fatal(err)
	}
	v, err = s.Ensure(ctx, "Tvdb", req, Item{})
	if err != nil || v.Status != "failed" || v.Generation != 2 || len(v.Item.People) != 1 || v.Item.People[0].Name != "Retained guest" {
		t.Fatal("failed repair discarded data or looped", v, err)
	}
}

func TestTVDBCastRepairPreservesLocksAndCheckedCaches(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		locked, checked, unknown bool
	}{
		{"locked cast", true, false, true},
		{"already checked unknown crew", false, true, true},
		{"ordinary cached actor", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := metadataFixture(t, &fixtureReader{})
			req := Request{Kind: "episode", TitleID: "tt0386676", Season: 1, Number: 1, TVDBCastTypesChecked: tc.checked}
			v, err := s.Ensure(ctx, "Tvdb", req, Item{Type: "Episode", Name: "Episode"})
			if err != nil {
				t.Fatal(err)
			}
			kind := "Actor"
			if tc.unknown {
				kind = "Unknown"
			}
			item := Item{ID: v.Item.ID, Type: "Episode", Name: "Episode", People: []Person{{ID: guid("fixture-person"), Name: "Person", Type: kind}}}
			if tc.locked {
				item.LockedFields = []string{"Cast"}
			}
			if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET status='ready',applied_generation=generation,item=$2 WHERE id=$1`, v.Item.ID, item); err != nil {
				t.Fatal(err)
			}
			v, err = s.Ensure(ctx, "Tvdb", req, Item{})
			if err != nil || v.Status != "ready" || v.Generation != 1 || v.Item.People[0].Type != kind {
				t.Fatal("cached/locked cast was changed", v, err)
			}
		})
	}
}
