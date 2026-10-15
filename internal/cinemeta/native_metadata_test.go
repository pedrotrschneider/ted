package cinemeta

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestCatalogOptionalArtworkRuntimeAndEpisodeOverview(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"meta":{"id":"tt1245698","type":"series","name":"Show","background":"https://images.metahub.space/background.jpg","logo":"https://images.metahub.space/logo.png","runtime":"45 min","videos":[{"id":"tt1245698:1:1","season":1,"episode":1,"overview":"Episode overview","thumbnail":"https://images.metahub.space/episode.jpg"}]}}`)
	})
	show, err := c.Series(context.Background(), "tt1245698")
	if err != nil || show.Background == "" || show.Logo == "" || show.Runtime != "45 min" || show.Episodes[0].Description != "Episode overview" || show.Episodes[0].Image == "" {
		t.Fatal(show, err)
	}
}
func TestCatalogOptionalUnsafeArtworkIsOmittedWithoutHidingTitle(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"meta":{"id":"tt1234567","type":"movie","name":"Movie","background":"http://127.0.0.1/private","logo":"https://secret@images.metahub.space/logo.png"}}`)
	})
	movie, err := c.Movie(context.Background(), "tt1234567")
	if err != nil || movie.Name != "Movie" || movie.Background != "" || movie.Logo != "" {
		t.Fatal(movie, err)
	}
}
