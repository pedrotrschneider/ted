package addons

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDeviceDownloadProbesDurationBeforePublishingAStatusVideo(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable; run through Devbox")
	}
	for _, seconds := range []string{"2", "40"} {
		t.Run(seconds+"seconds", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			file := filepath.Join(t.TempDir(), "fixture.mp4")
			command := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=1", "-t", seconds, "-c:v", "mpeg4", "-movflags", "+faststart", file)
			if e := command.Run(); e != nil {
				t.Fatal("could not generate duration fixture", e)
			}
			s, auth, g, remote, _ := fixture(t)
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/file" {
					f, e := os.Open(file)
					if e != nil {
						t.Error(e)
						w.WriteHeader(500)
						return
					}
					defer f.Close()
					w.Header().Set("Content-Type", "video/mp4")
					http.ServeContent(w, r, "fixture.mp4", time.Time{}, f)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"url": base + "/file", "name": "Duration fixture"}}})
			}))
			defer server.Close()
			base = server.URL
			remote.collection = []json.RawMessage{descriptorRaw(base)}
			s.http = server.Client()
			s.media = server.Client()
			if _, e := s.pool.Exec(ctx, `INSERT INTO native_catalog_titles(kind,title_id,metadata,observed_at) VALUES('movie','tt1234567','{"runtime":"1 min"}',now())`); e != nil {
				t.Fatal(e)
			}
			media := mediaIdentity("movie", "tt1234567")
			page, e := s.SelectedSources(ctx, auth, g, media, "")
			if e != nil || len(page.Sources) != 1 {
				t.Fatal(e)
			}
			session, e := s.CreateSession(ctx, auth, g, media, page.Sources[0].ID)
			if e != nil {
				t.Fatal(e)
			}
			descriptor, e := s.NativeDownload(ctx, g, session.ID)
			if seconds == "2" {
				if !errors.Is(e, ErrPlaceholder) {
					t.Fatal("status video became a download", e)
				}
				if _, e = s.NativeDownload(ctx, g, session.ID); !errors.Is(e, ErrPlaceholder) {
					t.Fatal("retry forgot the placeholder verdict", e)
				}
			} else if e != nil || descriptor.DurationMS < 39000 || descriptor.ExpectedDurationMS != 60000 {
				t.Fatal("valid file/runtime was rejected", descriptor, e)
			}
			var count int
			if e := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM native_watch_events)+(SELECT count(*) FROM watch_history)`).Scan(&count); e != nil || count != 0 {
				t.Fatal("download probing registered a viewing", e)
			}
		})
	}
}
