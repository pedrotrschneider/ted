package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLookupAllowsSlowValidProviderAndHonorsCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			s, auth, grant, remote, _ := fixture(t)
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if cancelled {
					<-r.Context().Done()
					return
				}
				// Reproduce a successful connection/response just beyond the old cap.
				timer := time.NewTimer(4200 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
				w.Write([]byte(`{"streams":[{"url":"https://cdn.example/episode.mkv"}]}`))
			}))
			defer server.Close()
			s.http = server.Client()
			remote.collection = []json.RawMessage{descriptorRaw(server.URL)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				go func() { <-entered; cancel() }()
			}
			page, err := s.SelectedSources(ctx, auth, grant, mediaIdentity("episode", "tt1234567:1:4"), "")
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("caller cancellation lost", err)
				}
				var count int
				if e := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM native_addon_sources`).Scan(&count); e != nil || count != 0 {
					t.Fatal("cancelled lookup published sources", e, count)
				}
			} else if err != nil || len(page.Sources) != 1 || len(page.Issues) != 0 {
				t.Fatal("valid slow source rejected", err, len(page.Sources), page.Issues)
			}
		})
	}
}

func TestPreferredLookupsEnterBeforeSlowUnrelatedAddons(t *testing.T) {
	for _, mode := range []string{"continuation", "favorite", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, auth, grant, remote, _ := fixture(t)
			ctx := context.Background()
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var mu sync.Mutex
			active, peak, selectedCalls := 0, 0, 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				defer func() { mu.Lock(); active--; mu.Unlock() }()
				chosen := strings.HasPrefix(r.URL.Path, "/chosen/")
				if chosen {
					mu.Lock()
					selectedCalls++
					mu.Unlock()
					select {
					case entered <- struct{}{}:
					default:
					}
				} else {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{
					"url": server.URL + r.URL.Path + "/file.mp4", "name": "Direct pack",
					"behaviorHints": map[string]any{"bingeGroup": "pack", "filename": "episode.mp4"},
				}}})
			}))
			defer server.Close()
			s.http = server.Client()
			var descriptors []Descriptor
			var wire []json.RawMessage
			for i := 0; i < 6; i++ {
				d, err := parseDescriptor(descriptorRaw(server.URL))
				if err != nil {
					t.Fatal(err)
				}
				prefix := fmt.Sprintf("slow-%d", i)
				if i == 5 {
					prefix = "chosen"
				}
				d.Manifest.ID = prefix
				d.Manifest.Name = prefix
				d.Transport = server.URL + "/" + prefix + "/manifest.json"
				raw, _ := json.Marshal(d)
				wire = append(wire, raw)
				descriptors = append(descriptors, d)
			}
			setCollection := func(values []json.RawMessage) {
				remote.mu.Lock()
				remote.collection = values
				remote.mu.Unlock()
				if _, err := s.Collection(ctx, grant.User.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			media := mediaIdentity("episode", "tt1234567:1:2")
			if mode == "favorite" {
				setCollection(wire[5:])
				first, err := s.SelectedSources(ctx, auth, grant, mediaIdentity("episode", "tt1234567:1:1"), "")
				if err != nil || len(first.Sources) != 1 {
					t.Fatal("favorite seed", err)
				}
				if _, err = s.SetFavorite(ctx, auth, grant, first.Sources[0].ID, true); err != nil {
					t.Fatal(err)
				}
				<-entered
			}
			setCollection(wire)
			if mode == "disabled" {
				actor := accounts.Identity{ID: grant.User.ID, Version: grant.User.Version}
				if _, err := s.UpdatePolicy(ctx, actor, descriptors[5].PolicyKey(), false, 0); err != nil {
					t.Fatal(err)
				}
			}
			result := make(chan SourcePage, 1)
			failures := make(chan error, 1)
			go func() {
				preferred := descriptors[5].Key()
				if mode == "favorite" {
					preferred = ""
				}
				page, err := s.SelectedSourcesPrioritizing(ctx, auth, grant, media, "", preferred)
				if err != nil {
					failures <- err
					return
				}
				result <- page
			}()
			if mode != "disabled" {
				select {
				case <-entered:
				case err := <-failures:
					t.Fatal(err)
				case <-time.After(time.Second):
					t.Fatal("preferred add-on starved behind held unrelated lookups")
				}
			}
			unblock()
			var page SourcePage
			select {
			case page = <-result:
			case err := <-failures:
				t.Fatal(err)
			case <-time.After(2 * time.Second):
				t.Fatal("lookup did not settle")
			}
			mu.Lock()
			maxActive, calls := peak, selectedCalls
			mu.Unlock()
			if maxActive > 4 {
				t.Fatal("lookup concurrency exceeded four", maxActive)
			}
			expected := 6
			if mode == "disabled" {
				expected = 5
				if calls != 0 {
					t.Fatal("preferred disabled add-on was queried")
				}
			}
			if len(page.Sources) != expected {
				t.Fatal("complete source list changed", len(page.Sources), expected)
			}
			if mode == "favorite" && !page.Sources[0].Favorite {
				t.Fatal("favorite not retained across the episode group")
			}
		})
	}
}
