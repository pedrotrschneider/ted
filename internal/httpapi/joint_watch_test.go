package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"testing"
)

func TestJointIndicatorsAreReadOnlyAndDoNotInheritMembers(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	var member int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1 RETURNING id`, f.admin.ID).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := f.api.accounts.ChangeJoint(ctx, identity(f.admin), "add", member, ""); err != nil {
		t.Fatal(err)
	}
	media := []catalog.MediaIdentity{{Kind: "series", ID: show.ID}, {Kind: "episode", ID: show.ID + ":1:1"}}
	values, err := f.api.jointWatches(ctx, member, media)
	if err != nil {
		t.Fatal(err)
	}
	v := values["series/"+show.ID]
	if len(v) != 1 || v[0].Watch.Watched != 2 || v[0].Watch.Available != 2 || !v[0].Watch.Seen {
		t.Fatal(values)
	}
	if values, err = f.api.jointWatches(ctx, f.admin.ID, media); err != nil || len(values) != 0 {
		t.Fatal("joint inherited member state", values, err)
	}
	titles := []nativeTitleDetail{{Media: media[0]}}
	if err = f.api.decorateNativeState(ctx, member, titles); err != nil {
		t.Fatal(err)
	}
	if titles[0].State.Played || titles[0].EpisodeCounts.Watched != 0 || len(titles[0].JointWatches) != 1 {
		t.Fatal("joint projection changed personal completion", titles)
	}
	var version int64
	if err = f.pool.QueryRow(ctx, `SELECT session_version FROM users WHERE id=$1`, member).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = f.api.accounts.ChangeJoint(ctx, accounts.Identity{ID: member, Version: version}, "leave", f.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	if values, err = f.api.jointWatches(ctx, member, media); err != nil || len(values) != 0 {
		t.Fatal("departed membership disclosed indicators", values, err)
	}
	if state, err := f.api.watch.NativeState(ctx, f.admin.ID, media[1]); err != nil || !state.Played {
		t.Fatal("leave modified joint history", state, err)
	}
}

func TestJointIndicatorsUseOnlyTheJointRatingAndReflectClears(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	var member int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'rating_member','Member',password_hash,'user' FROM users WHERE id=$1 RETURNING id`, f.admin.ID).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := f.api.accounts.ChangeJoint(ctx, identity(f.admin), "add", member, ""); err != nil {
		t.Fatal(err)
	}
	for account, rating := range map[int64]int{f.admin.ID: 2, member: 7} {
		if _, err := f.pool.Exec(ctx, `INSERT INTO account_title_preferences(user_id,kind,title_id,metadata,rating) VALUES($1,'series',$2,'{}',$3)`, account, show.ID, rating); err != nil {
			t.Fatal(err)
		}
	}
	titles := []nativeTitleDetail{{Media: catalog.MediaIdentity{Kind: "series", ID: show.ID}}}
	if err := f.api.decorateNativeState(ctx, member, titles); err != nil {
		t.Fatal(err)
	}
	if len(titles[0].JointWatches) != 1 || titles[0].JointWatches[0].Rating == nil || *titles[0].JointWatches[0].Rating != 2 || titles[0].UserRating == nil || *titles[0].UserRating != 7 {
		t.Fatalf("personal and joint ratings were pooled: %+v", titles[0])
	}
	raw, err := json.Marshal(titles[0].JointWatches[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil || wire["rating"] != float64(2) {
		t.Fatal(string(raw), err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE account_title_preferences SET rating=NULL WHERE user_id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.api.decorateNativeState(ctx, member, titles); err != nil || titles[0].JointWatches[0].Rating != nil || *titles[0].UserRating != 7 {
		t.Fatal("cleared joint rating retained or personal rating changed", titles, err)
	}
}

func TestJointIndicatorsKeepUnseenRatingsPrivate(t *testing.T) {
	f, _ := nativeSeasonWatchFixture(t)
	ctx := context.Background()
	var member int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'unseen_member','Member',password_hash,'user' FROM users WHERE id=$1 RETURNING id`, f.admin.ID).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if err := f.api.accounts.ChangeJoint(ctx, identity(f.admin), "add", member, ""); err != nil {
		t.Fatal(err)
	}
	media := []catalog.MediaIdentity{{Kind: "movie", ID: "tt9999999"}}
	if _, err := f.pool.Exec(ctx, `INSERT INTO account_title_preferences(user_id,kind,title_id,metadata,rating) VALUES($1,'movie',$2,'{}',7)`, f.admin.ID, media[0].ID); err != nil {
		t.Fatal(err)
	}
	values, err := f.api.jointWatches(ctx, member, media)
	if err != nil {
		t.Fatal(err)
	}
	v := values["movie/tt9999999"]
	if len(v) != 1 || v[0].Watch.Seen || v[0].Rating != nil {
		t.Fatal("unseen joint rating disclosed", values)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE users SET disabled=true WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	values, err = f.api.jointWatches(ctx, member, media)
	if err != nil || len(values) != 0 {
		t.Fatal("disabled joint disclosed indicators", values, err)
	}
}
