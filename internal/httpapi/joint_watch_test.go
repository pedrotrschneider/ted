package httpapi

import (
	"context"
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
