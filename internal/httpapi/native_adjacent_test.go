package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"testing"
	"time"
)

func TestNativeAdjacentEpisodesUseCanonicalTuplesAndReleasedServerDay(t *testing.T) {
	f := setup(t)
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt1234567", Type: "series", Name: "Fixture"}}
	for _, item := range []struct {
		id             string
		season, number int
		date           string
	}{
		{"tt1234567:0:1", 0, 1, "2020-01-01"},
		{"tt1234567:1:2", 1, 2, "2020-01-01"},
		{"tt1234567:1:4", 1, 4, "2020-01-02"},
		{"tt1234567:2:1", 2, 1, "2020-01-03"},
		{"tt1234567:2:2", 2, 2, ""},
		{"tt1234567:2:3", 2, 3, "2099-01-01"},
	} {
		show.Episodes = append(show.Episodes, cinemeta.Episode{ID: item.id, Season: item.season, Number: item.number, Name: item.id, ReleaseDate: item.date})
	}
	if err := f.api.catalog.SaveSchedule(context.Background(), show, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	for _, test := range []struct{ id, previous, next string }{
		{"tt1234567:1:2", "", "tt1234567:1:4"},
		{"tt1234567:1:4", "tt1234567:1:2", "tt1234567:2:1"},
		{"tt1234567:2:1", "tt1234567:1:4", ""},
		{"tt1234567:0:1", "", ""},
	} {
		r := nativeCall(t, h, "GET", "/api/client/v1/titles/episode/"+test.id+"/adjacent", pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		var page struct{ Previous, Next *nativeTitleDetail }
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		id := func(value *nativeTitleDetail) string {
			if value == nil {
				return ""
			}
			return value.Media.ID
		}
		if id(page.Previous) != test.previous || id(page.Next) != test.next {
			t.Fatalf("%s: %+v", test.id, page)
		}
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/episode/tt1234567:9:9/adjacent", pair.AccessToken, nil, nil), 404)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/episode/tt1234567:1:2/adjacent", "", nil, nil), 401)
	var effects int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM media_mappings)+(SELECT count(*) FROM watch_history)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal(effects, err)
	}
}
