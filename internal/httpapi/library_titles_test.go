package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"entertainmentdistrict/internal/cinemeta"
)

func libraryIDs(t *testing.T, f *fixture, cookie *http.Cookie, query string) []string {
	t.Helper()
	r := call(t, f.api.Handler(), "GET", "/api/library-titles?"+query, nil, cookie)
	requireStatus(t, r, 200)
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("personal result is cacheable")
	}
	var page struct {
		Titles []cinemeta.Movie `json:"titles"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, title := range page.Titles {
		ids = append(ids, title.ID)
	}
	return ids
}
func TestLibraryTitlesWatchedIntersectionAndIsolation(t *testing.T) {
	f, exec := posterFixture(t)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	first := posterMedia(t, f, exec, "tt1000001", "movie", true)
	posterMedia(t, f, exec, "tt1000002", "movie", true)
	exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt1000003','movie','{}')`)
	exec(`UPDATE catalog_media SET metadata=jsonb_build_object('id',cinemeta_id,'type','movie','name',CASE WHEN cinemeta_id='tt1000001' THEN 'Batman' ELSE 'Other' END,'year','2012','description','','poster','','genres',CASE WHEN cinemeta_id='tt1000001' THEN '["Action"]'::jsonb ELSE '["Comedy"]'::jsonb END)`)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":1,"play_count":0}','PlaybackProgress',now())`, f.admin.ID, first)
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT $1,id,'playback_started','{"played":true}',now() FROM catalog_media WHERE cinemeta_id='tt1000003'`, f.admin.ID)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"saved=true", []string{"tt1000001", "tt1000002"}},
		{"watched=true", []string{"tt1000001", "tt1000003"}},
		{"saved=true&watched=true", []string{"tt1000001"}},
		{"watched=true&genre=Comedy", []string{"tt1000003"}},
		{"saved=true&search=BATMAN", []string{"tt1000001"}},
		{"watched=true&after=tt1000001", []string{"tt1000003"}},
	} {
		if got := libraryIDs(t, f, cookie, "kind=movie&"+tc.query); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want)
		}
	}
	if _, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	other := login(t, h, "viewer")
	if got := libraryIDs(t, f, other, "kind=movie&watched=true"); len(got) != 0 {
		t.Fatal("another user's viewing leaked", got)
	}
	if got := libraryIDs(t, f, other, "kind=movie&saved=true"); len(got) != 2 {
		t.Fatal("saved list unavailable to regular user", got)
	}
	for _, id := range []string{"tt1000001", "tt1000003"} {
		if !posterRead(t, f, cookie, "movie", id).Seen {
			t.Fatal("list differs from eye", id)
		}
	}
	requireStatus(t, call(t, h, "GET", "/api/library-titles?kind=movie&watched=true", nil, nil), 401)
	for _, query := range []string{"kind=movie", "kind=bad&saved=true", "kind=movie&watched=true&after=bad", "kind=movie&saved=true&genre=bad", "kind=movie&saved=true&watched=bad"} {
		requireStatus(t, call(t, h, "GET", "/api/library-titles?"+query, nil, cookie), 400)
	}
}
func TestLibraryTitlesSeriesStartedAndResetHistory(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"id":"tt1245698","type":"series","name":"Birdy","year":"2008","description":"","poster":"","genres":["Animation"],"episode_count":2}')`)
	first := posterMedia(t, f, exec, "tt1245698:1:1", "episode", true)
	posterMedia(t, f, exec, "tt1245698:1:2", "episode", true)
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,'playback_started','{"played":false,"position_ticks":1}',now())`, f.admin.ID, first)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":0,"play_count":0}','TogglePlayed',now())`, f.admin.ID, first)
	if got := libraryIDs(t, f, cookie, "kind=series&saved=true&watched=true&genre=Animation"); !reflect.DeepEqual(got, []string{"tt1245698"}) {
		t.Fatal(got)
	}
	if w := posterRead(t, f, cookie, "series", "tt1245698"); !w.Seen || w.Watched != 0 {
		t.Fatal(w)
	}
	if got := libraryIDs(t, f, cookie, "kind=movie&watched=true"); len(got) != 0 {
		t.Fatal("episodes became movie titles", got)
	}
}

