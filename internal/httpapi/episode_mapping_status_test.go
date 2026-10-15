package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"entertainmentdistrict/internal/streaming"
)

func TestEpisodeMappingStatusVisibleToRegularProfiles(t *testing.T) {
	f, exec := posterFixture(t)
	service, err := streaming.New(context.Background(), f.pool, nil, f.api.logger, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming = service
	posterMedia(t, f, exec, "tt1245698:1:1", "episode", true)
	posterMedia(t, f, exec, "tt1245698:1:2", "episode", false)
	if _, err = f.service.Create(context.Background(), identity(f.admin), "viewer", "Viewer", testPassword, "user"); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	cookie := login(t, h, "viewer")
	for _, tc := range []struct {
		id               string
		mapped, eligible bool
	}{
		{"tt1245698:0:1", false, false},
		{"tt1245698:1:1", true, true},
		{"tt1245698:1:2", true, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			path := "/api/episodes/" + tc.id + "/local"
			response := call(t, h, "GET", path, nil, cookie)
			requireStatus(t, response, 200)
			var state map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state["mapped"] != tc.mapped || state["eligible"] != tc.eligible {
				t.Fatal(state)
			}
			for _, key := range []string{"torrent_id", "source_snapshot", "file_path", "mapping_id"} {
				if _, ok := state[key]; ok {
					t.Fatalf("private field: %s", key)
				}
			}
			requireStatus(t, call(t, h, "GET", path, nil, nil), 401)
			requireStatus(t, call(t, h, "GET", "/api/admin/episodes/"+tc.id+"/streaming", nil, cookie), 403)
		})
	}
}
