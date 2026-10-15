package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"entertainmentdistrict/internal/cinemeta"
)

func TestSavedMoviesReadOnlyAccess(t *testing.T) {
	f, rd, selection := mappingSetup(t)
	ctx := context.Background()
	if _, err := f.api.catalog.Save(ctx, identity(f.admin), "tt1345836", selection, f.api.movies, rd); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(ctx, identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	viewer := login(t, h, "viewer")
	requireStatus(t, call(t, h, "GET", "/api/movies/saved", nil, nil), 401)
	for _, name := range []string{"viewer", "admin"} {
		r := call(t, h, "GET", "/api/movies/saved", nil, login(t, h, name))
		requireStatus(t, r, 200)
		expected, _ := json.Marshal(map[string]any{"movies": []cinemeta.Movie{{ID: "tt1345836", Type: "movie", Name: "The Dark Knight Rises", Year: "2012", Genres: []string{}}}})
		var got, want any
		json.Unmarshal(r.Body.Bytes(), &got)
		json.Unmarshal(expected, &want)
		actual, _ := json.Marshal(got)
		expected, _ = json.Marshal(want)
		if string(actual) != string(expected) {
			t.Fatalf("expected metadata only, got %s", r.Body.String())
		}
	}
	requireStatus(t, call(t, h, "GET", "/api/admin/mappings", nil, viewer), 403)
	requireStatus(t, call(t, h, "GET", mappingPath, nil, viewer), 403)
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, viewer), 403)
}

func TestSavedMoviesPaginationAndFiltering(t *testing.T) {
	f, _, _ := mappingSetup(t)
	ctx := context.Background()
	// Include a mapped episode and an unmapped movie alongside 101 saved movies.
	_, err := f.pool.Exec(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata)
 SELECT 'tt'||(1000000+n)::text||CASE WHEN n=102 THEN ':1:1' ELSE '' END,CASE WHEN n=102 THEN 'episode' ELSE 'movie' END,
 jsonb_build_object('id','tt'||(1000000+n)::text,'type','movie','name','Title','year','','description','','poster','','genres','[]'::jsonb)
 FROM generate_series(1,103) n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 SELECT id,'1','private-torrent',repeat('a',40),1,'/private-file',100,'{}',$1 FROM catalog_media WHERE cinemeta_id!='tt1000103'`, f.admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	var page struct {
		Movies []cinemeta.Movie `json:"movies"`
		Next   string           `json:"next_after"`
	}
	r := call(t, h, "GET", "/api/movies/saved", nil, cookie)
	requireStatus(t, r, 200)
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Movies) != 100 || page.Movies[0].ID != "tt1000001" || page.Next != "tt1000100" {
		t.Fatal("incorrect first page", page)
	}
	r = call(t, h, "GET", "/api/movies/saved?after="+page.Next, nil, cookie)
	requireStatus(t, r, 200)
	page.Next = ""
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Movies) != 1 || page.Movies[0].ID != "tt1000101" || page.Next != "" {
		t.Fatal("incorrect final page", page)
	}
	r = call(t, h, "GET", "/api/movies/saved?after=tt1000101", nil, cookie)
	requireStatus(t, r, 200)
	if r.Body.String() != "{\"movies\":[]}\n" {
		t.Fatal("empty page", r.Body.String())
	}
	requireStatus(t, call(t, h, "GET", "/api/movies/saved?after=invalid", nil, cookie), 400)
}
