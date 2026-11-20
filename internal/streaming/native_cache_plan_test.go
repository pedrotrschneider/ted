package streaming

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeCachePlanMeasuresResumedPacketWindowOnRealMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "fixture.mkv")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "20", "-c:v", "mpeg4", "-g", "24", "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture generation: %v %.1024s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"fixture"`)
		http.ServeContent(w, r, "fixture.mkv", time.Time{}, bytes.NewReader(data))
	})
	for _, resume := range []int64{0, 8000, 19000} {
		plan, err := BuildNativeCachePlan(ctx, handler, resume, 64<<10, 32<<10)
		if err != nil {
			t.Fatal("resume", resume, err)
		}
		if plan.Size != int64(len(data)) || plan.ResumeMS != resume || plan.DurationMS < 19000 || plan.Start < 0 || plan.End >= plan.Size || plan.End-plan.Start+1 > 64<<10 {
			t.Fatalf("invalid plan: %+v", plan)
		}
		if resume > 0 && plan.Start == 0 {
			t.Fatal("resume plan started at the file beginning")
		}
	}
}
func TestNativeCachePlanRejectsShallowAndInvalidPacketPositions(t *testing.T) {
	base := NativeCachePlan{Size: 1000, ResumeMS: 10000, Ahead: 100, Back: 50}
	packet := func(pos, size int, pts float64) string {
		return fmt.Sprintf(`{"codec_type":"video","stream_index":0,"pos":"%d","size":"%d","pts_time":"%f","flags":"K_"}`, pos, size, pts)
	}
	for _, packets := range []string{packet(100, 10, 12) + "," + packet(500, 10, 15), packet(100, 10, 10) + "," + packet(500, 10, 12), packet(995, 10, 10) + "," + packet(500, 10, 15)} {
		raw := []byte(`{"format":{"duration":"20"},"packets":[` + packets + `]}`)
		if _, err := cachePlanPackets(base, raw); err == nil {
			t.Fatal("invalid packet window accepted", packets)
		}
	}
	// Audio packets must contribute to the retained byte span, but an audio
	// timestamp cannot disguise a missing video keyframe or a shallow video probe.
	for _, packets := range []string{
		strings.ReplaceAll(packet(100, 10, 9), `"flags":"K_"`, `"flags":"__"`) + "," + packet(700, 10, 15),
		packet(100, 10, 9) + `,` + strings.ReplaceAll(packet(700, 10, 15), `"codec_type":"video"`, `"codec_type":"audio"`),
	} {
		if _, err := cachePlanPackets(base, []byte(`{"format":{"duration":"20"},"packets":[`+packets+`]}`)); err == nil {
			t.Fatal("unplayable video window accepted", packets)
		}
	}
	raw := []byte(`{"format":{"duration":"20"},"packets":[` + packet(100, 10, 9) + "," + packet(700, 10, 15) + `]}`)
	plan, err := cachePlanPackets(base, raw)
	if err != nil || plan.Start != 100 || plan.End != 199 {
		t.Fatal(plan, err)
	}
	base.Ahead = 1000
	raw = []byte(`{"format":{"duration":"20"},"packets":[` + packet(100, 10, 9) + "," + packet(700, 10, 15) + "," + strings.ReplaceAll(packet(850, 20, 15), `"codec_type":"video"`, `"codec_type":"audio"`) + `]}`)
	plan, err = cachePlanPackets(base, raw)
	if err != nil || plan.End != 869 {
		t.Fatal("last audio packet omitted from cache span", plan, err)
	}
}
