package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"strings"
	"testing"
)

type failingNativeSeries struct {
	seriesStub
	err error
}

func (s failingNativeSeries) Series(context.Context, string) (cinemeta.Series, error) {
	return cinemeta.Series{}, s.err
}
func (s failingNativeSeries) BrowseSeries(context.Context, string, int, cinemeta.Filters) (cinemeta.SeriesPage, error) {
	return cinemeta.SeriesPage{}, s.err
}

func TestNativeDiscoveryHierarchyOutageAndNoLibraryEffects(t *testing.T) {
	f := setup(t)
	f.api.movies = &movieStub{}
	f.api.series = seriesStub{count: 51}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/catalog/movie", "", nil, nil), 401)
	for _, path := range []string{"/api/client/v1/catalog/movie", "/api/client/v1/catalog/series", "/api/client/v1/titles/movie/tt1345836", "/api/client/v1/titles/series/tt1245698"} {
		requireStatus(t, nativeCall(t, h, "GET", path, pair.AccessToken, nil, nil), 200)
	}
	first := nativeCall(t, h, "GET", "/api/client/v1/titles/series/tt1245698/episodes?season=1", pair.AccessToken, nil, nil)
	requireStatus(t, first, 200)
	var page struct {
		Titles []nativeTitleDetail `json:"titles"`
		Next   *int                `json:"next_after"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || len(page.Titles) != 50 || page.Next == nil || *page.Next != 50 || page.Titles[0].Parent == nil || page.Titles[0].Ready {
		t.Fatal(page, err)
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/series/tt1245698/episodes?season=1&after=50", pair.AccessToken, nil, nil), 200)
	var effects int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM catalog_media)+(SELECT count(*) FROM catalog_series)+(SELECT count(*) FROM media_mappings)+(SELECT count(*) FROM watch_current)+(SELECT count(*) FROM watch_history)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal("discovery created library/history", effects, err)
	}
	f.api.movies = &movieStub{err: cinemeta.ErrUnavailable}
	f.api.series = failingNativeSeries{err: cinemeta.ErrIncomplete}
	for _, path := range []string{"/api/client/v1/titles/movie/tt1345836", "/api/client/v1/titles/series/tt1245698", "/api/client/v1/titles/episode/tt1245698:1:1"} {
		r := nativeCall(t, h, "GET", path, pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		if !strings.Contains(r.Body.String(), `"metadata_status":"cached"`) {
			t.Fatal("outage discarded cache", r.Body.String())
		}
	}
	home := nativeCall(t, h, "GET", "/api/client/v1/home", pair.AccessToken, nil, nil)
	requireStatus(t, home, 200)
	if strings.Count(home.Body.String(), `"unavailable":true`) != 2 || !strings.Contains(home.Body.String(), `"catalog_incomplete"`) {
		t.Fatal("failed discovery became successful empty", home.Body.String())
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt8888888", pair.AccessToken, nil, nil), 502)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/rows/next-up?after=invalid", pair.AccessToken, nil, nil), 400)
}

func TestNativeDiscoveryRechecksRevocationAfterProviderRead(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	f.api.movies = &movieStub{during: func() {
		grant, err := f.api.NativeAuth.Authenticate(context.Background(), pair.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.api.NativeAuth.Logout(context.Background(), grant, false); err != nil {
			t.Fatal(err)
		}
	}}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/catalog/movie", pair.AccessToken, nil, nil), 401)
}
