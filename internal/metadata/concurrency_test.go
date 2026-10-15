package metadata

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Held external operations make both River queues' actual database-backed
// scheduling observable without timing guesses about provider response speed.
func exerciseFiveWorkers(t *testing.T, s *Service, queue string, entered <-chan struct{}, release chan struct{}) {
	t.Helper()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		unblock()
		cancel()
		stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := s.Stop(stop); err != nil {
			t.Error(err)
		}
	})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for range 5 {
		select {
		case <-entered:
		case <-deadline.C:
			t.Fatal("five workers did not enter held external operations")
		}
	}
	var running, available int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='running'),count(*) FILTER (WHERE state='available') FROM river_job WHERE queue=$1`, queue).Scan(&running, &available); err != nil {
		t.Fatal(err)
	}
	if running != 5 || available != 1 {
		t.Fatalf("held queue %s: running=%d available=%d; want five active and one queued", queue, running, available)
	}
	unblock()
	select {
	case <-entered:
	case <-deadline.C:
		t.Fatal("queued sixth operation did not start after a slot was released")
	}
	for {
		var completed int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE queue=$1 AND state='completed'`, queue).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed == 6 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("not all six durable operations completed", completed)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestMetadataQueueRunsFiveHeldProviderRequests(t *testing.T) {
	entered, release := make(chan struct{}, 6), make(chan struct{})
	s := metadataFixture(t, &fixtureReader{fetch: func(ctx context.Context, _ string, r Request) (Document, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return Document{ProviderID: r.TitleID[2:], Item: Item{Name: "Enriched movie"}}, nil
		case <-ctx.Done():
			return Document{}, ctx.Err()
		}
	}})
	for n := range 6 {
		if _, err := s.Ensure(context.Background(), "Tmdb", Request{Kind: "movie", TitleID: fmt.Sprintf("tt%07d", 1345836+n)}, Item{Type: "Movie", Name: "Cinemeta"}); err != nil {
			t.Fatal(err)
		}
	}
	exerciseFiveWorkers(t, s, "native_metadata", entered, release)
}

func TestArtworkQueueRunsFiveHeldTransfers(t *testing.T) {
	entered, release := make(chan struct{}, 6), make(chan struct{})
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write(data.Bytes())
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	s := metadataFixture(t, &fixtureReader{fetch: func(_ context.Context, _ string, r Request) (Document, error) {
		return Document{ProviderID: r.TitleID[2:], Item: Item{Name: "Movie"}, Images: []Image{{Type: "Primary", URL: "https://artworks.thetvdb.com/image.png"}}}, nil
	}})
	s.imageHTTP = &http.Client{Transport: rewriteTransport{base: server.Listener.Addr().String(), transport: http.DefaultTransport}, Timeout: 20 * time.Second}
	for n := range 6 {
		v, err := s.Ensure(context.Background(), "Tmdb", Request{Kind: "movie", TitleID: fmt.Sprintf("tt%07d", 1345836+n)}, Item{Type: "Movie", Name: "Cinemeta"})
		if err != nil {
			t.Fatal(err)
		}
		runEntity(t, s, v.Item.ID)
		ready, err := s.Get(context.Background(), v.Item.ID)
		if err != nil || len(ready.Images) != 1 {
			t.Fatal(ready, err)
		}
		if _, err = s.RequestImage(context.Background(), ready.Images[0].ID, false); err != nil {
			t.Fatal(err)
		}
	}
	exerciseFiveWorkers(t, s, "native_artwork", entered, release)
}
