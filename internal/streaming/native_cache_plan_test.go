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
	if err != nil || plan.End < 869 || plan.End >= base.Size {
		t.Fatal("last audio packet omitted from cache span", plan, err)
	}
}

func TestNativeCachePlanIgnoresCoverArtworkOnRealMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	main := filepath.Join(root, "main.mp4")
	cover := filepath.Join(root, "cover.jpg")
	mp4 := filepath.Join(root, "covered.mp4")
	mkv := filepath.Join(root, "covered.mkv")
	commands := [][]string{
		{"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "20", "-c:v", "mpeg4", "-g", "24", "-bf", "2", "-c:a", "aac", "-shortest", main},
		{"-f", "lavfi", "-i", "color=c=blue:s=32x32", "-frames:v", "1", cover},
		{"-i", main, "-i", cover, "-map", "0", "-map", "1", "-c", "copy", "-disposition:v:1", "attached_pic", mp4},
		// Matroska reports this attachment as a video packet without a byte
		// position, matching the owner's Anime Time episode capture.
		{"-i", main, "-map", "0", "-c", "copy", "-attach", cover, "-metadata:s:t", "mimetype=image/jpeg", mkv},
	}
	for _, args := range commands {
		if output, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture generation: %v %.1024s", err, output)
		}
	}
	for _, file := range []string{mp4, mkv} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		media := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"covered-file"`)
			http.ServeContent(w, r, filepath.Base(file), time.Time{}, bytes.NewReader(data))
		})
		for _, resume := range []int64{0, 8000} {
			plan, err := BuildNativeCachePlan(ctx, media, resume, 64<<10, 32<<10)
			if err != nil {
				t.Fatal("playable video with cover artwork rejected", filepath.Ext(file), resume, err)
			}
			if plan.Start > int64(len(data))/2 || plan.End-plan.Start+1 > 64<<10 {
				t.Fatal("cover was treated as the playback window", plan)
			}
		}
	}
}

func TestNativeCachePlanIncludesLacedUntimedAudioWithoutInventingVideoCoverage(t *testing.T) {
	plan := NativeCachePlan{Size: 1 << 20, Ahead: 512 << 10, Back: 128 << 10}
	raw := []byte(`{"format":{"duration":"20"},"streams":[{"index":0,"codec_type":"video"},{"index":1,"codec_type":"audio"}],"packets":[
	{"codec_type":"video","stream_index":0,"pos":"100","size":"1000","pts_time":"0","flags":"K_"},
	{"codec_type":"video","stream_index":0,"pos":"10000","size":"1000","pts_time":"5.1","flags":"__"},
	{"codec_type":"audio","stream_index":1,"pos":"200000","size":"65536","pts_time":"5.1"},
	{"codec_type":"audio","stream_index":1,"pos":"200000","size":"65536"},
	{"codec_type":"audio","stream_index":1,"pos":"200000","size":"65536"}]}`)
	measured, err := cachePlanPackets(plan, raw)
	if err != nil || measured.End < 200000+3*65536-1 || measured.End-measured.Start+1 > plan.Ahead {
		t.Fatal("laced audio coverage lost", measured, err)
	}
	bad := strings.Replace(string(raw), `"pts_time":"5.1","flags":"__"`, `"flags":"__"`, 1)
	_, err = cachePlanPackets(plan, []byte(bad))
	if err == nil || CachePlanFailureCode(err) != "video_timestamp_invalid" {
		t.Fatal("missing video time manufactured readiness", err)
	}
}
