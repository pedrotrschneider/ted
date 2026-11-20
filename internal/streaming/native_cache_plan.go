package streaming

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type NativeCachePlan struct {
	Size       int64  `json:"size"`
	ETag       string `json:"etag"`
	ResumeMS   int64  `json:"resume_ms"`
	DurationMS int64  `json:"duration_ms"`
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Ahead      int64  `json:"ahead_bytes"`
	Back       int64  `json:"back_bytes"`
}

// Packet positions are measured by the already bundled ffprobe against the
// pinned authenticated media handler. No private URL enters arguments/output.
func BuildNativeCachePlan(ctx context.Context, media http.Handler, resume, ahead, back int64) (NativeCachePlan, error) {
	if resume < 0 || resume > 604800000 || ahead < 1 || ahead > 256<<20 || back < 0 || back > 128<<20 {
		return NativeCachePlan{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return NativeCachePlan{}, ErrUnavailable
	}
	var delivered atomic.Int64
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return NativeCachePlan{}, ErrUnavailable
	}
	path := "/" + hex.EncodeToString(nonce[:]) + "/cache-plan"
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || (r.Method != "GET" && r.Method != "HEAD") {
			http.NotFound(w, r)
			return
		}
		media.ServeHTTP(&planWriter{ResponseWriter: w, delivered: &delivered, cancel: cancel}, r.WithContext(ctx))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	endpoint := "http://" + listener.Addr().String() + path
	req, err := http.NewRequestWithContext(ctx, "HEAD", endpoint, nil)
	if err != nil {
		return NativeCachePlan{}, err
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	head, err := client.Do(req)
	if err != nil {
		return NativeCachePlan{}, ErrUnavailable
	}
	head.Body.Close()
	if head.StatusCode != 200 || head.ContentLength <= 0 {
		return NativeCachePlan{}, ErrUnavailable
	}
	plan := NativeCachePlan{Size: head.ContentLength, ETag: head.Header.Get("ETag"), ResumeMS: resume, Ahead: ahead, Back: back}
	if len(plan.ETag) < 2 || len(plan.ETag) > 1024 || plan.ETag[0] != '"' {
		return NativeCachePlan{}, ErrProbeInvalid
	}
	interval := fmt.Sprintf("%.3f%%%.3f", float64(resume)/1000, float64(resume)/1000+5.25)
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-rw_timeout", "8000000", "-probesize", "10000000", "-analyzeduration", "10000000", "-read_intervals", interval, "-show_entries", "format=duration:packet=codec_type,stream_index,pos,size,pts_time,flags", "-of", "json", endpoint)
	var out boundedOutput
	var stderr probeStderr
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = 3 * time.Second
	if cmd.Run() != nil {
		return NativeCachePlan{}, ErrUnavailable
	}
	return cachePlanPackets(plan, out.Bytes())
}

type planWriter struct {
	http.ResponseWriter
	delivered *atomic.Int64
	cancel    context.CancelFunc
}

func (w *planWriter) Write(b []byte) (int, error) {
	if w.delivered.Add(int64(len(b))) > 192<<20 {
		w.cancel()
		return 0, ErrUnavailable
	}
	return w.ResponseWriter.Write(b)
}
func (w *planWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func cachePlanPackets(plan NativeCachePlan, raw []byte) (NativeCachePlan, error) {
	var data struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Packets []struct {
			Type   string `json:"codec_type"`
			Stream int    `json:"stream_index"`
			Pos    string `json:"pos"`
			Size   string `json:"size"`
			PTS    string `json:"pts_time"`
			Flags  string `json:"flags"`
		} `json:"packets"`
	}
	if len(raw) > 2<<20 || json.Unmarshal(raw, &data) != nil || len(data.Packets) > 20000 {
		return NativeCachePlan{}, ErrProbeInvalid
	}
	duration, err := strconv.ParseFloat(data.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 604800 || float64(plan.ResumeMS) >= duration*1000 {
		return NativeCachePlan{}, ErrProbeInvalid
	}
	plan.DurationMS = int64(duration * 1000)
	plan.Start = plan.Size
	plan.End = -1
	var firstPTS, lastPTS float64
	first := true
	videoStream := -1
	for _, p := range data.Packets {
		if p.Type != "audio" && p.Type != "video" {
			continue
		}
		pos, e := strconv.ParseInt(p.Pos, 10, 64)
		size, e2 := strconv.ParseInt(p.Size, 10, 64)
		pts, e3 := strconv.ParseFloat(p.PTS, 64)
		if e != nil || e2 != nil || e3 != nil || pos < 0 || size <= 0 || size > plan.Size-pos || math.IsNaN(pts) || math.IsInf(pts, 0) {
			return NativeCachePlan{}, ErrProbeInvalid
		}
		if p.Type == "video" {
			if first {
				if !strings.Contains(p.Flags, "K") {
					return NativeCachePlan{}, ErrProbeInvalid
				}
				firstPTS, videoStream, first = pts, p.Stream, false
			}
			if p.Stream == videoStream {
				lastPTS = max(lastPTS, pts)
			}
		}
		plan.Start = min(plan.Start, pos)
		plan.End = max(plan.End, pos+size-1)
	}
	// Seek must have reached a keyframe at/before the intended position and the
	// requested playable window. A shallow/failed probe is never cache readiness.
	target := min(float64(plan.ResumeMS)/1000+5, duration)
	if first || firstPTS > float64(plan.ResumeMS)/1000+0.25 || lastPTS < target-0.25 || plan.Start > plan.End {
		return NativeCachePlan{}, ErrProbeInvalid
	}
	plan.End = min(plan.End, plan.Start+plan.Ahead-1)
	return plan, nil
}
