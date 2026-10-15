package streaming

import (
	"encoding/json"
	"errors"
	"testing"
)

func probeWithStreams(streams []map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"format": map[string]string{"format_name": "matroska,webm", "duration": "1420.09", "size": "1465140070"}, "streams": streams})
	return raw
}
func TestProbeAcceptsCodecLessAttachmentsWithoutDroppingMetadata(t *testing.T) {
	raw := probeWithStreams([]map[string]any{{"index": 0, "codec_type": "video", "codec_name": "hevc"}, {"index": 1, "codec_type": "audio", "codec_name": "opus"}, {"index": 2, "codec_type": "subtitle", "codec_name": "ass"}, {"index": 3, "codec_type": "attachment", "tags": map[string]string{"filename": "font.otf"}}, {"index": 4, "codec_type": "attachment"}})
	before := string(raw)
	if err := validateProbe(raw, 1465140070); err != nil {
		t.Fatal(err)
	}
	if string(raw) != before {
		t.Fatal("probe snapshot changed")
	}
	if !errors.Is(validateProbe(raw, 1), ErrProbeInvalid) {
		t.Fatal("attachment exception bypassed file identity")
	}
}
func TestProbeStillRejectsMissingPlaybackCodecsAndDuplicateIndexes(t *testing.T) {
	for _, kind := range []string{"video", "audio", "subtitle", "unknown"} {
		streams := []map[string]any{{"index": 0, "codec_type": "video", "codec_name": "hevc"}, {"index": 1, "codec_type": kind}}
		if !errors.Is(validateProbe(probeWithStreams(streams), 1465140070), ErrProbeInvalid) {
			t.Fatalf("missing codec accepted for %s", kind)
		}
	}
	for _, streams := range [][]map[string]any{
		{{"index": 0, "codec_type": "attachment"}},
		{{"index": 0, "codec_type": "video", "codec_name": "hevc"}, {"index": 0, "codec_type": "attachment"}},
		{{"index": 0, "codec_type": "video", "codec_name": "hevc"}, {"index": -1, "codec_type": "attachment"}},
	} {
		if !errors.Is(validateProbe(probeWithStreams(streams), 1465140070), ErrProbeInvalid) {
			t.Fatal("invalid tracks accepted")
		}
	}
}
