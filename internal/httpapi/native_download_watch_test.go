package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/watchstate"
)

func TestNativeDownloadWatchBatchAuthValidationAndNoCatalogMutation(t *testing.T) {
	f, h, pair := nativePlaybackFixture(t)
	path := "/api/client/v1/downloads/watch-state"
	media := []catalog.MediaIdentity{{Kind: "movie", ID: "tt1234567"}, {Kind: "episode", ID: "tt7654321:1:1"}}
	input := map[string]any{"media": media}
	requireStatus(t, nativeCall(t, h, "POST", path, "", input, nil), 401)
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"media": []catalog.MediaIdentity{media[0], media[0]}}, nil), 400)
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, map[string]any{"media": []catalog.MediaIdentity{}}, nil), 400)
	var before, after int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM catalog_media").Scan(&before); err != nil {
		t.Fatal(err)
	}
	r := nativeCall(t, h, "POST", path, pair.AccessToken, input, nil)
	requireStatus(t, r, 200)
	var result struct {
		Items []watchstate.NativeDownloadWatch `json:"items"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil || len(result.Items) != 2 || result.Items[1].State.Media != media[1] {
		t.Fatal(r.Body.String(), err)
	}
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM catalog_media").Scan(&after); err != nil || before != after {
		t.Fatal("reading an unmapped identity created catalog membership", before, after, err)
	}
}
