package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testProvider(t *testing.T, handler http.HandlerFunc) *provider {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	p := &provider{client: upstream.Client(), upstream: upstream.URL, data: t.TempDir(), public: "http://127.0.0.1:42390", internal: "http://provider:42390", states: map[string]obj{}}
	p.media = filepath.Join(p.data, "demo.mp4")
	if err := os.WriteFile(p.media, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProxyProtocolCatalogAndErrors(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "Fail") {
			http.Error(w, "unavailable", 503)
			return
		}
		if strings.Contains(r.URL.Path, "Broken") {
			io.WriteString(w, `{"metas":`)
			return
		}
		if strings.Contains(r.URL.Path, "Batman") {
			io.WriteString(w, `{"metas":[{"id":"tt0372784","name":"Batman Begins"}]}`)
			return
		}
		io.WriteString(w, `{"metas":[{"id":"tt0133093","name":"The Matrix"}]}`)
	})
	for _, accept := range []string{"application/json", "application/xml"} {
		r := httptest.NewRequest("GET", "/provider/library/sections/movies/all", nil)
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		p.serve(w, r)
		if w.Code != 200 || w.Header().Get("Content-Length") == "" {
			t.Fatalf("framing/status: %d %v", w.Code, w.Header())
		}
		if accept == "application/json" {
			var response struct{ MediaContainer struct{ Metadata []obj } }
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.MediaContainer.Metadata) != 2 {
				t.Fatal("catalog did not include fixture and remote title")
			}
			if response.MediaContainer.Metadata[1]["Media"] != nil {
				t.Fatal("unmapped catalog title advertised a source")
			}
		} else {
			var response struct {
				XMLName xml.Name
				Videos  []struct {
					Title string `xml:"title,attr"`
				} `xml:"Video"`
			}
			if err := xml.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Videos) != 2 {
				t.Fatalf("XML: %v %+v", err, response)
			}
		}
	}
	for _, query := range []string{"Fail", "Broken"} {
		w := httptest.NewRecorder()
		p.serve(w, httptest.NewRequest("GET", "/provider/hubs/search?query="+query, nil))
		if w.Code != 502 {
			t.Fatalf("%s became successful empty catalog: %d", query, w.Code)
		}
	}
	items, err := p.catalog(context.Background(), "movie", "Batman")
	if err != nil || len(items) != 1 || items[0]["title"] != "Batman Begins" {
		t.Fatalf("search: %v %v", items, err)
	}
}

func TestFixtureRangesAndSourceIdentity(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"metas":[]}`) })
	r := httptest.NewRequest("GET", "/provider/stream/demo.mp4", nil)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	p.serve(w, r)
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range: %d %s %v", w.Code, w.Body, w.Header())
	}
	fixture := p.fixture("demo-movie")
	media := fixture["Media"].([]obj)
	part := media[0]["Part"].([]obj)[0]
	if part["key"] != "/stream/demo.mp4" || fixture["ratingKey"] != "demo-movie" {
		t.Fatal("source/key cannot be safely proxied")
	}
}

func TestRemoteEpisodeIdentity(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"meta":{"id":"tt1234","name":"Example show","videos":[{"title":"Pilot","season":1,"episode":1},{"title":"Second","season":1,"episode":2}]}}`)
	})
	items, err := p.metadata(context.Background(), "episode-tt1234-1-2")
	if err != nil || len(items) != 1 || items[0]["type"] != "episode" || items[0]["title"] != "Second" || items[0]["Media"] != nil {
		t.Fatalf("episode detail: %v %v", items, err)
	}
	if _, err := p.metadata(context.Background(), "episode-tt1234-1-3"); err == nil {
		t.Fatal("absent episode returned as a show")
	}
	items, err = p.metadata(context.Background(), "demo-movie/related")
	if err != nil || len(items) != 0 {
		t.Fatalf("optional related metadata: %v %v", items, err)
	}
}

func TestWatchStatePersistenceValidationAndConcurrentReads(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"metas":[]}`) })
	write := func(path string) int {
		w := httptest.NewRecorder()
		p.serve(w, httptest.NewRequest("PUT", path, nil))
		return w.Code
	}
	if write("/provider/:/timeline?ratingKey=demo-movie&time=21000&state=paused") != 200 {
		t.Fatal("timeline write failed")
	}
	b, err := os.ReadFile(filepath.Join(p.data, "watch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]obj
	if err := json.Unmarshal(b, &restored); err != nil || number(restored["demo-movie"]["viewOffset"]) != 21000 {
		t.Fatalf("state persistence: %v %s", err, b)
	}
	if write("/:/timeline?ratingKey=demo-movie&time=999999") != 400 || write("/:/scrobble?ratingKey=movie-tt0133093") != 400 {
		t.Fatal("invalid writes accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = p.fixture("demo-movie")
				write("/:/timeline?ratingKey=demo-movie&time=21000&state=paused")
			}
		}()
	}
	wg.Wait()
	if write("/:/scrobble?ratingKey=demo-movie") != 200 || number(p.fixture("demo-movie")["viewCount"]) != 1 {
		t.Fatal("watched fixture did not update")
	}
}
