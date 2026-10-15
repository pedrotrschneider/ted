package stremio

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

const recordJSON = `{"_id":"tt1345836","_mtime":"1970-01-01T00:00:01.234Z","type":"movie","name":"Movie","state":{"timesWatched":1,"timeOffset":9876},"unrelated":{"keep":true}}`

func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c := New()
	c.base = server.URL
	return c
}
func TestSnapshotCompletenessAndConcurrentChange(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "partial", "changed", "duplicate", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if b["authKey"] != "private-key" || b["collection"] != "libraryItem" {
					t.Error("wrong request")
				}
				switch r.URL.Path {
				case "/datastoreMeta":
					reads++
					mtime := 1234
					if mode == "changed" && reads > 1 {
						mtime++
					}
					if mode == "empty" {
						fmt.Fprint(w, `{"result":[]}`)
					} else {
						fmt.Fprintf(w, `{"result":[["tt1345836",%d]]}`, mtime)
					}
				case "/datastoreGet":
					if b["all"] != true {
						t.Error("partial read")
					}
					switch mode {
					case "empty", "partial":
						fmt.Fprint(w, `{"result":[]}`)
					case "duplicate":
						fmt.Fprint(w, `{"result":[`+recordJSON+`,`+recordJSON+`]}`)
					case "malformed":
						fmt.Fprint(w, `{"result":[{"_id":"tt1345836"}]}`)
					default:
						fmt.Fprint(w, `{"result":[`+recordJSON+`]}`)
					}
				default:
					t.Error(r.URL.Path)
				}
			})
			records, err := c.Snapshot(context.Background(), "private-key")
			if mode == "complete" {
				if err != nil || len(records) != 1 || !strings.Contains(string(records[0].Raw), "unrelated") {
					t.Fatal(records, err)
				}
			} else if mode == "empty" {
				if err != nil || records == nil || len(records) != 0 {
					t.Fatal(records, err)
				}
			} else if err == nil {
				t.Fatal("incomplete/concurrent snapshot accepted")
			}
		})
	}
}
func TestStremioCredentialsCancellationAndRedaction(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"private-password private-token","code":1}}`)
	})
	_, err := c.Login(context.Background(), "test@example.com", "private-password")
	if !errors.Is(err, ErrCredentials) || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Snapshot(ctx, "private-token"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	session := Session{ID: "id", Email: "test@example.com", Key: "private-token"}
	b, _ := json.Marshal(session)
	if strings.Contains(string(b)+fmt.Sprintf("%v %#v", session, session), "private-token") {
		t.Fatal("credential exposed")
	}
	c = fake(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/private", 307)
	})
	if _, err = c.Login(context.Background(), "test@example.com", "private-password"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestMovieWatchedNeverUsesPlaybackPosition(t *testing.T) {
	r, err := parseRecord([]byte(recordJSON))
	if err != nil {
		t.Fatal(err)
	}
	r.State["timesWatched"] = json.RawMessage(`0`)
	if played, err := MovieWatched(r); err != nil || played {
		t.Fatal("partial movie imported as completed", err)
	}
	delete(r.State, "timesWatched")
	if _, err := MovieWatched(r); !errors.Is(err, ErrIncomplete) {
		t.Fatal("absence became unwatched", err)
	}
}

func TestRecordDatesNormalizeToMetadataMilliseconds(t *testing.T) {
	for _, date := range []string{"1970-01-01T00:00:01.234Z", "1970-01-01T01:00:01.234+01:00"} {
		raw := strings.Replace(recordJSON, "1970-01-01T00:00:01.234Z", date, 1)
		r, err := parseRecord([]byte(raw))
		if err != nil || r.Modified != 1234 || string(r.Raw) != raw {
			t.Fatal("date comparison or original record changed", err)
		}
	}
}
func TestRecordMalformedDatesNeverBecomeSuccessfulAbsence(t *testing.T) {
	for _, date := range []string{`null`, `1234`, `"not-a-date"`, `""`} {
		raw := strings.Replace(recordJSON, `"1970-01-01T00:00:01.234Z"`, date, 1)
		if _, err := parseRecord([]byte(raw)); !errors.Is(err, ErrIncomplete) {
			t.Fatal("invalid date accepted", date, err)
		}
	}
}

func TestStremioUnchangedMetadataAvoidsFullRead(t *testing.T) {
	previous, err := parseRecord([]byte(recordJSON))
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			calls := 0
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case "/datastoreMeta":
					if changed {
						fmt.Fprint(w, `{"result":[["tt1345836",1235]]}`)
					} else {
						fmt.Fprint(w, `{"result":[["tt1345836",1234]]}`)
					}
				case "/datastoreGet":
					fmt.Fprint(w, `{"result":[`+strings.ReplaceAll(recordJSON, "1.234Z", "1.235Z")+`]}`)
				default:
					t.Error("unexpected endpoint")
				}
			})
			records, err := c.SnapshotSince(context.Background(), "private", []Record{previous})
			if err != nil || len(records) != 1 {
				t.Fatal(records, err)
			}
			want := 1
			if changed {
				want = 4
			}
			if calls != want {
				t.Fatal(calls, want)
			}
		})
	}
}
