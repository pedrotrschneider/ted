package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/watchstate"
)

func TestNativeMovieExplicitStremioRetainsMappedSourceAndAuthorization(t *testing.T) {
	f, _, pair := nativePlaybackFixture(t)
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stream/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": "http://" + r.Host + "/file.mp4", "name": "Stremio release"}}})
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "file.mp4", time.Time{}, strings.NewReader(strings.Repeat("addon", 1000)))
	}))
	defer upstream.Close()
	descriptor, _ := json.Marshal(map[string]any{"transportUrl": upstream.URL + "/private-key/manifest.json", "manifest": map[string]any{"id": "fixture", "name": "Fixture", "types": []string{"movie"}, "resources": []string{"stream"}}})
	f.api.addons = addons.New(f.pool, addonAccountFixture{[]json.RawMessage{descriptor}}, upstream.Client(), nil)
	if _, err := f.pool.Exec(ctx, `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES('addon','fixture@example.com','private-auth')`); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	media := catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}
	for _, ready := range []bool{true, false} {
		if _, err := f.pool.Exec(ctx, `UPDATE streaming_items SET published=$1,phase=CASE WHEN $1 THEN 'ready' ELSE 'failed' END`, ready); err != nil {
			t.Fatal(err)
		}
		mapped := nativeCall(t, h, "POST", "/api/client/v1/playback/sources", pair.AccessToken, map[string]any{"media": media}, nil)
		requireStatus(t, mapped, 200)
		if !strings.Contains(mapped.Body.String(), `"mapped_file_name":"movie.mkv"`) || !strings.Contains(mapped.Body.String(), `"mode":"mapped"`) {
			t.Fatal("lost mapping preference/file name", mapped.Body.String())
		}
		chosen := nativeCall(t, h, "POST", "/api/client/v1/playback/sources", pair.AccessToken, map[string]any{"media": media, "provider": "stremio"}, nil)
		requireStatus(t, chosen, 200)
		var page struct{ Sources []addons.Choice }
		if err := json.Unmarshal(chosen.Body.Bytes(), &page); err != nil || len(page.Sources) != 1 {
			t.Fatal("explicit sources unavailable", chosen.Body.String(), err)
		}
		r := nativeCall(t, h, "POST", "/api/client/v1/playback/sessions", pair.AccessToken, map[string]any{"media": media, "source_id": page.Sources[0].ID}, nil)
		requireStatus(t, r, 201)
		var session streaming.NativeSession
		if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil || session.Source != "addon" {
			t.Fatal(session, err)
		}
		requireStatus(t, nativeMediaCall(h, "GET", session.MediaURL, "", "bytes=0-9"), 401)
		body := nativeMediaCall(h, "GET", session.MediaURL, pair.AccessToken, "bytes=0-9")
		requireStatus(t, body, 206)
		if body.Body.String() != "addonaddon" {
			t.Fatal("explicit source did not own session")
		}
	}
	var mapping string
	var history int
	if err := f.pool.QueryRow(ctx, `SELECT id::text,(SELECT count(*) FROM watch_history) FROM media_mappings WHERE movie_id=$1`, nativeTestMedia).Scan(&mapping, &history); err != nil || mapping != nativeTestMapping || history != 0 {
		t.Fatal("source selection changed mapping/history", mapping, history, err)
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/sources", pair.AccessToken, map[string]any{"media": media, "provider": "unknown"}, nil), 422)
}

