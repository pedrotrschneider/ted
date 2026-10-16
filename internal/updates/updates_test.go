package updates

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }
func TestVersionOrdering(t *testing.T) {
	for _, v := range []struct {
		a, b string
		want bool
	}{
		{"1.2.10", "1.2.9", true}, {"1.2.0-rc10", "1.2.0-rc2", true},
		{"1.2.0", "1.2.0-rc10", true}, {"1.2.0-rc10", "1.2.0", false},
		{"2.0.0", "1.9.9", true}, {"1.2.0", "1.2.0", false}, {"1.0.0", "0.1.0-dev", false},
		{"bad", "1.0.0", false}, {"1.01.0", "1.0.0", false},
	} {
		if got := Newer(v.a, v.b); got != v.want {
			t.Errorf("%s > %s: %v", v.a, v.b, got)
		}
	}
}
func TestPublishedImageAndCache(t *testing.T) {
	calls := 0
	digest := "sha256:" + strings.Repeat("a", 64)
	c := NewChecker()
	c.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := ""
		switch {
		case r.URL.Path == "/token":
			body = `{"token":"public-pull-token"}`
		case r.URL.Path == "/v2/pedrotrschneider/ted/manifests/latest":
			body = `{"manifests":[{"digest":"` + digest + `","platform":{"os":"linux","architecture":"amd64"}}]}`
		case strings.Contains(r.URL.Path, "/manifests/sha256:"):
			body = `{"config":{"digest":"` + digest + `"}}`
		case strings.Contains(r.URL.Path, "/blobs/"):
			body = `{"config":{"Labels":{"org.opencontainers.image.version":"0.1.9"}}}`
		default:
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	for range 2 {
		v, err := c.Version(context.Background(), "0.1.0")
		if err != nil || v != "0.1.9" {
			t.Fatal(v, err)
		}
	}
	if calls != 4 {
		t.Fatalf("expected cached lookup, got %d calls", calls)
	}
}
func TestFailedChecksBackOff(t *testing.T) {
	c := NewChecker()
	calls := 0
	c.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for range 2 {
		if _, err := c.Version(context.Background(), "0.1.0"); err == nil {
			t.Fatal("failure became success")
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
