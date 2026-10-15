package streaming

import (
	"strings"
	"testing"
)

func TestNativeTechnicalExcludesProviderFieldsAndRetainsTracks(t *testing.T) {
	raw := []byte(`{"provider":"secret","format":{"duration":"123.5","size":"1000","format_name":"matroska","filename":"https://private/secret"},"streams":[{"index":2,"codec_type":"audio","codec_name":"aac","tags":{"language":"eng","title":"English","provider":"secret"},"disposition":{"default":1,"forced":0,"secret":"secret"}}]}`)
	value, err := nativeTechnical(raw)
	if err != nil || strings.Contains(string(value), "secret") || !strings.Contains(string(value), `"language":"eng"`) || !strings.Contains(string(value), `"index":2`) {
		t.Fatal("unsafe or incomplete client technical data", err)
	}
	if duration, err := nativeDuration(raw); err != nil || duration != 123500 {
		t.Fatal(duration, err)
	}
	for _, raw := range []string{`{}`, `{"format":{},"streams":"invalid"}`, `{"format":{},"streams":[{"tags":false}]}`} {
		if _, err := nativeTechnical([]byte(raw)); err == nil {
			t.Fatal("accepted invalid technical shape")
		}
	}
}
