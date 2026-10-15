package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"testing"
	"time"
)

func TestTitleStatusesAuthorizationAndValidation(t *testing.T) {
	f, _, _ := mappingSetup(t)
	h := f.api.Handler()
	path := "/api/title-statuses?kind=movie&ids=tt1345836"
	requireStatus(t, call(t, h, "GET", path, nil, nil), 401)
	if _, err := f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, h, "viewer")
	r := call(t, h, "GET", path, nil, cookie)
	requireStatus(t, r, 200)
	var v struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Statuses) != 1 || v.Statuses[0].State != "" || *v.Statuses[0].Total != 1 {
		t.Fatal(v)
	}
	for _, query := range []string{"kind=episode&ids=tt1345836", "kind=movie&ids=bad", "kind=movie&ids=tt1345836,tt1345836"} {
		requireStatus(t, call(t, h, "GET", "/api/title-statuses?"+query, nil, cookie), 400)
	}
}

func TestTitleStatusesConfirmationFailureRetryAndRemoval(t *testing.T) {
	f, _, _ := mappingSetup(t)
	ctx := context.Background()
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Two released catalog episodes, only one selected. Count must not use mapped-row total.
	show, _ := seriesStub{}.Series(ctx, "tt1245698")
	if err := f.api.catalog.SaveSchedule(ctx, show, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"regular_episode_count":2}')`)
	var media, version, op string
	if err := f.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES('tt1245698:1:1','episode','{}') RETURNING id::text`).Scan(&media); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO mapping_versions(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by,active) VALUES($1,'1','t',repeat('a',40),1,'/file.mkv',100,'{}',$2,false) RETURNING id::text`, media, f.admin.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO mapping_operations(title_id,kind,actor,request,phase) VALUES('tt1245698','series','{}','{"files":[{}]}','validating') RETURNING id::text`).Scan(&op); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mapping_candidates(operation_id,media_id,mapping_id) VALUES($1,$2,$3)`, op, media, version)
	check := func(state string, added int, validated int) {
		t.Helper()
		r := call(t, h, "GET", "/api/title-statuses?kind=series&ids=tt1245698", nil, cookie)
		requireStatus(t, r, 200)
		var v struct {
			Statuses []titleStatus `json:"statuses"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		s := v.Statuses[0]
		total := 2
		if state == "pending" {
			total = 1
		}
		if s.State != state || s.Added != added || s.Total == nil || *s.Total != total || s.Validated != validated {
			t.Fatal(s)
		}
	}
	check("pending", 0, 0)
	exec(`UPDATE mapping_candidates SET probe='{}' WHERE operation_id=$1`, op)
	check("pending", 0, 1)
	exec(`UPDATE mapping_operations SET phase='publishing' WHERE id=$1`, op)
	check("pending", 0, 1)
	exec(`UPDATE mapping_versions SET active=true WHERE id=$1`, version)
	exec(`INSERT INTO streaming_items(movie_id,mapping_id,phase,probe,published) VALUES($1,$2,'ready','{}',true)`, media, version)
	check("pending", 1, 1) // Same identity in both sources is counted only once.
	exec(`UPDATE mapping_operations SET phase='complete' WHERE id=$1`, op)
	check("added", 1, 1)
	exec(`INSERT INTO mapping_operations(title_id,kind,actor,request,phase) VALUES('tt1245698','series','{}','{}','failed')`)
	check("failed", 1, 0)
	exec(`INSERT INTO mapping_operations(title_id,kind,actor,request,phase) VALUES('tt1245698','series','{}','{"files":[{}]}','queued')`)
	check("pending", 1, 0)
	exec(`INSERT INTO library_removals(title_id,kind,phase) VALUES('tt1245698','series','complete')`)
	check("", 0, 0)
}

func TestTitleStatusesBackfillsFullCatalogCount(t *testing.T) {
	f, _, _ := mappingSetup(t)
	f.api.series = seriesStub{}
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{}'); INSERT INTO mapping_operations(title_id,kind,actor,request,phase) VALUES('tt1245698','series','{}','{"files":[{}]}','failed')`); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	r := call(t, h, "GET", "/api/title-statuses?kind=series&ids=tt1245698", nil, login(t, h, "admin"))
	requireStatus(t, r, 200)
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT (metadata->>'regular_episode_count')::int FROM catalog_series WHERE cinemeta_id='tt1245698'`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestTitleStatusesSubmissionProgressAcrossBatchesAndRetry(t *testing.T) {
	f, _, _ := mappingSetup(t)
	ctx := context.Background()
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"regular_episode_count":500}')`)
	var sub string
	if err := f.pool.QueryRow(ctx, `INSERT INTO mapping_submissions(title_id,kind,actor,request,batch_size) VALUES('tt1245698','series','{}','{"files":[{},{},{}]}',2) RETURNING id::text`).Scan(&sub); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mapping_operations(title_id,kind,actor,request,phase,submission_id,batch_number) VALUES('tt1245698','series','{}','{"files":[{},{}]}','complete',$1,1),('tt1245698','series','{}','{"files":[{}]}','failed',$1,2),('tt1245698','series','{}','{"files":[{}]}','queued',$1,2)`, sub)
	r := call(t, h, "GET", "/api/title-statuses?kind=series&ids=tt1245698", nil, cookie)
	requireStatus(t, r, 200)
	var v struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	s := v.Statuses[0]
	if s.State != "pending" || s.Added != 0 || s.Validated != 2 || s.Total == nil || *s.Total != 3 {
		t.Fatal(s)
	}
}

type specialsCountCatalog struct{ seriesStub }

func (s specialsCountCatalog) Series(ctx context.Context, id string) (cinemeta.Series, error) {
	show, err := s.seriesStub.Series(ctx, id)
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: id + ":0:1", Season: 0, Number: 1}, cinemeta.Episode{ID: id + ":0:2", Season: 0, Number: 2})
	return show, err
}
func TestTitleStatusesRegularSeasonCompletionExcludesSpecials(t *testing.T) {
	f, exec := posterFixture(t)
	f.api.series = specialsCountCatalog{}
	cookie := login(t, f.api.Handler(), "admin")
	// The old cached total included specials; it must not be reused for the badge.
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"episode_count":4}')`)
	posterMedia(t, f, exec, "tt1245698:1:1", "episode", true)
	special := posterMedia(t, f, exec, "tt1245698:0:1", "episode", true)
	read := func(added, available int) {
		t.Helper()
		r := call(t, f.api.Handler(), "GET", "/api/title-statuses?kind=series&ids=tt1245698", nil, cookie)
		requireStatus(t, r, 200)
		var v struct {
			Statuses []titleStatus `json:"statuses"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		s := v.Statuses[0]
		if s.State != "added" || s.Added != added || s.Total == nil || *s.Total != 2 || s.Watch.Available != available {
			t.Fatal(s)
		}
	}
	read(1, 2) // A saved special cannot make a partially saved regular season complete.
	posterMedia(t, f, exec, "tt1245698:1:2", "episode", true)
	read(2, 2) // All regular episodes complete, even with another special absent.
	exec(`UPDATE streaming_items SET published=false WHERE movie_id=$1`, special)
	read(2, 2) // Removing a special leaves green completion unchanged.
}
func TestTitleStatusesSpecialsOnlyStillShowsAddedMembership(t *testing.T) {
	f, exec := posterFixture(t)
	show, _ := seriesStub{}.Series(context.Background(), "tt1245698")
	if err := f.api.catalog.SaveSchedule(context.Background(), show, time.Now()); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, f.api.Handler(), "admin")
	exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES('tt1245698','anime','{"regular_episode_count":2}')`)
	posterMedia(t, f, exec, "tt1245698:0:1", "episode", true)
	r := call(t, f.api.Handler(), "GET", "/api/title-statuses?kind=series&ids=tt1245698", nil, cookie)
	requireStatus(t, r, 200)
	var v struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	s := v.Statuses[0]
	if s.State != "added" || s.Added != 0 || s.Total == nil || *s.Total != 2 {
		t.Fatal(s)
	}
}
