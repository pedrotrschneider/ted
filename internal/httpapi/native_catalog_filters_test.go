package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"entertainmentdistrict/internal/cinemeta"
)

func filteredNativePage(t *testing.T, h http.Handler, token, kind, query string) nativeCatalogPage {
	t.Helper()
	r := nativeCall(t, h, "GET", "/api/client/v1/catalog/"+kind+"?"+query, token, nil, nil)
	requireStatus(t, r, 200)
	var page nativeCatalogPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func nativePageIDs(page nativeCatalogPage) []string {
	ids := []string{}
	for _, title := range page.Titles {
		ids = append(ids, title.Media.ID)
	}
	return ids
}

func TestNativeCatalogueFiltersIntersectHistoryMappingsAndPageBeforeDelivery(t *testing.T) {
	f, exec := posterFixture(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	first := posterMedia(t, f, exec, "tt1000001", "movie", true)
	posterMedia(t, f, exec, "tt1000002", "movie", true)
	exec(`UPDATE catalog_media SET metadata=jsonb_build_object('id',cinemeta_id,'type','movie','name',cinemeta_id,'genres','["Action"]'::jsonb)`)
	exec(`UPDATE catalog_media SET metadata=metadata-'id'-'type' WHERE cinemeta_id='tt1000002'`)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":false,"position_ticks":70000000,"play_count":0,"last_played":null}','PlaybackProgress',now())`, f.admin.ID, first)
	exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) SELECT 'tt'||(3000000+n)::text,'movie',jsonb_build_object('id','tt'||(3000000+n)::text,'type','movie','name','Title '||lpad(n::text,3,'0'),'genres','["Drama"]'::jsonb) FROM generate_series(1,51) n`)
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT $1,id,'playback_completed','{"played":true}',now() FROM catalog_media WHERE cinemeta_id LIKE 'tt3000%'`, f.admin.ID)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"mapped=true", []string{"tt1000001", "tt1000002"}},
		{"mapped=true&watched=watched", []string{"tt1000001"}},
		{"watched=partial&genre=Action", []string{"tt1000001"}},
		{"watched=fully", []string{}},
	} {
		if got := nativePageIDs(filteredNativePage(t, h, pair.AccessToken, "movie", tc.query)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want)
		}
	}
	page := filteredNativePage(t, h, pair.AccessToken, "movie", "watched=watched&genre=Drama")
	if len(page.Titles) != 50 || page.NextAfter != "tt3000050" || page.NextSkip != nil {
		t.Fatal("filtering/paging lost retained history", page)
	}
	if got := nativePageIDs(filteredNativePage(t, h, pair.AccessToken, "movie", "watched=watched&genre=Drama&after="+page.NextAfter)); !reflect.DeepEqual(got, []string{"tt3000051"}) {
		t.Fatal(got)
	}
	if _, err := f.service.Create(context.Background(), identity(f.admin), "other", "Other", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	r := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "other", "password": testPassword, "device_id": "other-device", "device_name": "Test"}, nil)
	requireStatus(t, r, 200)
	var other struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	if got := filteredNativePage(t, h, other.Access, "movie", "watched=watched"); len(got.Titles) != 0 {
		t.Fatal("history leaked", got)
	}
	if got := filteredNativePage(t, h, other.Access, "movie", "mapped=true"); len(got.Titles) != 2 {
		t.Fatal("mapped visibility lost", got)
	}
	for _, query := range []string{"mapped=invalid", "watched=invalid", "watched=fully&after=bad", "watched=partial&skip=50", "after=tt1000001", "mapped=true&genre=Invalid"} {
		requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/catalog/movie?"+query, pair.AccessToken, nil, nil), 400)
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/catalog/movie?mapped=true", "", nil, nil), 401)
}

