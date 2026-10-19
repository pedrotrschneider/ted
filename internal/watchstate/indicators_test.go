package watchstate

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"testing"
)

func TestOtherIndicatorsExcludeSharedJointsAndDeduplicateReleasedEpisodes(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := New(pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	users := map[string]int64{}
	for _, name := range []string{"pedro", "clara", "lucas", "shared", "shared2", "private", "private2", "disabled"} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES($1,$1,'unused','user') RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		users[name] = id
	}
	for _, pair := range [][2]string{{"shared", "pedro"}, {"shared", "clara"}, {"shared2", "pedro"}, {"shared2", "clara"}, {"private", "clara"}, {"private2", "clara"}} {
		exec(`INSERT INTO joint_account_members(joint_id,member_id,color) VALUES($1,$2,'#123456')`, users[pair[0]], users[pair[1]])
	}
	exec(`UPDATE users SET disabled=true WHERE id=$1`, users["disabled"])
	const series = "tt1234567"
	const movie = "tt7654321"
	type episode struct {
		ID      string `json:"id"`
		Season  int    `json:"season"`
		Release string `json:"release_date"`
	}
	schedule := []episode{{series + ":1:1", 1, "2026-01-01"}, {series + ":1:2", 1, "2026-01-01"}, {series + ":1:3", 1, "2026-01-01"}, {series + ":1:4", 1, "2027-01-01"}, {series + ":0:1", 0, "2026-01-01"}}
	raw, _ := json.Marshal(schedule)
	exec(`INSERT INTO catalog_episode_schedules VALUES($1,$2,now(),now())`, series, raw)
	media := map[string]string{}
	for _, id := range []string{movie, series + ":1:1", series + ":1:2", series + ":1:3", series + ":1:4", series + ":0:1"} {
		kind := "episode"
		if id == movie {
			kind = "movie"
		}
		var mid string
		if err := pool.QueryRow(ctx, `INSERT INTO catalog_media(kind,cinemeta_id,metadata) VALUES($1,$2,'{}') RETURNING id::text`, kind, id).Scan(&mid); err != nil {
			t.Fatal(err)
		}
		media[id] = mid
	}
	state := func(user, id string, played bool, position int64) {
		t.Helper()
		v, _ := json.Marshal(map[string]any{"played": played, "position_ticks": position})
		exec(`INSERT INTO watch_current(user_id,media_id,state,source,observed_at,duration_ms) VALUES($1,$2,$3,'TogglePlayed',now(),100000)`, users[user], media[id], v)
	}
	for _, v := range [][2]string{{"pedro", ":1:1"}, {"clara", ":1:1"}, {"shared", ":1:2"}, {"shared2", ":1:2"}, {"private", ":1:3"}, {"private2", ":1:3"}, {"private", ":1:1"}, {"private", ":1:4"}, {"private", ":0:1"}} {
		state(v[0], series+v[1], true, 0)
	}
	state("clara", movie, false, 250000000)
	state("private", movie, false, 500000000)
	state("shared", movie, true, 0)
	state("disabled", movie, true, 0)
	targets := []catalog.MediaIdentity{{Kind: "series", ID: series}, {Kind: "movie", ID: movie}, {Kind: "episode", ID: series + ":1:2"}}
	get := func(viewer string) map[string][]UserIndicator {
		t.Helper()
		v, e := store.OtherIndicators(ctx, users[viewer], targets, "2026-10-17")
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	find := func(values []UserIndicator, name string) UserIndicator {
		t.Helper()
		for _, v := range values {
			if v.Name == name {
				return v
			}
		}
		t.Fatalf("missing %s in %v", name, values)
		return UserIndicator{}
	}
	// Personal ratings win even when lower; otherwise choose the highest
	// included joint rating. Rating-only unseen users must remain invisible.
	for _, v := range []struct {
		name, kind, id string
		rating         int
	}{
		{"clara", "movie", movie, 3}, {"private", "movie", movie, 6}, {"private2", "movie", movie, 5}, {"shared", "movie", movie, 7},
		{"private", "series", series, 4}, {"private2", "series", series, 6}, {"shared", "series", series, 7},
		{"lucas", "movie", movie, 7},
	} {
		exec(`INSERT INTO account_title_preferences(user_id,kind,title_id,rating,metadata) VALUES($1,$2,$3,$4,'{}')`, users[v.name], v.kind, v.id, v.rating)
	}
	lucas := get("lucas")
	if rating := find(lucas["movie/"+movie], "clara").Rating; rating == nil || *rating != 3 {
		t.Fatal("personal rating lost precedence", rating)
	}
	if rating := find(lucas["series/"+series], "clara").Rating; rating == nil || *rating != 7 {
		t.Fatal("highest included joint rating missing", rating)
	}
	pedroRatings := get("pedro")
	if rating := find(pedroRatings["series/"+series], "clara").Rating; rating == nil || *rating != 6 {
		t.Fatal("shared joint rating leaked into peer projection", rating)
	}
	for _, person := range get("clara")["movie/"+movie] {
		if person.Name == "lucas" {
			t.Fatal("unwatched rating exposed")
		}
	}

	if len(lucas["series/"+series]) != 2 {
		t.Fatal("joint hosts/disabled users leaked", lucas)
	}
	if v := find(lucas["series/"+series], "clara"); v.Watch.Watched != 3 || v.Watch.Available != 3 {
		t.Fatal("episodes doubled or future/special counted", v)
	}
	if v := find(lucas["movie/"+movie], "clara"); v.Watch.Watched != 1 || v.Watch.Progress != nil {
		t.Fatal("completed joint movie did not dominate progress", v)
	}
	pedro := get("pedro")
	clara := find(pedro["series/"+series], "clara")
	if clara.Watch.Watched != 2 || clara.Watch.Available != 3 {
		t.Fatal("shared joints were not excluded", clara)
	}
	if len(pedro["episode/"+series+":1:2"]) != 0 {
		t.Fatal("shared episode leaked into another person's badge", pedro)
	}
	if v := find(pedro["movie/"+movie], "clara"); v.Watch.Watched != 0 || v.Watch.Progress == nil || *v.Watch.Progress != 50 {
		t.Fatal("partial union did not preserve max progress", v)
	}
	season := 1
	seasons, e := store.OtherSeasonIndicators(ctx, users["pedro"], series, &season, "2026-10-17")
	if e != nil {
		t.Fatal(e)
	}
	if v := find(seasons["1"], "clara"); v.Watch.Watched != 2 || v.Watch.Available != 3 {
		t.Fatal("season union differs from series", v)
	}
	jointLogin := get("shared")
	if v := find(jointLogin["series/"+series], "clara"); v.Watch.Watched != 3 {
		t.Fatal("joint login projection failed", v)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM watch_current`).Scan(&count); err != nil || count != 13 {
		t.Fatal("read changed watch state", count, err)
	}
	exec(`DELETE FROM joint_account_members WHERE member_id=$1 AND joint_id=ANY($2::bigint[])`, users["pedro"], []int64{users["shared"], users["shared2"]})
	if v := find(get("pedro")["series/"+series], "clara"); v.Watch.Watched != 3 {
		t.Fatal("membership removal did not change projection", v)
	}
	exec(`INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,'playback_started','{"position_ticks":1}',now())`, users["lucas"], media[movie])
	if v := find(get("pedro")["movie/"+movie], "lucas"); !v.Watch.Seen || v.Watch.Watched != 0 || v.Watch.Progress != nil {
		t.Fatal("retained viewing was lost or converted to completion", v)
	}
	exec(`UPDATE users SET disabled=true WHERE id=ANY($1::bigint[])`, []int64{users["private"], users["private2"]})
	if v := find(get("pedro")["series/"+series], "clara"); v.Watch.Watched != 2 {
		t.Fatal("disabled joint history remained in the union", v)
	}
	exec(`UPDATE users SET disabled=true WHERE id=$1`, users["clara"])
	for _, v := range get("lucas")["movie/"+movie] {
		if v.Name == "clara" {
			t.Fatal("disabled user still visible", v)
		}
	}
}
