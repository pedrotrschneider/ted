package httpapi

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/realdebrid"
)

// Explicitly opted-in, read-only live verification through the production authenticated handlers.
func TestLiveRDFixtures(t *testing.T) {
	if os.Getenv("TED_RD_LIVE_TEST") != "1" {
		t.Skip("live RD verification not requested")
	}
	key := os.Getenv("TED_REAL_DEBRID_API_KEY")
	if key == "" {
		t.Fatal("missing live RD credential")
	}
	f := setup(t)
	f.api.rd = realdebrid.New(key)
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	targets := map[string]bool{"The.Dark.Knight.Rises.2012.RERiP.2160p.UHD.BluRay.x265-TERMiNAL": false, "[INDEX] Birdy the Mighty Decode [US.DVD][x264][10-Bit][AC3] (Dual Audio)": false}
	seen := map[string]bool{}
	complete := false
	for page := 1; page <= 100; page++ {
		path := "/api/admin/rd/torrents?page=" + strconv.Itoa(page)
		r := call(t, h, "GET", path, nil, cookie)
		requireStatus(t, r, 200)
		if strings.Contains(r.Body.String(), key) || strings.Contains(r.Body.String(), `"links"`) {
			t.Fatal("sensitive data exposed")
		}
		var result realdebrid.Page
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, torrent := range result.Torrents {
			if seen[torrent.ID] {
				t.Fatal("account changed during scan")
			}
			seen[torrent.ID] = true
			if _, ok := targets[torrent.Name]; !ok {
				continue
			}
			if targets[torrent.Name] {
				t.Fatal("ambiguous fixture")
			}
			targets[torrent.Name] = true
			detailResponse := call(t, h, "GET", "/api/admin/rd/torrents/"+torrent.ID, nil, cookie)
			requireStatus(t, detailResponse, 200)
			var detail realdebrid.Detail
			if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			visible, extras := 0, 0
			for _, file := range detail.Files {
				if file.SuggestedExclusion {
					extras++
				}
				if file.Video && !file.SuggestedExclusion {
					visible++
					if !file.Selected {
						t.Fatal("fixture video is no longer selected")
					}
				}
			}
			want := 1
			if strings.HasPrefix(torrent.Name, "[INDEX]") {
				want = 26
			}
			if visible != want {
				t.Fatalf("fixture visible videos=%d want=%d", visible, want)
			}
			t.Logf("Verified %s: %d visible videos, %d extra/sample files; remote selections unchanged", torrent.Name, visible, extras)
			if want == 1 {
				popular := call(t, h, "GET", "/api/movies", nil, cookie)
				requireStatus(t, popular, 200)
				var first cinemeta.Page
				json.Unmarshal(popular.Body.Bytes(), &first)
				if first.NextSkip == nil || *first.NextSkip != 50 {
					t.Fatal("unexpected popular pagination")
				}
				next := call(t, h, "GET", "/api/movies?skip=50", nil, cookie)
				requireStatus(t, next, 200)
				var second cinemeta.Page
				json.Unmarshal(next.Body.Bytes(), &second)
				for _, m := range first.Movies {
					for _, n := range second.Movies {
						if m.ID == n.ID {
							t.Fatal("popular catalog changed or overlapped during verification")
						}
					}
				}
				search := call(t, h, "GET", "/api/movies?search=The%20Dark%20Knight%20Rises", nil, cookie)
				requireStatus(t, search, 200)
				var matches cinemeta.Page
				json.Unmarshal(search.Body.Bytes(), &matches)
				found := false
				for _, m := range matches.Movies {
					if m.ID == "tt1345836" {
						found = true
					}
				}
				if !found || matches.NextSkip != nil {
					t.Fatal("movie search did not return the expected identity")
				}
				requireStatus(t, call(t, h, "GET", "/api/movies/tt1345836", nil, cookie), 200)
				var file realdebrid.File
				for _, candidate := range detail.Files {
					if candidate.Video && !candidate.SuggestedExclusion {
						file = candidate
					}
				}
				selection := catalog.Selection{TorrentID: detail.ID, Hash: detail.Hash, FileID: file.ID, Path: file.Path, Bytes: file.Bytes}
				saved := call(t, h, "PUT", mappingPath, selection, cookie)
				requireStatus(t, saved, 200)
				var m catalog.Mapping
				json.Unmarshal(saved.Body.Bytes(), &m)
				if m.Movie.ID != "tt1345836" || m.Selection != selection || m.AccountID == "" || m.Status != "draft" {
					t.Fatal("incorrect live movie mapping")
				}
				requireStatus(t, call(t, h, "GET", mappingPath, nil, cookie), 200)
				t.Log("Verified live Cinemeta popular pages, search, movie details, RD account identity and saved root-MKV draft in disposable PostgreSQL; no provider mutations")
			}
		}
		if result.NextPage == nil {
			complete = true
			break
		}
	}
	if !complete {
		t.Fatal("bounded fixture scan did not finish")
	}
	for name, found := range targets {
		if !found {
			t.Fatalf("fixture not found: %s", name)
		}
	}
}