func TestNativeCatalogueShowFiltersIncludeStartedMainEpisodesAndExcludeSpecialFutureCounts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	show := cinemeta.Series{Movie: cinemeta.Movie{ID: "tt8000001", Type: "series", Name: "Started show", Genres: []string{"Drama"}}}
	for _, e := range []struct {
		season, number int
		date           string
	}{{0, 1, "2000-01-01"}, {1, 1, "2000-01-01"}, {1, 2, "2000-01-01"}, {1, 3, "2999-01-01"}, {1, 4, ""}} {
		show.Episodes = append(show.Episodes, cinemeta.Episode{ID: fmt.Sprintf("%s:%d:%d", show.ID, e.season, e.number), Season: e.season, Number: e.number, ReleaseDate: e.date})
	}
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range show.Episodes {
		exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,'episode','{}')`, e.ID)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	set := func(id string, played bool, position int) {
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,jsonb_build_object('played',$3::bool,'position_ticks',$4::bigint,'play_count',0,'last_played',NULL),'TEDLocal',now() FROM catalog_media WHERE cinemeta_id=$2 ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state`, f.admin.ID, id, played, position)
	}
	check := func(filter string, want int) {
		t.Helper()
		page := filteredNativePage(t, h, pair.AccessToken, "series", "watched="+filter)
		if len(page.Titles) != want {
			t.Fatal(filter, page)
		}
	}
	set(show.ID+":0:1", true, 0)
	check("partial", 0)
	check("watched", 0)
	set(show.ID+":1:1", false, 70000000)
	check("partial", 1)
	check("fully", 0)
	set(show.ID+":1:1", true, 0)
	check("partial", 1)
	set(show.ID+":1:2", true, 0)
	check("fully", 1)
	check("partial", 0)
	page := filteredNativePage(t, h, pair.AccessToken, "series", "watched=fully")
	if !page.Titles[0].State.Played || page.Titles[0].EpisodeCounts.Total != 2 {
		t.Fatal(page)
	}
	// A schedule correction adds a released unwatched main episode. No
	// special/future episode was counted; the filter observes current state.
	show.Episodes[3].ReleaseDate = "2000-01-01"
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	check("fully", 0)
	check("partial", 1)
}

func TestNativeCatalogueMoviePartialUsesSameInterruptedRewatchRuleAsContinueWatching(t *testing.T) {
	f, exec := posterFixture(t)
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	id := posterMedia(t, f, exec, "tt1000001", "movie", true)
	exec(`UPDATE catalog_media SET metadata='{"id":"tt1000001","type":"movie","name":"Rewatch"}' WHERE id=$1`, id)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at,revision) VALUES($1,$2,'{"played":true,"position_ticks":170000000,"play_count":1,"last_played":"2026-10-11T12:00:00Z"}','TEDNativePlayback','2026-10-11T12:00:00Z',3)`, f.admin.ID, id)
	exec(`INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,mapping_id,duration_ms,applied_revision,last_event_at,started,completed) SELECT $1,$2,'test-device',movie_id,id,100000,3,'2026-10-11T12:00:00Z',true,false FROM media_mappings WHERE movie_id=$3`, f.admin.ID, pair.FamilyID, id)
	page := filteredNativePage(t, h, pair.AccessToken, "movie", "watched=partial")
	if len(page.Titles) != 1 || !page.Titles[0].State.Played || page.Titles[0].State.PositionMS != 17000 {
		t.Fatal("unfinished rewatch lost", page)
	}
	exec(`UPDATE native_playback_sessions SET completed=true WHERE media_id=$1`, id)
	if page := filteredNativePage(t, h, pair.AccessToken, "movie", "watched=partial"); len(page.Titles) != 0 {
		t.Fatal("finished session still partial", page)
	}
	exec(`UPDATE native_playback_sessions SET completed=false WHERE media_id=$1`, id)
	exec(`UPDATE watch_current SET revision=4 WHERE media_id=$1`, id)
	if page := filteredNativePage(t, h, pair.AccessToken, "movie", "watched=partial"); len(page.Titles) != 0 {
		t.Fatal("superseded session still partial", page)
	}
}

type nativeYearCatalogue struct {
	movieStub
	filters []cinemeta.Filters
}

func (s *nativeYearCatalogue) Browse(_ context.Context, _ string, skip int, filters cinemeta.Filters) (cinemeta.Page, error) {
	s.filters = append(s.filters, filters)
	next := skip + 50
	return cinemeta.Page{Movies: []cinemeta.Movie{
		{ID: "tt1000001", Type: "movie", Name: "Action title", Genres: []string{"Action"}},
		{ID: "tt1000002", Type: "movie", Name: "Drama title", Genres: []string{"Drama"}},
	}, NextSkip: &next}, nil
}

func TestNativeCatalogueNewGenreIntersectionRetainsCursorAndCurrentYear(t *testing.T) {
	f := setup(t)
	source := &nativeYearCatalogue{}
	f.api.movies = source
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	page := filteredNativePage(t, h, pair.AccessToken, "movie", "catalog=new&genre=Drama")
	if got := nativePageIDs(page); !reflect.DeepEqual(got, []string{"tt1000002"}) || page.NextSkip == nil || *page.NextSkip != 50 {
		t.Fatal(page)
	}
	if source.filters[0].Genre != "" || source.filters[0].Year != time.Now().In(f.api.CalendarLocation).Year() {
		t.Fatal(source.filters)
	}
	page = filteredNativePage(t, h, pair.AccessToken, "movie", "catalog=new&genre=Comedy&skip=50")
	if len(page.Titles) != 0 || page.NextSkip == nil || *page.NextSkip != 100 {
		t.Fatal("empty intersection prematurely ended catalogue", page)
	}
}
