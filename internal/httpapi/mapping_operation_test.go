package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/streaming"
	"strings"
	"testing"
)

func TestMappingSubmissionAuthorizationDurabilityAndUnknownOutcome(t *testing.T) {
	f, _, selection := mappingSetup(t)
	ctx := context.Background()
	service, err := streaming.New(ctx, f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, nil), 401)
	if _, err = f.service.Create(ctx, identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, login(t, h, "viewer")), 403)
	requireStatus(t, call(t, h, "GET", "/api/admin/movies/tt1345836/mapping-operation", nil, cookie), 200)
	response := call(t, h, "PUT", mappingPath, selection, cookie)
	requireStatus(t, response, 202)
	var op streaming.MappingOperation
	if err = json.Unmarshal(response.Body.Bytes(), &op); err != nil || op.Phase != "queued" || op.Total != 1 {
		t.Fatal(op, err)
	}
	again := call(t, h, "PUT", mappingPath, selection, cookie)
	requireStatus(t, again, 202)
	if response.Body.String() != again.Body.String() {
		t.Fatal("retry duplicated operation")
	}
	selection.Path = "/another.mkv"
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, cookie), 409)
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM media_mappings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("submission activated mapping", count, err)
	}
	recreated, err := streaming.New(ctx, f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = recreated
	status := call(t, f.api.Handler(), "GET", "/api/admin/movies/tt1345836/mapping-operation", nil, cookie)
	requireStatus(t, status, 200)
	if !strings.Contains(status.Body.String(), op.ID) {
		t.Fatal("operation lost")
	}
	if err = f.service.Revoke(ctx, identity(f.admin)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, cookie), 401)
}
func TestMultiTorrentMappingSubmissionCoverageAndInvalidRequests(t *testing.T) {
	f, _, selection := mappingSetup(t)
	service, err := streaming.New(context.Background(), f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	batch := catalog.SeriesSelection{Category: "anime", Files: []catalog.EpisodeSelection{{EpisodeID: "tt1245698:1:1", Selection: selection}, {EpisodeID: "tt1245698:2:1", Selection: selection}}}
	batch.Files[1].TorrentID = "T2"
	duplicate := batch
	duplicate.Files = append(append([]catalog.EpisodeSelection{}, batch.Files...), batch.Files[0])
	requireStatus(t, call(t, h, "PUT", seriesMappingPath, duplicate, cookie), 400)
	requireStatus(t, call(t, h, "PUT", seriesMappingPath, batch, cookie), 202)
	var stored catalog.SeriesSelection
	if err = f.pool.QueryRow(context.Background(), `SELECT request FROM mapping_operations`).Scan(&stored); err != nil || len(stored.Files) != 2 || stored.Files[1].TorrentID != "T2" {
		t.Fatal("complete batch not retained", stored, err)
	}
	requireStatus(t, call(t, h, "GET", "/api/admin/series/tt1245698/mapping-operation", nil, cookie), 200)
	f.api.Streaming = nil
	requireStatus(t, call(t, h, "PUT", mappingPath, selection, cookie), 503)
}

func TestMappingRetryAuthorizationAndStaleHandle(t *testing.T) {
	f, _, selection := mappingSetup(t)
	ctx := context.Background()
	service, err := streaming.New(ctx, f.pool, nil, f.api.logger, 4)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	h := f.api.Handler()
	admin := login(t, h, "admin")
	response := call(t, h, "PUT", mappingPath, selection, admin)
	requireStatus(t, response, 202)
	var op streaming.MappingOperation
	if err = json.Unmarshal(response.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE mapping_operations SET phase='failed' WHERE id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/movies/tt1345836/mapping-operation/retry"
	input := map[string]string{"submission_id": op.ID}
	requireStatus(t, call(t, h, "POST", path, input, nil), 401)
	if _, err = f.service.Create(ctx, identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "POST", path, input, login(t, h, "viewer")), 403)
	requireStatus(t, call(t, h, "POST", path, map[string]string{"submission_id": "wrong"}, admin), 400)
	requireStatus(t, call(t, h, "POST", path, map[string]string{"submission_id": "11111111-2222-4333-8444-555555555555"}, admin), 409)
	requireStatus(t, call(t, h, "POST", path, input, admin), 202)
	requireStatus(t, call(t, h, "POST", path, input, admin), 202)
	var count int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM mapping_operations WHERE submission_id=$1", op.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("retry not idempotent", count, err)
	}
}
