package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/settings"
	"entertainmentdistrict/internal/streaming"
)

// Opt-in diagnosis of an existing session through the production media handler.
// No viewing events, downloads, or source selections are submitted.
func TestLiveStoredSessionPlayback(t *testing.T) {
	id := os.Getenv("TED_LIVE_SESSION_UUID")
	if id == "" {
		t.Skip("no existing session supplied")
	}
	if !nativeUUID.MatchString(id) {
		t.Fatal("invalid session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, e := database.Open(ctx, os.Getenv("TED_DATABASE_URL"))
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	var g accounts.NativeGrant
	var size int64
	if e = pool.QueryRow(ctx, `SELECT ps.user_id,ps.device_id,ps.family_id::text,p.file_size FROM native_playback_sessions ps JOIN native_addon_playback p ON p.session_id=ps.id WHERE ps.id=$1`, id).Scan(&g.User.ID, &g.DeviceID, &g.FamilyID, &size); e != nil {
		t.Fatal("session unavailable")
	}
	s := New(pool, nil, nil, nil)
	s.RD, e = realdebrid.NewInventory(pool, realdebrid.NewStored(settings.New(pool)), nil)
	if e != nil {
		t.Fatal("inventory unavailable")
	}
	defer s.http.CloseIdleConnections()
	defer s.media.CloseIdleConnections()
	if os.Getenv("TED_LIVE_SESSION_TRACE") == "1" {
		s.media.Transport = preparationProbeTransport{base: s.media.Transport, t: t}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := s.Serve(w, r, g, id); e != nil {
			t.Log("media handler failure", probeResult(e))
			http.Error(w, "unavailable", 502)
		}
	})
	started := time.Now()
	raw, e := streaming.ProbeFile(ctx, size, handler)
	if e != nil {
		t.Fatal("stored session probe failed", probeResult(e))
	}
	var data struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &data) != nil {
		t.Fatal("invalid probe")
	}
	t.Logf("Stored session: validated video/audio metadata duration=%s elapsed=%s", data.Format.Duration, time.Since(started))
	relay := httptest.NewServer(handler)
	defer relay.Close()
	for _, offset := range []string{"0", "480"} {
		started = time.Now()
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-threads", "2", "-ss", offset, "-i", relay.URL, "-frames:v", "1", "-an", "-sn", "-f", "null", "-")
		if e = cmd.Run(); e != nil {
			t.Fatal("stored session frame decode failed", offset)
		}
		t.Logf("Stored session: decoded frame offset=%ss elapsed=%s", offset, time.Since(started))
	}
}
