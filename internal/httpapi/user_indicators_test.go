package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/watchstate"
	"image/color"
	"strconv"
	"testing"
)

func TestOtherIndicatorsReachNativeTitlesSeasonsAndWebWithoutChangingPersonalState(t *testing.T) {
	f, series := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	var peer int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'peer','Peer',password_hash,'user' FROM users WHERE id=$1 RETURNING id`, f.admin.ID).Scan(&peer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $1,media_id,state,source,observed_at FROM watch_current WHERE user_id=$2 LIMIT 1`, peer, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	titles := []nativeTitleDetail{{Media: catalog.MediaIdentity{Kind: "series", ID: series.ID}}}
	if err := f.api.decorateNativeState(ctx, f.admin.ID, titles); err != nil {
		t.Fatal(err)
	}
	if len(titles[0].UserWatches) != 1 || titles[0].UserWatches[0].AccountID != peer || titles[0].EpisodeCounts.Watched != 2 {
		t.Fatal("peer projection modified personal state", titles)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	response := nativeCall(t, h, "GET", "/api/client/v1/titles/series/"+series.ID+"/seasons/1/watch-state", pair.AccessToken, nil, nil)
	requireStatus(t, response, 200)
	var seasons struct {
		UserCounts map[string][]watchstate.UserIndicator `json:"user_counts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &seasons); err != nil || len(seasons.UserCounts["1"]) != 1 || seasons.UserCounts["1"][0].Watch.Watched != 1 {
		t.Fatal("season peer projection missing", response.Body.String(), err)
	}
	cookie := login(t, h, "admin")
	response = call(t, h, "GET", "/api/title-statuses?kind=series&ids="+series.ID, nil, cookie)
	requireStatus(t, response, 200)
	var page struct {
		Statuses []titleStatus `json:"statuses"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Statuses) != 1 || len(page.Statuses[0].UserWatches) != 1 || page.Statuses[0].Watch.Watched != 2 {
		t.Fatal("web peer projection missing", response.Body.String(), err)
	}
}

func TestOtherAccountPicturesRequireAuthenticationAndHideJointOrDisabledAccounts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	h := f.api.Handler()
	cookie := login(t, h, "admin")
	peer, err := f.service.Create(ctx, identity(f.admin), "peer", "Peer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	image := avatarPicture(t, color.RGBA{B: 255, A: 255})
	if _, err = f.service.SetAvatar(ctx, identity(peer), image); err != nil {
		t.Fatal(err)
	}
	path := "/api/accounts/" + strconv.FormatInt(peer.ID, 10) + "/avatar"
	requireStatus(t, call(t, h, "GET", path, nil, nil), 401)
	response := call(t, h, "GET", path, nil, cookie)
	requireStatus(t, response, 200)
	own, err := f.service.Avatar(ctx, identity(peer))
	if err != nil || !bytes.Equal(own, response.Body.Bytes()) {
		t.Fatal("wrong person's image", err)
	}
	pair := nativeHTTPLogin(t, h)
	nativePath := "/api/client/v1/accounts/" + strconv.FormatInt(peer.ID, 10) + "/avatar"
	requireStatus(t, nativeCall(t, h, "GET", nativePath, "", nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "GET", nativePath, pair.AccessToken, nil, nil), 200)
	requireStatus(t, call(t, h, "PUT", path, nil, cookie), 405)
	if err = f.service.ChangeJoint(ctx, identity(peer), "add", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, call(t, h, "GET", path, nil, cookie), 404)
	if err = f.service.ChangeJoint(ctx, identity(peer), "remove", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE users SET disabled=true WHERE id=$1`, peer.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "GET", nativePath, pair.AccessToken, nil, nil), 404)
}
