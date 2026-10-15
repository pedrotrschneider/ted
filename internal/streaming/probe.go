package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)

var ErrProbeInvalid = errors.New("media probe returned invalid metadata")

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 2<<20 {
		return 0, errors.New("probe output exceeded limit")
	}
	return b.Buffer.Write(data)
}
func (s *Service) ffprobe(ctx context.Context, m Media) (json.RawMessage, error) {
	return probeHandler(ctx, m.Bytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.sendMedia(w, r, m) }))
}
func probeHandler(ctx context.Context, expectedBytes int64, handler http.Handler) (json.RawMessage, error) {
	trace := &probeTrace{}
	ctx = context.WithValue(ctx, probeTraceKey{}, trace)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, ErrUnavailable
	}
	// Only a process-local relay URL enters ffprobe's arguments/output. RD links
	// and integration credentials never reach process arguments or stderr logs.
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" || (r.Method != "GET" && r.Method != "HEAD") {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-rw_timeout", "15000000", "-probesize", "10000000", "-analyzeduration", "10000000", "-show_entries", "format=format_name,duration,size,bit_rate:stream=index,codec_type,codec_name,codec_tag_string,width,height,channels,channel_layout,sample_rate,bit_rate,profile,level,pix_fmt,bits_per_raw_sample,avg_frame_rate,r_frame_rate,color_range,color_space,color_transfer,color_primaries:stream_tags=language,title:stream_disposition=default,forced:chapter=id,start_time,end_time:chapter_tags=title", "-of", "json", "http://"+listener.Addr().String()+"/probe")
	var out boundedOutput
	cmd.Stdout = &out
	var stderr probeStderr
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	if err = cmd.Run(); err != nil {
		failure := &probeFailure{Cause: ErrUnavailable, Reason: "process_failed", ExpectedBytes: expectedBytes, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), StderrSignals: stderr.signals()}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			failure.ExitCode = &code
		}
		failure.RelayEvents, failure.RelayEventsTotal = trace.snapshot()
		return nil, failure
	}
	if err = validateProbe(out.Bytes(), expectedBytes); err != nil {
		var failure *probeFailure
		if errors.As(err, &failure) {
			code := 0
			failure.ExitCode = &code
			failure.StderrSignals = stderr.signals()
			failure.RelayEvents, failure.RelayEventsTotal = trace.snapshot()
			trace.classifyIncomplete(failure)
		}
		return nil, err
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}

// ProbeFile reuses the checked, process-local relay for other selected sources;
// private upstream URLs and credentials never enter ffprobe arguments.
func ProbeFile(ctx context.Context, bytes int64, handler http.Handler) (json.RawMessage, error) {
	return probeHandler(ctx, bytes, handler)
}
func validateProbe(raw []byte, expectedBytes int64) error {
	// Explicit tags keep ffprobe's native JSON as the durable technical snapshot.
	var data struct {
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
			Name     string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Index int    `json:"index"`
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
		} `json:"streams"`
	}
	failure := &probeFailure{Cause: ErrProbeInvalid, ExpectedBytes: expectedBytes}
	reject := func(reason string) error { failure.Reason = reason; return failure }
	if json.Unmarshal(raw, &data) != nil {
		return reject("invalid_json")
	}
	failure.StreamCount = len(data.Streams)
	if data.Format.Name == "" {
		return reject("missing_format")
	}
	if len(data.Streams) == 0 {
		return reject("missing_streams")
	}
	duration, err := strconv.ParseFloat(data.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7*24*3600 {
		return reject("invalid_duration")
	}
	failure.DurationSeconds = &duration
	size, err := strconv.ParseInt(data.Format.Size, 10, 64)
	if data.Format.Size == "" {
		return reject("missing_size")
	}
	if err != nil {
		return reject("invalid_size")
	}
	failure.ReportedBytes = &size
	if size != expectedBytes {
		return reject("size_mismatch")
	}
	video := false
	seen := map[int]bool{}
	for _, stream := range data.Streams {
		index := stream.Index
		failure.StreamIndex = &index
		switch stream.Type {
		case "video", "audio", "subtitle", "attachment", "data":
			failure.StreamType = stream.Type
		default:
			failure.StreamType = "unknown"
		}
		if stream.Index < 0 {
			return reject("negative_stream_index")
		}
		if seen[stream.Index] {
			return reject("duplicate_stream_index")
		}
		if stream.Codec == "" && stream.Type != "attachment" {
			return reject("missing_codec")
		}
		seen[stream.Index] = true
		if stream.Type == "video" {
			video = true
		}
	}
	if !video {
		failure.StreamIndex = nil
		failure.StreamType = ""
		return reject("missing_video")
	}
	return nil
}
