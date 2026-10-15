package addons

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamLookupIdentifiesTEDAndPreservesProviderFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.UserAgent() != addonUserAgent {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"streams":[{"url":"https://cdn.example/episode.mkv"},{"infoHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","fileIdx":0},{"externalUrl":"https://example.com/page"}]}`))
			}))
			defer server.Close()
			d := Descriptor{Transport: server.URL + "/manifest.json"}
			s := &Service{http: server.Client()}
			sources, unsupported, err := s.fetchStreams(context.Background(), d, "episode", "tt0386676:1:4")
			if status == http.StatusOK {
				if err != nil || len(sources) != 2 || unsupported != 1 || sources[1].InfoHash == "" {
					t.Fatal("identified client lost supported/unsupported results", err, len(sources), unsupported)
				}
			} else if !errors.Is(err, ErrUnavailable) || len(sources) != 0 {
				t.Fatal("provider failure became successful lookup", err)
			}
		})
	}
}

func TestMediaAndSubtitleRequestsIdentifyTEDWithoutReplacingProviderHeaders(t *testing.T) {
	for _, configured := range []bool{false, true} {
		headers := map[string]string{"Authorization": "provider-only"}
		want := addonUserAgent
		if configured {
			headers["User-Agent"] = "Provider-required-client"
			want = headers["User-Agent"]
		}
		for _, byteRange := range []string{"bytes=0-4095", ""} {
			req, err := sourceRequest(context.Background(), Source{URL: "https://cdn.example/file", Headers: headers}, byteRange)
			if err != nil || req.UserAgent() != want || req.Header.Get("Authorization") != "provider-only" || req.Header.Get("Range") != byteRange || req.Header.Get("Accept-Encoding") != "identity" {
				t.Fatal("required file headers changed", err)
			}
		}
	}
}
