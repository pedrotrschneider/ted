package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"net/http"
	"strings"
	"testing"
	"time"
)

func nativeSeriesRead(t *testing.T, h http.Handler, token, path string) nativeTitleDetail {
	t.Helper()
	r := nativeCall(t, h, "GET", path, token, nil, nil)
	requireStatus(t, r, 200)
	var title nativeTitleDetail
	if path == "/api/client/v1/home" {
		var home struct {
			Rows []struct {
				ID     string
				Titles []nativeTitleDetail
			}
		}
		if err := json.Unmarshal(r.Body.Bytes(), &home); err != nil {
			t.Fatal(err)
		}
		for _, row := range home.Rows {
			if row.ID == "popular-series" && len(row.Titles) == 1 {
				return row.Titles[0]
			}
		}
		t.Fatal("missing popular series", r.Body.String())
	} else if strings.HasPrefix(path, "/api/client/v1/catalog/") {
		var page nativeCatalogPage
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Titles) != 1 {
			t.Fatal(page, err)
		}
		return page.Titles[0]
	}
	if err := json.Unmarshal(r.Body.Bytes(), &title); err != nil {
		t.Fatal(err)
	}
	return title
}

func nativeSeriesWatchedFixture(t *testing.T) (*fixture, cinemeta.Series) {
	t.Helper()
	f := setup(t)
	f.api.series = seriesStub{}
	f.api.movies = &movieStub{}
	ctx := context.Background()
	show, _ := seriesStub{}.Series(ctx, "tt1245698")
	if err := f.api.rememberWatchSeries(ctx, identity(f.admin), show); err != nil {
		t.Fatal(err)
	}
	for _, e := range show.Episodes {
		_, err := f.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true,"play_count":1}', 'TEDLocal',now() FROM catalog_media WHERE cinemeta_id=$2`, f.admin.ID, e.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f, show
}

func TestNativeSeriesCompletionMatchesBrowserAcrossDiscoveryAndDetails(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	browser := posterRead(t, f, login(t, h, "admin"), "series", show.ID)
	for _, path := range []string{"/api/client/v1/catalog/series", "/api/client/v1/catalog/series?search=Birdy", "/api/client/v1/home", "/api/client/v1/titles/series/" + show.ID} {
		title := nativeSeriesRead(t, h, pair.AccessToken, path)
		if !title.State.Played || title.EpisodeCounts == nil || title.EpisodeCounts.Total != browser.Available || title.EpisodeCounts.Watched != browser.Watched || title.EpisodeCounts.Total != 2 {
			t.Fatal(path, title)
		}
	}
	var seriesStates int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE c.kind='series'`).Scan(&seriesStates); err != nil || seriesStates != 0 {
		t.Fatal("completion invented durable series state", seriesStates, err)
	}
}

func TestNativeSeriesCompletionTracksCurrentStateAndUser(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE watch_current SET state='{"played":false,"position_ticks":70000000,"play_count":1}' WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, show.Episodes[1].ID); err != nil {
		t.Fatal(err)
	}
	title := nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/catalog/series")
	if title.State.Played || title.EpisodeCounts == nil || title.EpisodeCounts.Watched != 1 || title.EpisodeCounts.Total != 2 {
		t.Fatal("partial watch classified as complete", title)
	}
	if _, err := f.service.Create(ctx, identity(f.admin), "other", "Other", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	r := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "other", "password": testPassword, "device_id": "other-device", "device_name": "Test"}, nil)
	requireStatus(t, r, 200)
	var other accounts.NativeTokens
	if err := json.Unmarshal(r.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	title = nativeSeriesRead(t, h, other.AccessToken, "/api/client/v1/titles/series/"+show.ID)
	if title.State.Played || title.EpisodeCounts == nil || title.EpisodeCounts.Watched != 0 || title.EpisodeCounts.Total != 2 {
		t.Fatal("another user's completion leaked", title)
	}
}

func TestNativeSeriesCompletionExcludesSpecialsFutureAndUnknownSchedules(t *testing.T) {
	f, show := nativeSeriesWatchedFixture(t)
	ctx := context.Background()
	show.Episodes = append(show.Episodes,
		cinemeta.Episode{ID: show.ID + ":0:1", Season: 0, Number: 1, ReleaseDate: "2008-01-01"},
		cinemeta.Episode{ID: show.ID + ":1:3", Season: 1, Number: 3, ReleaseDate: "9999-01-01"},
		cinemeta.Episode{ID: show.ID + ":1:4", Season: 1, Number: 4})
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	title := nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/catalog/series")
	if !title.State.Played || title.EpisodeCounts == nil || title.EpisodeCounts.Total != 2 || title.EpisodeCounts.Watched != 2 {
		t.Fatal("unreleased/specials prevent completion", title)
	}
	// Failed provider reads retain the successful schedule and its watch counts.
	f.api.series = failingNativeSeries{err: cinemeta.ErrIncomplete}
	title = nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/titles/series/"+show.ID)
	if !title.State.Played || title.EpisodeCounts == nil || title.EpisodeCounts.Total != 2 || title.MetadataStatus != "cached" {
		t.Fatal("provider failure discarded completion", title)
	}
	f.api.series = seriesStub{}
	if _, err := f.pool.Exec(ctx, `DELETE FROM catalog_episode_schedules WHERE title_id=$1`, show.ID); err != nil {
		t.Fatal(err)
	}
	title = nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/catalog/series")
	if title.State.Played || title.EpisodeCounts != nil {
		t.Fatal("unknown schedule invented completion", title)
	}
	show.Episodes = show.Episodes[2:]
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	title = nativeSeriesRead(t, h, pair.AccessToken, "/api/client/v1/catalog/series")
	if title.State.Played || title.EpisodeCounts == nil || *title.EpisodeCounts != (nativeEpisodeCounts{}) {
		t.Fatal("zero eligible episodes classified as complete", title)
	}
}
