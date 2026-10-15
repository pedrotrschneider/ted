package addons

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTorrentIdentityPriorityAndProviderBingeFormats(t *testing.T) {
	hash := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	for _, wire := range []string{
		`{"infoHash":"` + hash + `","fileIdx":0,"behaviorHints":{"bingeGroup":"comet|realdebrid|` + other + `","filename":"ep.mkv"}}`,
		`{"url":"https://example.com/private-resolver","behaviorHints":{"bingeGroup":"comet|realdebrid|` + hash + `","filename":"ep.mkv","videoSize":1000}}`,
		`{"url":"https://example.com/private-resolver","behaviorHints":{"bingeGroup":"torrentio|` + hash + `","filename":"ep.mkv"}}`,
	} {
		s, e := parseSource(json.RawMessage(wire))
		if e != nil || s.InfoHash != hash || s.URL != "" || s.ReleaseKey() == "" {
			t.Fatalf("identity or priority lost: %v", e)
		}
	}
	s, e := parseSource(json.RawMessage(`{"url":"https://example.com/file.mp4","behaviorHints":{"bingeGroup":"torrentio|1080p|HEVC","videoHash":"` + hash + `"}}`))
	if e != nil || s.InfoHash != "" || s.URL == "" {
		t.Fatal("quality or OpenSubtitles hash was mistaken for torrent identity", e)
	}
	for _, wire := range []string{`{"infoHash":"oops","url":"https://example.com/file.mp4"}`, `{"infoHash":"` + hash + `","fileIdx":-1}`, `{"fileIdx":0,"url":"https://example.com/file.mp4"}`} {
		if _, e := parseSource(json.RawMessage(wire)); e == nil {
			t.Fatal("invalid explicit torrent identity accepted")
		}
	}
}
func TestIdentifiedReleaseKeysSurviveMutableDescriptionsAndDistinguishFiles(t *testing.T) {
	a, e := parseSource(json.RawMessage(`{"infoHash":"` + strings.Repeat("a", 40) + `","fileIdx":1,"description":"7 seeders","behaviorHints":{"filename":"ep.mkv"}}`))
	if e != nil {
		t.Fatal(e)
	}
	b := a
	b.Description = "10 seeders"
	b.Name = "New provider badge"
	if a.ReleaseKey() != b.ReleaseKey() {
		t.Fatal("mutable labels changed selected release")
	}
	i := int64(2)
	b.FileIndex = &i
	if a.ReleaseKey() == b.ReleaseKey() {
		t.Fatal("another file matched the selected release")
	}
}
