package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New("private-test-key")
	client.base = server.URL + "/"
	return client
}
func TestAccountIdentityDiscardsProfileAndRejectsMissingID(t *testing.T) {
	for _, tc := range []struct{ body, want string }{{`{"id":1234,"email":"private@example.invalid","username":"private"}`, "1234"}, {`{}`, ""}, {`{"id":-1}`, ""}, {`{"id":"1234"}`, ""}} {
		c := fake(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/user" {
				t.Error("wrong account endpoint")
			}
			fmt.Fprint(w, tc.body)
		})
		id, err := c.AccountID(context.Background())
		if id != tc.want || (tc.want == "" && !errors.Is(err, ErrIncomplete)) || (tc.want != "" && err != nil) {
			t.Fatal("incorrect account identity", err)
		}
	}
}
func item(id string) string {
	return fmt.Sprintf(`{"id":%q,"filename":"Movie","bytes":100,"status":"downloaded","progress":100,"added":"2026-10-08T12:00:00Z","links":["https://secret-provider-link"]}`, id)
}
func TestListPaginationCompletenessAndRedaction(t *testing.T) {
	client := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("wrong provider request")
		}
		w.Header().Set("X-Total-Count", "101")
		if r.URL.Query().Get("page") == "1" {
			rows := []string{}
			for i := range 100 {
				rows = append(rows, item(fmt.Sprint(i)))
			}
			fmt.Fprint(w, "["+strings.Join(rows, ",")+"]")
		} else {
			fmt.Fprint(w, "["+item("100")+"]")
		}
	})
	first, err := client.List(context.Background(), 1)
	if err != nil || len(first.Torrents) != 100 || first.NextPage == nil || *first.NextPage != 2 {
		t.Fatal("first page failed", err)
	}
	last, err := client.List(context.Background(), 2)
	if err != nil || len(last.Torrents) != 1 || last.NextPage != nil {
		t.Fatal("last page failed", err)
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "secret-provider-link") || strings.Contains(string(data), "links") {
		t.Fatal("provider URL leaked")
	}
}
func TestIncompleteResponsesNeverBecomeSuccessfulLists(t *testing.T) {
	for _, tc := range []struct{ body, total string }{
		{"null", "0"}, {"[]", "1"}, {"[" + item("1") + "," + item("1") + "]", "2"}, {`[{"id":"1"}]`, "1"}, {"[]", "broken"}, {"[] trailing", "0"},
	} {
		t.Run(tc.body+tc.total, func(t *testing.T) {
			client := fake(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Total-Count", tc.total)
				fmt.Fprint(w, tc.body)
			})
			result, err := client.List(context.Background(), 1)
			if !errors.Is(err, ErrIncomplete) || result.Torrents != nil {
				t.Fatal("partial data accepted", err)
			}
		})
	}
}
func TestDetailsPreserveSelectionsAndExcludeExtras(t *testing.T) {
	client := fake(t, func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimSuffix(item("T1"), "}")
		fmt.Fprint(w, base+`,"hash":"0123456789012345678901234567890123456789","files":[{"id":9,"path":"/movie.mkv","bytes":100,"selected":1},{"id":11,"path":"/Extra/trailer.mkv","bytes":5,"selected":1},{"id":12,"path":"/readme.txt","bytes":4,"selected":0}]}`)
	})
	detail, err := client.Torrent(context.Background(), "T1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != 3 || detail.Files[0].ID != 9 || !detail.Files[0].Video || detail.Files[0].SuggestedExclusion || !detail.Files[1].SuggestedExclusion || !detail.Files[1].Selected || detail.Files[2].Selected {
		t.Fatal("file state changed")
	}
	data, _ := json.Marshal(detail)
	if strings.Contains(string(data), "secret-provider-link") {
		t.Fatal("URL leaked")
	}
	for _, path := range []string{"/../movie.mkv", "/a/../movie.mkv", "relative.mkv"} {
		bad := fake(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, strings.TrimSuffix(item("T1"), "}")+`,"hash":"0123456789012345678901234567890123456789","files":[{"id":1,"path":%q,"bytes":1,"selected":1}]}`, path)
		})
		if _, err := bad.Torrent(context.Background(), "T1"); !errors.Is(err, ErrIncomplete) {
			t.Fatal("unsafe path accepted")
		}
	}
}
func TestProviderFailuresCancellationAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{{401, ErrCredentials}, {403, ErrCredentials}, {404, ErrNotFound}, {429, ErrLimited}, {503, ErrUnavailable}, {302, ErrUnavailable}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			client := fake(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://must-not-follow.invalid")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, "private-test-key secret-provider-link")
			})
			_, err := client.List(context.Background(), 1)
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private-test-key") {
				t.Fatal("incorrect sanitized failure", err)
			}
		})
	}
	client := fake(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request reached provider") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New("").List(context.Background(), 1); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
}
