package addons

import (
	"context"
	"entertainmentdistrict/internal/realdebrid"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPreparationDistinguishesTransientCDNFailuresFromInvalidMedia(t *testing.T) {
	for _, mode := range []string{"ordinary", "startup", "progressive"} {
		for _, failure := range []string{"limited", "unavailable", "short", "invalid_range"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				size := int64(8000)
				if mode == "progressive" {
					size = 4 << 20
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch failure {
					case "limited":
						w.Header().Set("Retry-After", "125")
						w.WriteHeader(429)
						return
					case "unavailable":
						w.WriteHeader(503)
						return
					}
					start, end, err := parseRange(r.Header.Get("Range"), size)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if failure == "invalid_range" {
						start++
					}
					w.Header().Set("Content-Type", "video/x-matroska")
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
					w.WriteHeader(206)
					w.Write([]byte{0x1a, 0x45, 0xdf, 0xa3})
				}))
				defer server.Close()
				service := New(nil, nil, server.Client(), nil)
				source := Source{URL: server.URL, VideoSize: size}
				if mode != "ordinary" {
					source.RDFile = &realdebrid.FileRequest{}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, err := service.preparePlayback(ctx, source)
				expected := ErrUnavailable
				if failure == "invalid_range" {
					expected = ErrUnsupported
				}
				if !errors.Is(err, expected) {
					t.Fatalf("got %v, want %v", err, expected)
				}
				if failure == "limited" {
					var delayed interface{ RetryAfter() time.Duration }
					if !errors.As(err, &delayed) || delayed.RetryAfter() != 125*time.Second {
						t.Fatal("lost provider retry delay", err)
					}
				}
			})
		}
	}
}