func TestLibraryTitlesImportedShowWithoutChannelMembership(t *testing.T) {
	f, exec := posterFixture(t)
	exec(`INSERT INTO watch_title_catalog VALUES('tt2934286','series','["tt2934286:1:1","tt2934286:1:2"]',2)`)
	exec(`INSERT INTO catalog_episode_schedules VALUES('tt2934286','[{"id":"tt2934286:1:1","season":1,"release_date":"2022-01-01"},{"id":"tt2934286:1:2","season":1,"release_date":"2022-01-08"}]',now(),now())`)
	cookie := login(t, f.api.Handler(), "admin")
	exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt2934286:1:1','episode','{"name":"Show · S1 E1","series":{"id":"tt2934286","type":"series","name":"Show","year":"","description":"","poster":"https://images.metahub.space/poster/medium/tt2934286/img","genres":[]}}')`)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true,"position_ticks":0,"play_count":0,"last_played":null}','StremioImport',now() FROM catalog_media WHERE cinemeta_id='tt2934286:1:1'`, f.admin.ID)
	if got := libraryIDs(t, f, cookie, "kind=series&watched=true"); !reflect.DeepEqual(got, []string{"tt2934286"}) {
		t.Fatal(got)
	}
	if got := libraryIDs(t, f, cookie, "kind=series&saved=true"); len(got) != 0 {
		t.Fatal("history created membership", got)
	}
	if got := libraryIDs(t, f, cookie, "kind=movie&watched=true"); len(got) != 0 {
		t.Fatal("episode became movie", got)
	}
	if w := posterRead(t, f, cookie, "series", "tt2934286"); !w.Seen || w.Watched != 1 || w.Available != 2 {
		t.Fatal(w)
	}
	// A later explicitly categorized catalog title takes precedence without a duplicate.
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt2934286','anime','{"id":"tt2934286","type":"series","name":"Updated show","year":"","description":"","poster":"","genres":["Animation"]}')`)
	if got := libraryIDs(t, f, cookie, "kind=series&watched=true&genre=Animation"); !reflect.DeepEqual(got, []string{"tt2934286"}) {
		t.Fatal(got)
	}
}

func TestLibraryTitlesAlphabeticalFilters(t *testing.T) {
	for _, kind := range []string{"movie", "series"} {
		t.Run(kind, func(t *testing.T) {
			f, exec := posterFixture(t)
			cookie := login(t, f.api.Handler(), "admin")
			for i, name := range []string{"Zulu", "alpha", "Alpha"} {
				id := fmt.Sprintf("tt200000%d", i)
				mediaID, mediaKind := id, "movie"
				if kind == "series" {
					mediaID, mediaKind = id+":1:1", "episode"
					exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES($1,'shows',jsonb_build_object('id',$1::text,'type','series','name',$2::text))`, id, name)
				}
				media := posterMedia(t, f, exec, mediaID, mediaKind, true)
				exec(`UPDATE catalog_media SET metadata=jsonb_build_object('id',cinemeta_id,'type','movie','name',$2::text) WHERE id=$1`, media, name)
				exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','TogglePlayed',now())`, f.admin.ID, media)
			}
			for _, filter := range []string{"saved=true", "watched=true", "saved=true&watched=true"} {
				got := libraryIDs(t, f, cookie, "kind="+kind+"&"+filter)
				want := []string{"tt2000001", "tt2000002", "tt2000000"}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(filter, got, want)
				}
			}
		})
	}
}
func TestLibraryTitlesAlphabeticalPagination(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) SELECT 'tt'||(3000000+n)::text,'movie',jsonb_build_object('id','tt'||(3000000+n)::text,'type','movie','name','Title '||lpad((102-n)::text,3,'0')) FROM generate_series(1,101) n`)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true}','TogglePlayed',now() FROM catalog_media`, f.admin.ID)
	r := call(t, f.api.Handler(), "GET", "/api/library-titles?kind=movie&watched=true", nil, cookie)
	requireStatus(t, r, 200)
	var page struct {
		Titles []cinemeta.Movie `json:"titles"`
		Next   string           `json:"next_after"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Titles) != 100 || page.Titles[0].ID != "tt3000101" || page.Next != "tt3000002" {
		t.Fatal(page)
	}
	got := libraryIDs(t, f, cookie, "kind=movie&watched=true&after="+page.Next)
	if !reflect.DeepEqual(got, []string{"tt3000001"}) {
		t.Fatal(got)
	}
}

func TestMetadataDetailRepairsPersonalListWithoutChangingViewing(t *testing.T) {
	f, exec := posterFixture(t)
	f.api.movies = &movieStub{}
	id := posterMedia(t, f, exec, "tt1345836", "movie", true)
	exec(`UPDATE catalog_media SET metadata='{"id":"tt1345836","type":"movie","name":"Saved title","year":"","genres":[]}' WHERE id=$1`, id)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true,"play_count":2}','TogglePlayed',now())`, f.admin.ID, id)
	cookie := login(t, f.api.Handler(), "admin")
	requireStatus(t, call(t, f.api.Handler(), "GET", "/api/movies/tt1345836", nil, cookie), 200)
	r := call(t, f.api.Handler(), "GET", "/api/library-titles?kind=movie&saved=true&watched=true", nil, cookie)
	requireStatus(t, r, 200)
	var page struct {
		Titles []cinemeta.Movie `json:"titles"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Titles) != 1 || page.Titles[0].Year != "2012" || page.Titles[0].Name != "Saved title" {
		t.Fatal(page)
	}
	var played bool
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT (state->>'played')::bool,(state->>'play_count')::int FROM watch_current WHERE media_id=$1`, id).Scan(&played, &count); err != nil || !played || count != 2 {
		t.Fatal(played, count, err)
	}
}

