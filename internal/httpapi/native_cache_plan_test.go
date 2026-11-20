package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/streaming"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeCachePlanUsesAuthorizedPinnedSessionWithoutViewing(t *testing.T) {
	f, h, tokens := nativePlaybackFixture(t)
	session := createNativeHTTPPlayback(t, h, tokens.AccessToken)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	dir := filepath.Join(root, nativeTestMedia)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "1.media")
	command := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "20", "-c:v", "mpeg4", "-g", "24", "-c:a", "aac", "-shortest", "-f", "matroska", file)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture generation: %v %.1024s", err, output)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	f.api.Streaming.CloseStorage()
	if err = f.api.Streaming.ConfigureStorage(root); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE media_mappings SET file_bytes=$1 WHERE id=$2`, info.Size(), nativeTestMapping); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE local_files SET total_bytes=$1,completed_bytes=$1,validated_bytes=$1 WHERE movie_id=$2`, info.Size(), nativeTestMedia); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE native_playback_sessions SET duration_ms=20000,expected_duration_ms=20000 WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/client/v1/playback/sessions/" + session.ID + "/cache-plan"
	body := map[string]any{"resume_ms": 8000, "ahead_bytes": 65536, "back_bytes": 32768}
	for _, token := range []string{"", tokens.RefreshToken} {
		requireStatus(t, nativeCall(t, h, "POST", path, token, body, nil), http.StatusUnauthorized)
	}
	other := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]any{"username": "admin", "password": testPassword, "device_id": "other-device", "device_name": "Other"}, nil)
	requireStatus(t, other, 200)
	var pair accounts.NativeTokens
	if err = json.Unmarshal(other.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "POST", path, pair.AccessToken, body, nil), 404)
	response := nativeCall(t, h, "POST", path, tokens.AccessToken, body, nil)
	requireStatus(t, response, 200)
	var plan streaming.NativeCachePlan
	if err = json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Size != info.Size() || plan.ResumeMS != 8000 || plan.Start <= 0 || plan.ETag == "" {
		t.Fatal(plan)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_history`).Scan(&count); err != nil || count != 0 {
		t.Fatal("probe created viewing", count, err)
	}
	// The same valid container is a placeholder against a much longer runtime.
	if _, err = f.pool.Exec(ctx, `UPDATE native_playback_sessions SET expected_duration_ms=100000 WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	response = nativeCall(t, h, "POST", path, tokens.AccessToken, body, nil)
	requireStatus(t, response, 422)
	var failure map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure["code"] != "source_placeholder" {
		t.Fatal("placeholder admitted", failure, err)
	}
}