func TestNativePersonalMovieArtworkIsCachedOnlyAndPreservesUnfinishedRewatch(t *testing.T) {
	f, _, pair := nativePlaybackFixture(t)
	installMetadata(t, f)
	f.api.movies = &movieStub{}
	f.api.series = seriesStub{}
	h := f.api.Handler()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE catalog_media SET metadata=jsonb_set(metadata,'{poster}','"https://images.metahub.space/poster/tt1234567.jpg"') WHERE id=$1`, nativeTestMedia); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at,revision)
 VALUES($1,$2,'{"played":true,"position_ticks":170000000,"play_count":1,"last_played":"2026-10-11T12:00:00Z"}','TEDNativePlayback','2026-10-11T12:00:00Z',3)`, f.admin.ID, nativeTestMedia); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO native_playback_sessions(user_id,family_id,device_id,media_id,mapping_id,duration_ms,applied_revision,last_event_at,started,completed)
 SELECT $1,$3,'test-device',movie_id,id,100000,3,'2026-10-11T12:00:00Z',true,false FROM media_mappings WHERE movie_id=$2`, f.admin.ID, nativeTestMedia, pair.FamilyID); err != nil {
		t.Fatal(err)
	}
	beforeState := nativeCall(t, h, "GET", "/api/client/v1/watch-state/movie/tt1234567", pair.AccessToken, nil, nil)
	requireStatus(t, beforeState, 200)
	var entityID string
	for _, phase := range []string{"cold", "ready", "failed"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "ready" {
				snapshot, err := f.api.Metadata.Ensure(ctx, "Tmdb", metadata.Request{Kind: "movie", TitleID: "tt1234567"}, metadata.Item{Type: "Movie", Name: "Native fixture"})
				if err != nil {
					t.Fatal(err)
				}
				entityID = snapshot.Item.ID
				if _, err := f.pool.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url,status) VALUES($1,'Backdrop',0,'https://private-provider-key.invalid/secret.jpg','ready')`, entityID); err != nil {
					t.Fatal(err)
				}
			}
			if phase != "cold" {
				if _, err := f.pool.Exec(ctx, `UPDATE metadata_entities SET status=$2,item=jsonb_set(item,'{Overview}','"Cached movie overview"') WHERE id=$1`, entityID, phase); err != nil {
					t.Fatal(err)
				}
			}
			var beforeJobs, beforeEntities int
			if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM river_job),(SELECT count(*) FROM metadata_entities)`).Scan(&beforeJobs, &beforeEntities); err != nil {
				t.Fatal(err)
			}
			for _, route := range []string{"/api/client/v1/rows/continue-watching", "/api/client/v1/home"} {
				r := nativeCall(t, h, "GET", route, pair.AccessToken, nil, nil)
				requireStatus(t, r, 200)
				var page nativePersonalPage
				if strings.HasSuffix(route, "/home") {
					var home struct {
						Rows []struct {
							ID     string              `json:"id"`
							Titles []nativeTitleDetail `json:"titles"`
						}
					}
					if err := json.Unmarshal(r.Body.Bytes(), &home); err != nil {
						t.Fatal(err)
					}
					for _, row := range home.Rows {
						if row.ID == "continue-watching" {
							page.Titles = row.Titles
						}
					}
				} else if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Titles) != 1 {
					t.Fatal("lost personal row", r.Body.String())
				}
				v := page.Titles[0]
				if !v.State.Played || v.State.PositionMS != 17000 || v.State.Revision != 3 || !v.Local || !v.Ready || v.Metadata.Background != "" || v.Metadata.Poster == "" {
					t.Fatal("artwork decoration changed personal state/fallback", v)
				}
				if phase == "cold" {
					if v.Enrichment != nil {
						t.Fatal("cold personal row enrolled metadata", v.Enrichment)
					}
				} else if v.Enrichment == nil || v.Enrichment.Status != phase || v.Enrichment.Item.Overview != "Cached movie overview" || len(v.Enrichment.Images) != 1 || v.Enrichment.Images[0].Type != "Backdrop" || !strings.HasPrefix(v.Enrichment.Images[0].URL, "/api/client/v1/metadata/images/") {
					t.Fatal("lost cached movie artwork", v.Enrichment)
				}
				if strings.Contains(r.Body.String(), "private-provider-key") {
					t.Fatal("provider URL leaked")
				}
			}
			var afterJobs, afterEntities int
			if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM river_job),(SELECT count(*) FROM metadata_entities)`).Scan(&afterJobs, &afterEntities); err != nil || afterJobs != beforeJobs || afterEntities != beforeEntities {
				t.Fatal("personal reads scheduled enrichment", beforeJobs, afterJobs, beforeEntities, afterEntities, err)
			}
			afterState := nativeCall(t, h, "GET", "/api/client/v1/watch-state/movie/tt1234567", pair.AccessToken, nil, nil)
			requireStatus(t, afterState, 200)
			if afterState.Body.String() != beforeState.Body.String() {
				t.Fatal("personal artwork changed watch state")
			}
		})
	}
}

