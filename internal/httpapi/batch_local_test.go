package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/streaming"
	"testing"
)

func TestSeriesDownloadScopeCoverageAndAuthorization(t *testing.T) {
	f, rd, selection := mappingSetup(t)
	f.api.series = batchSeriesStub{}
	service, err := streaming.New(context.Background(), f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	if err = service.ConfigureStorage(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.CloseStorage)
	selection.Bytes = 1000
	rd.detail.Files[0].Bytes = 1000
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	batch := catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: "tt1245698:1:1", Selection: selection}, {EpisodeID: "tt1245698:0:1", Selection: selection}}}
	if err := f.api.catalog.SaveEpisodes(context.Background(), identity(f.admin), "tt1245698", batch, f.api.series, rd); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(context.Background(), `INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) SELECT movie_id,id,'ready',true,'{}'::jsonb FROM media_mappings`); err != nil {
		t.Fatal(err)
	}
	path := "/api/series/tt1245698/local"
	requireStatus(t, call(t, h, "POST", path, map[string]any{}, nil), 401)
	requireStatus(t, call(t, h, "POST", path, map[string]any{}, cookie), 409)
	var count int
	if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial batch", count, err)
	}
	response := call(t, h, "POST", path, map[string]any{"season": 0}, cookie)
	requireStatus(t, response, 202)
	var result streaming.BatchResult
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result != (streaming.BatchResult{Total: 1, Queued: 1}) {
		t.Fatal(result, err)
	}
	response = call(t, h, "POST", path, map[string]any{"ready_only": true}, cookie)
	requireStatus(t, response, 202)
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result != (streaming.BatchResult{Total: 3, Queued: 1, Existing: 1, Missing: 1}) {
		t.Fatal(result, err)
	}
	requireStatus(t, call(t, h, "POST", path, map[string]any{"season": -1}, cookie), 400)
	requireStatus(t, call(t, h, "POST", path, map[string]any{"season": 99}, cookie), 400)
}
