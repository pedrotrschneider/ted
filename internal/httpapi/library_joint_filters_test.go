package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"entertainmentdistrict/internal/accounts"
)

func jointFilterUser(t *testing.T, f *fixture, name string) accounts.User {
	t.Helper()
	u, err := f.service.Create(context.Background(), identity(f.admin), name, name, testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLibraryJointWatchedUnionSelectionAndFilters(t *testing.T) {
	for _, kind := range []string{"movie", "series"} {
		t.Run(kind, func(t *testing.T) {
			f, exec := posterFixture(t)
			home, other := jointFilterUser(t, f, "home"), jointFilterUser(t, f, "other")
			ctx := context.Background()
			for _, host := range []accounts.User{home, other} {
				if err := f.service.ChangeJoint(ctx, identity(host), "add", f.admin.ID, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.ChangeJoint(ctx, identity(f.admin), "color", home.ID, "#123456"); err != nil {
				t.Fatal(err)
			}
			media := []string{}
			for i := 1; i <= 4; i++ {
				id := fmt.Sprintf("tt700000%d", i)
				mid, mkind := id, "movie"
				if kind == "series" {
					mid, mkind = id+":1:1", "episode"
					exec(`INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES($1,'shows',jsonb_build_object('name',$1::text,'genres','["Action"]'::jsonb))`, id)
				}
				var mediaID string
				if i == 2 {
					mediaID = posterMedia(t, f, exec, mid, mkind, true)
				} else if err := f.pool.QueryRow(ctx, `INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,'{}') RETURNING id::text`, mid, mkind).Scan(&mediaID); err != nil {
					t.Fatal(err)
				}
				media = append(media, mediaID)
				exec(`UPDATE catalog_media SET metadata=jsonb_build_object('name',split_part(cinemeta_id,':',1),'genres','["Action"]'::jsonb) WHERE id=$1`, media[i-1])
			}
			for _, v := range []struct {
				user  int64
				media int
			}{{f.admin.ID, 0}, {home.ID, 1}, {home.ID, 2}, {other.ID, 2}} {
				exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','TogglePlayed',now())`, v.user, media[v.media])
			}
			// Retained viewing qualifies even without a current completion flag.
			exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,'playback_started','{"position_ticks":1}',now())`, other.ID, media[3])
			cookie := login(t, f.api.Handler(), "admin")
			base := "kind=" + kind + "&watched=true"
			for _, tc := range []struct {
				filter string
				want   []string
			}{
				{"", []string{"tt7000001", "tt7000002", "tt7000003", "tt7000004"}},
				{fmt.Sprintf("&watch_accounts=%d", f.admin.ID), []string{"tt7000001"}},
				{fmt.Sprintf("&watch_accounts=%d,%d", home.ID, other.ID), []string{"tt7000002", "tt7000003", "tt7000004"}},
				{fmt.Sprintf("&watch_accounts=%d", other.ID), []string{"tt7000003", "tt7000004"}},
				{"&watch_accounts=", []string{}},
				{"&saved=true&genre=Action", []string{"tt7000002"}},
				{"&search=7000004", []string{"tt7000004"}},
			} {
				if got := libraryIDs(t, f, cookie, base+tc.filter); !reflect.DeepEqual(got, tc.want) {
					t.Fatal(tc.filter, got, tc.want)
				}
			}
			response := call(t, f.api.Handler(), "GET", "/api/library-titles?"+base, nil, cookie)
			var page struct {
				Accounts []libraryWatchAccount `json:"watch_accounts"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Accounts) != 3 || page.Accounts[0].ID != f.admin.ID || page.Accounts[0].Color != "#FFFFFF" || page.Accounts[1].ID != home.ID || page.Accounts[1].Color != "#123456" {
				t.Fatal(page)
			}
			// Read-only union must not copy joint state into the caller.
			var count int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM watch_current WHERE user_id=$1`, f.admin.ID).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestLibraryJointFilterMembershipAndDisabledFences(t *testing.T) {
	f, exec := posterFixture(t)
	ctx := context.Background()
	host, outsider := jointFilterUser(t, f, "home"), jointFilterUser(t, f, "outsider")
	media := posterMedia(t, f, exec, "tt7100001", "movie", false)
	exec(`UPDATE catalog_media SET metadata='{"name":"Joint only","genres":[]}' WHERE id=$1`, media)
	for _, u := range []accounts.User{host, outsider} {
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','TogglePlayed',now())`, u.ID, media)
	}
	cookie := login(t, f.api.Handler(), "admin")
	query := fmt.Sprintf("kind=movie&watched=true&watch_accounts=%d", host.ID)
	if got := libraryIDs(t, f, cookie, query); len(got) != 0 {
		t.Fatal("arbitrary account disclosed", got)
	}
	if err := f.service.ChangeJoint(ctx, identity(host), "add", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := libraryIDs(t, f, cookie, query); len(got) != 1 {
		t.Fatal(got)
	}
	exec(`UPDATE users SET disabled=true WHERE id=$1`, host.ID)
	if got := libraryIDs(t, f, cookie, query); len(got) != 0 {
		t.Fatal("disabled joint disclosed", got)
	}
	exec(`UPDATE users SET disabled=false WHERE id=$1`, host.ID)
	if err := f.service.ChangeJoint(ctx, identity(f.admin), "leave", host.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := libraryIDs(t, f, cookie, query); len(got) != 0 {
		t.Fatal("former membership disclosed", got)
	}
	for _, q := range []string{"watch_accounts=abc", "watch_accounts=-1", "watch_accounts=0", "watch_accounts=1&watch_accounts=2", "watch_accounts=9223372036854775808"} {
		requireStatus(t, call(t, f.api.Handler(), "GET", "/api/library-titles?kind=movie&watched=true&"+q, nil, cookie), 400)
	}
	// Hosts do not inherit members' histories in the reverse direction.
	if err := f.service.ChangeJoint(ctx, identity(host), "add", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	own := posterMedia(t, f, exec, "tt7100002", "movie", false)
	exec(`UPDATE catalog_media SET metadata='{"name":"Member only","genres":[]}' WHERE id=$1`, own)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,'{"played":true}','TogglePlayed',now())`, f.admin.ID, own)
	if got := libraryIDs(t, f, login(t, f.api.Handler(), "home"), fmt.Sprintf("kind=movie&watched=true&watch_accounts=%d", f.admin.ID)); len(got) != 0 {
		t.Fatal("host inherited member", got)
	}
}

func TestLibraryJointFilterPaginatesUnionBeforeLimit(t *testing.T) {
	f, exec := posterFixture(t)
	host := jointFilterUser(t, f, "home")
	if err := f.service.ChangeJoint(context.Background(), identity(host), "add", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO catalog_media(cinemeta_id,kind,metadata) SELECT 'tt'||(7200000+n)::text,'movie',jsonb_build_object('name','Title '||lpad(n::text,3,'0')) FROM generate_series(1,101) n`)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true}','TogglePlayed',now() FROM catalog_media`, host.ID)
	exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,id,'{"played":true}','TogglePlayed',now() FROM catalog_media WHERE cinemeta_id='tt7200001'`, f.admin.ID)
	cookie := login(t, f.api.Handler(), "admin")
	response := call(t, f.api.Handler(), "GET", "/api/library-titles?kind=movie&watched=true", nil, cookie)
	requireStatus(t, response, 200)
	var page struct {
		Titles []struct {
			ID string `json:"id"`
		} `json:"titles"`
		Next string `json:"next_after"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Titles) != 100 || page.Next != "tt7200100" {
		t.Fatal(page)
	}
	if got := libraryIDs(t, f, cookie, "kind=movie&watched=true&after="+page.Next); !reflect.DeepEqual(got, []string{"tt7200101"}) {
		t.Fatal(got)
	}
}
