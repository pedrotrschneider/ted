package httpapi

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/streaming"
)

func TestNativePlaybackFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
		source     bool
	}{
		{"rd", "rd_unavailable", realdebrid.ErrUnavailable, true},
		{"archive", "source_archive", realdebrid.ErrArchive, true},
		{"mapping", "mapping_not_ready", streaming.ErrNotReady, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			a := &API{logger: slog.New(slog.NewJSONHandler(&log, nil))}
			request := httptest.NewRequest("POST", "/api/client/v1/playback/sessions?token=PRIVATE", nil)
			request.Header.Set("X-Teddy-Compatibility-Version", "0.3")
			request.Header.Set("Authorization", "Bearer PRIVATE")
			request.Pattern = "POST /api/client/v1/playback/sessions"
			recorder := httptest.NewRecorder()
			err := fmt.Errorf("%w: https://provider.example/PRIVATE", tc.err)
			if tc.source {
				a.nativeSourceFailure(recorder, request, err)
			} else {
				a.nativePlaybackFailure(recorder, request, err)
			}
			if !strings.Contains(log.String(), tc.code) || !strings.Contains(log.String(), request.Pattern) {
				t.Fatalf("missing failure context: %s", log.String())
			}
			if strings.Contains(log.String(), "PRIVATE") || strings.Contains(log.String(), "provider.example") {
				t.Fatalf("private details entered diagnostics: %s", log.String())
			}
			if !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatal("response lost its failure code")
			}
		})
	}
}
