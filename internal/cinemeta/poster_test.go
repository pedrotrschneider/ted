package cinemeta

import "testing"

func TestPosterURLLimitsArtworkHosts(t *testing.T) {
	for _, v := range []string{"https://images.metahub.space/poster/medium/tt2934286/img", "https://image.tmdb.org/t/p/w500/image.jpg"} {
		if PosterURL(v) != v {
			t.Fatal("safe artwork rejected")
		}
	}
	for _, v := range []string{"http://images.metahub.space/image", "https://images.metahub.space.attacker.example/image", "https://token@images.metahub.space/image", "https://images.metahub.space:8080/image", "javascript:alert(1)", "data:image/png;base64,AAAA", "https://localhost/image"} {
		if PosterURL(v) != "" {
			t.Fatal("untrusted artwork accepted")
		}
	}
}