func TestNativeBeginningChangesSessionOffsetWithoutClearingHistory(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	session := createNativeHTTPPlayback(t, h, pair.AccessToken)
	at := time.Now().UTC()
	events := []watchstate.NativeEvent{
		{ID: "33333333-3333-4333-8333-333333333333", SessionID: session.ID, Sequence: 1, Media: session.Media, Kind: "start", ObservedAt: at, Origin: "live"},
		{ID: "44444444-4444-4444-8444-444444444444", SessionID: session.ID, Sequence: 2, Media: session.Media, Kind: "stop", PositionMS: 40000, ObservedAt: at, Origin: "live"},
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/playback/events", pair.AccessToken, map[string]any{"events": events}, nil), 200)
	for _, played := range []bool{false, true} {
		if _, err := f.pool.Exec(context.Background(), `UPDATE watch_current SET state=jsonb_set(state,'{played}',to_jsonb($1::boolean))`, played); err != nil {
			t.Fatal(err)
		}
		before := nativeCall(t, h, "GET", "/api/client/v1/watch-state/movie/tt1234567", pair.AccessToken, nil, nil)
		r := nativeCall(t, h, "POST", "/api/client/v1/playback/sessions", pair.AccessToken, map[string]any{"media": session.Media, "start_from_beginning": true}, nil)
		requireStatus(t, r, 201)
		var restart streaming.NativeSession
		if err := json.Unmarshal(r.Body.Bytes(), &restart); err != nil || restart.ResumeMS != 0 {
			t.Fatal("beginning retained resume", restart, err)
		}
		after := nativeCall(t, h, "GET", "/api/client/v1/watch-state/movie/tt1234567", pair.AccessToken, nil, nil)
		if after.Body.String() != before.Body.String() || createNativeHTTPPlayback(t, h, pair.AccessToken).ResumeMS != 40000 {
			t.Fatal("beginning cleared current watch state")
		}
	}
}

func TestNativeBrowseReturnsCachedEnrichmentWithoutEnrollingColdTitles(t *testing.T) {
	f := setup(t)
	installMetadata(t, f)
	f.api.movies = &movieStub{}
	f.api.series = seriesStub{}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	for _, entry := range []struct{ kind, id string }{{"movie", "tt1345836"}, {"series", "tt1245698"}} {
		url := "/api/client/v1/catalog/" + entry.kind
		cold := nativeCall(t, h, "GET", url, pair.AccessToken, nil, nil)
		requireStatus(t, cold, 200)
		var page nativeCatalogPage
		if err := json.Unmarshal(cold.Body.Bytes(), &page); err != nil || len(page.Titles) != 1 || page.Titles[0].Enrichment != nil {
			t.Fatal("cold browse triggered enrichment", cold.Body.String(), err)
		}
		detail := nativeCall(t, h, "GET", "/api/client/v1/titles/"+entry.kind+"/"+entry.id, pair.AccessToken, nil, nil)
		requireStatus(t, detail, 200)
		var d nativeTitleDetail
		if err := json.Unmarshal(detail.Body.Bytes(), &d); err != nil || d.Enrichment == nil {
			t.Fatal(err)
		}
		id := d.Enrichment.Item.ID
		if _, err := f.pool.Exec(context.Background(), `UPDATE metadata_entities SET status='ready',item=jsonb_set(item,'{Overview}','"Cached provider description"') WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(context.Background(), `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url,status) VALUES($1,'Backdrop',0,'https://private-provider-key.invalid/secret.jpg','ready')`, id); err != nil {
			t.Fatal(err)
		}
		var before int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		warm := nativeCall(t, h, "GET", url, pair.AccessToken, nil, nil)
		requireStatus(t, warm, 200)
		if err := json.Unmarshal(warm.Body.Bytes(), &page); err != nil || page.Titles[0].Enrichment == nil || page.Titles[0].Enrichment.Item.Overview != "Cached provider description" || len(page.Titles[0].Enrichment.Images) != 1 {
			t.Fatal("cached summary/artwork absent", warm.Body.String(), err)
		}
		if strings.Contains(warm.Body.String(), "private-provider-key") {
			t.Fatal("provider URL leaked")
		}
		var after int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&after); err != nil || before != after {
			t.Fatal("browse scheduled provider jobs", before, after, err)
		}
	}
}
