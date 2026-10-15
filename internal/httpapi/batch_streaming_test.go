package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/streaming"
	"testing"
)

type batchSeriesStub struct{ seriesStub }

func (s batchSeriesStub) Series(ctx context.Context, id string) (cinemeta.Series, error) {
	show, err := s.seriesStub.Series(ctx, id)
	show.Episodes[1] = cinemeta.Episode{ID: "tt1245698:2:1", Season: 2, Number: 1, Name: "Season two"}
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: "tt1245698:0:1", Season: 0, Number: 1, Name: "Special"})
	return show, err
}
func TestSeriesPreparationScopeCoverageAndAuthorization(t *testing.T) {
	f, rd, selection := mappingSetup(t)
	f.api.series = batchSeriesStub{}
	service, err := streaming.New(context.Background(), f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	batch := catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: "tt1245698:1:1", Selection: selection}, {EpisodeID: "tt1245698:0:1", Selection: selection}}}
	if err := f.api.catalog.SaveEpisodes(context.Background(), identity(f.admin), "tt1245698", batch, f.api.series, rd); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/series/tt1245698/streaming"
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
	response = call(t, h, "POST", path, map[string]any{"mapped_only": true}, cookie)
	requireStatus(t, response, 202)
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result != (streaming.BatchResult{Total: 3, Queued: 1, Existing: 1, Missing: 1}) {
		t.Fatal(result, err)
	}
	requireStatus(t, call(t, h, "POST", path, map[string]any{"season": -1}, cookie), 400)
	requireStatus(t, call(t, h, "POST", path, map[string]any{"season": 99}, cookie), 400)
}