func TestLibraryTitlesCanonicalIdentityWithImportedDescriptions(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	for _, kind := range []string{"movie", "series"} {
		id, mediaID, mediaKind := "tt4000001", "tt4000001", "movie"
		raw := `{"name":"Imported movie","year":"2025","description":"Retained description","poster":"","custom":"preserve"}`
		if kind == "series" {
			id, mediaID, mediaKind = "tt4000002", "tt4000002:1:1", "episode"
			raw = `{"id":"tt9999999","type":"movie","name":"Imported show","description":"Retained description","poster":"","genres":null,"custom":"preserve"}`
			exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES($1,'shows',$2)`, id, raw)
		}
		media := posterMedia(t, f, exec, mediaID, mediaKind, true)
		exec(`UPDATE catalog_media SET metadata=$2 WHERE id=$1`, media, raw)
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','StremioImport',now())`, f.admin.ID, media)
		response := call(t, f.api.Handler(), "GET", "/api/library-titles?kind="+kind+"&watched=true&saved=true", nil, cookie)
		requireStatus(t, response, 200)
		var page struct {
			Titles []cinemeta.Movie `json:"titles"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Titles) != 1 {
			t.Fatal(response.Body.String())
		}
		title := page.Titles[0]
		if title.ID != id || title.Type != kind || title.Genres == nil || title.Description != "Retained description" {
			t.Fatal(title)
		}
		var retained string
		if err := f.pool.QueryRow(context.Background(), `SELECT metadata->>'custom' FROM catalog_media WHERE id=$1`, media).Scan(&retained); err != nil || retained != "preserve" {
			t.Fatal("description mutated", retained, err)
		}
		if kind == "movie" {
			saved := call(t, f.api.Handler(), "GET", "/api/movies/saved", nil, cookie)
			requireStatus(t, saved, 200)
			var savedPage struct {
				Movies []cinemeta.Movie `json:"movies"`
			}
			if err := json.Unmarshal(saved.Body.Bytes(), &savedPage); err != nil {
				t.Fatal(err)
			}
			if len(savedPage.Movies) != 1 || !reflect.DeepEqual(savedPage.Movies[0], title) {
				t.Fatal(saved.Body.String())
			}
		}
	}
}

func TestLibraryTitlesMalformedDescriptionDoesNotReturnPartialSuccess(t *testing.T) {
	f, exec := posterFixture(t)
	cookie := login(t, f.api.Handler(), "admin")
	for i, raw := range []string{`{"name":"Alpha","genres":[]}`, `{"name":"Zulu","genres":"invalid"}`} {
		media := posterMedia(t, f, exec, fmt.Sprintf("tt400001%d", i), "movie", true)
		exec(`UPDATE catalog_media SET metadata=$2 WHERE id=$1`, media, raw)
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','TogglePlayed',now())`, f.admin.ID, media)
	}
	for _, endpoint := range []string{"/api/library-titles?kind=movie&watched=true", "/api/movies/saved"} {
		response := call(t, f.api.Handler(), "GET", endpoint, nil, cookie)
		if response.Code < 500 || strings.Contains(response.Body.String(), `"titles"`) || strings.Contains(response.Body.String(), `"movies"`) {
			t.Fatal("incomplete descriptions became successful smaller list", response.Code, response.Body.String())
		}
	}
}
