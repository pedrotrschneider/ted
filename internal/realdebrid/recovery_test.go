package realdebrid

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRecoveryPreservesPartialEntryAndPersistsBeforeExactFileSelection(t *testing.T) {
	want := FileRequest{"123", "original", strings.Repeat("a", 40), "/movie.mkv", 1000}
	for _, saveFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(saveFailure), func(t *testing.T) {
			remembered, selected := false, false
			detail := func(id string, selection int, status string) string {
				return strings.TrimSuffix(strings.Replace(item(id), `"downloaded"`, `"`+status+`"`, 1), "}") + fmt.Sprintf(`,"hash":%q,"files":[{"id":42,"path":"/movie.mkv","bytes":1000,"selected":%d}]}`, want.Hash, selection)
			}
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"id":123}`)
				case "/torrents/info/original":
					fmt.Fprint(w, detail("original", 0, "downloaded"))
				case "/torrents":
					w.Header().Set("X-Total-Count", "1")
					fmt.Fprint(w, "["+detail("original", 0, "downloaded")+"]")
				case "/torrents/addMagnet":
					r.ParseForm()
					if r.Form.Get("magnet") != "magnet:?xt=urn:btih:"+want.Hash {
						t.Error("wrong hash")
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"owned"}`)
				case "/torrents/info/owned":
					fmt.Fprint(w, detail("owned", 0, "waiting_files_selection"))
				case "/torrents/selectFiles/owned":
					if !remembered {
						t.Error("selection before persistence")
					}
					r.ParseForm()
					if r.Form.Get("files") != "42" {
						t.Error("not the exact requested file")
					}
					selected = true
					w.WriteHeader(204)
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			prepared, e := c.PrepareFile(context.Background(), want, "", func(id string) error {
				if id != "owned" {
					t.Error("wrong owned ID")
				}
				if saveFailure {
					return errors.New("controlled database failure")
				}
				remembered = true
				return nil
			})
			if saveFailure {
				if e == nil || selected {
					t.Fatal("mutated unrecorded torrent")
				}
			} else if e != nil || !selected || prepared.TorrentID != "owned" || want.TorrentID != "original" {
				t.Fatal("recovery contract", e)
			}
		})
	}
}
func TestMissingRecoveryRejectsIncompleteAccountRead(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":123}`)
		case "/torrents/info/missing":
			w.WriteHeader(404)
		case "/torrents":
			w.Header().Set("X-Total-Count", "1")
			fmt.Fprint(w, `[]`)
		default:
			t.Error("unexpected mutation")
			w.WriteHeader(500)
		}
	})
	_, e := c.PrepareFile(context.Background(), FileRequest{"123", "missing", strings.Repeat("a", 40), "/movie.mkv", 1000}, "", func(string) error { t.Fatal("unexpected ownership write"); return nil })
	if !errors.Is(e, ErrIncomplete) {
		t.Fatal(e)
	}
}

func TestRecoveryDoesNotReselectAnOwnedEntryForAnotherEpisode(t *testing.T) {
	want := FileRequest{"123", "missing", strings.Repeat("a", 40), "/episode.mkv", 1000}
	remembered, selected := false, false
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		detail := func(id, status string) string {
			return strings.TrimSuffix(strings.Replace(item(id), `"downloaded"`, `"`+status+`"`, 1), "}") + fmt.Sprintf(`,"hash":%q,"files":[{"id":7,"path":"/other.mkv","bytes":1000,"selected":1},{"id":42,"path":"/episode.mkv","bytes":1000,"selected":0}]}`, want.Hash)
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":123}`)
		case "/torrents/info/missing":
			w.WriteHeader(404)
		case "/torrents/info/previous":
			fmt.Fprint(w, detail("previous", "downloaded"))
		case "/torrents":
			w.Header().Set("X-Total-Count", "1")
			fmt.Fprint(w, "["+detail("previous", "downloaded")+"]")
		case "/torrents/addMagnet":
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new"}`)
		case "/torrents/info/new":
			fmt.Fprint(w, detail("new", "waiting_files_selection"))
		case "/torrents/selectFiles/new":
			_ = r.ParseForm()
			if !remembered || r.Form.Get("files") != "42" {
				t.Error("missing ownership or wrong selection")
			}
			selected = true
			w.WriteHeader(204)
		default:
			t.Error("existing entry mutated", r.URL.Path)
			w.WriteHeader(400)
		}
	})
	prepared, err := c.PrepareFile(context.Background(), want, "previous", func(id string) error {
		remembered = id == "new"
		return nil
	})
	if err != nil || !selected || prepared.TorrentID != "new" {
		t.Fatal("another episode's selection was reused or changed", err)
	}
}
