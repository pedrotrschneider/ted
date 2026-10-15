package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestResolveSelectedFileAndRejectWrongUnrestrictedIdentity(t *testing.T) {
	want := FileRequest{"123", "T1", strings.Repeat("a", 40), "/movie.mkv", 1000}
	for _, bad := range []bool{false, true} {
		c := fake(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				fmt.Fprint(w, `{"id":123}`)
			case "/torrents/info/T1":
				fmt.Fprint(w, strings.TrimSuffix(item("T1"), "}")+`,"hash":"`+want.Hash+`","files":[{"id":1,"path":"/text.txt","bytes":5,"selected":0},{"id":9,"path":"/Sample/sample.mkv","bytes":50,"selected":1},{"id":42,"path":"/movie.mkv","bytes":1000,"selected":1}],"links":["https://real-debrid.com/d/sample","https://real-debrid.com/d/movie"]}`)
			case "/unrestrict/link":
				r.ParseForm()
				if r.Form.Get("link") != "https://real-debrid.com/d/movie" {
					t.Error("wrong selected-file link")
				}
				name := "movie.mkv"
				if bad {
					name = "sample.mkv"
				}
				fmt.Fprintf(w, `{"download":"https://server.download.real-debrid.com/secret","filename":%q,"filesize":1000}`, name)
			}
		})
		result, err := c.ResolveFile(context.Background(), want)
		if bad {
			if !errors.Is(err, ErrMismatch) {
				t.Fatal(err)
			}
		} else if err != nil || result.Bytes != 1000 {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "secret") {
			t.Fatal("URL serialized")
		}
	}
}
func TestLiveMappedFileResolution(t *testing.T) {
	if os.Getenv("TED_STREAM_LIVE_TEST") != "1" {
		t.Skip("explicit live streaming verification not requested")
	}
	var want FileRequest
	if json.Unmarshal([]byte(os.Getenv("TED_STREAM_LIVE_MAPPING")), &want) != nil {
		t.Fatal("invalid live mapping")
	}
	result, err := New(os.Getenv("TED_REAL_DEBRID_API_KEY")).ResolveFile(context.Background(), want)
	if err != nil {
		t.Fatal("live file resolution failed:", err)
	}
	if result.Bytes != want.Bytes || result.URL == "" {
		t.Fatal("invalid resolved file")
	}
	t.Log("RD selected-file link association verified by unrestricted filename and size; URL not printed")
}
