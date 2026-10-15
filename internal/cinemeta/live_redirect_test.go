package cinemeta

import (
	"net/http"
	"testing"
)

func TestLiveMetadataRedirectBoundary(t *testing.T) {
	for _, target := range []string{"https://cinemeta-live.strem.io/meta/series/tt15573636.json", "https://v3-cinemeta.strem.io/meta/series/tt15573636.json"} {
		req, _ := http.NewRequest("GET", target, nil)
		if err := catalogRedirect(req, nil); err != nil {
			t.Fatal(target, err)
		}
		if err := catalogRedirect(req, make([]*http.Request, 3)); err == nil {
			t.Fatal("redirect bound lost")
		}
	}
	for _, target := range []string{"http://cinemeta-live.strem.io/a", "https://cinemeta-live.strem.io.attacker.example/a", "https://user:secret@cinemeta-live.strem.io/a"} {
		req, _ := http.NewRequest("GET", target, nil)
		if err := catalogRedirect(req, nil); err == nil {
			t.Fatal("unsafe redirect accepted", target)
		}
	}
}
