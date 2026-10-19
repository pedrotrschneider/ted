package titleprefs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fixture(t *testing.T) (context.Context, *pgxpool.Pool, *Store) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.New(t)
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for i := 1; i <= 3; i++ {
		if _, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) VALUES($1,$1,'unused','user')`, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, pool, New(pool)
}
func save(t *testing.T, ctx context.Context, pool *pgxpool.Pool, user int64, kind, id, field string, saved bool, rating *int) State {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	state, err := Set(ctx, tx, user, cinemeta.Movie{ID: id, Type: kind, Name: "Title " + id, Genres: []string{}}, field, saved, rating)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return state
}
func TestIndependentPreferencesPersistAndNeverFollowViewing(t *testing.T) {
	ctx, pool, s := fixture(t)
	const id = "tt1234567"
	rating := 7
	save(t, ctx, pool, 1, "movie", id, "watchlist", true, nil)
	state := save(t, ctx, pool, 1, "movie", id, "rating", false, &rating)
	if !state.Watchlist || state.Rating == nil || *state.Rating != 7 {
		t.Fatal(state)
	}
	// A completed viewing and its later unwatch must leave both preferences intact.
	for _, played := range []bool{true, false} {
		_, err := pool.Exec(ctx, `WITH c AS(INSERT INTO catalog_media(kind,cinemeta_id,metadata) VALUES('movie',$1,'{}') ON CONFLICT(cinemeta_id) DO UPDATE SET metadata=EXCLUDED.metadata RETURNING id)
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT 1,id,jsonb_build_object('played',$2::bool),'TogglePlayed',now() FROM c ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state`, id, played)
		if err != nil {
			t.Fatal(err)
		}
		page, e := New(pool).List(ctx, 1, "watchlist", "movie", "", false)
		if e != nil || len(page.Titles) != 1 {
			t.Fatal("viewing removed saved title", page, e)
		}
	}
	values, err := s.States(ctx, 2, []catalog.MediaIdentity{{Kind: "movie", ID: id}})
	if err != nil || len(values) != 0 {
		t.Fatal("cross-account preferences", values, err)
	}
	state = save(t, ctx, pool, 1, "movie", id, "rating", false, nil)
	if !state.Watchlist || state.Rating != nil {
		t.Fatal("clear rating changed watch list", state)
	}
	state = save(t, ctx, pool, 1, "movie", id, "watchlist", false, nil)
	if state.Watchlist {
		t.Fatal(state)
	}
	for _, kind := range []string{"episode", "season"} {
		tx, _ := pool.Begin(ctx)
		_, err = Set(ctx, tx, 1, cinemeta.Movie{ID: id, Type: kind, Name: "Forbidden"}, "rating", false, &rating)
		tx.Rollback(ctx)
		if err != ErrInvalid {
			t.Fatal("accepted non-title", kind, err)
		}
	}
}
func TestUnratedUsesOwnRetainedStartedHistoryAndDeduplicatesSeries(t *testing.T) {
	ctx, pool, s := fixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO joint_account_members(joint_id,member_id,color) VALUES(2,1,'#123456')`); err != nil {
		t.Fatal(err)
	}
	seed := func(user int, id string, at time.Time, position int64) {
		t.Helper()
		kind := "movie"
		if cinemeta.ValidVideoID(id) {
			kind = "episode"
		}
		_, err := pool.Exec(ctx, `WITH c AS (INSERT INTO catalog_media(kind,cinemeta_id,metadata) VALUES($1,$2,'{}') ON CONFLICT(cinemeta_id) DO UPDATE SET metadata=EXCLUDED.metadata RETURNING id)
 INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT $3,id,jsonb_build_object('played',false,'position_ticks',$4::bigint,'last_played',$5::timestamptz),'TEDNativePlayback',$5 FROM c`, kind, id, user, position, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	seed(1, "tt1000001", now.Add(-time.Hour), 150)
	seed(1, "tt1000002:1:1", now, 150)
	seed(1, "tt1000002:0:1", now.Add(-time.Minute), 150)
	seed(1, "tt1000003", now, 0)   // unqualified observations never enter
	seed(2, "tt1000004", now, 150) // joint activity never enters
	page, err := s.List(ctx, 1, "0", "", "", false)
	if err != nil || len(page.Titles) != 2 || page.Titles[0].ID != "tt1000002" || page.Titles[0].Type != "series" || page.Titles[1].ID != "tt1000001" {
		t.Fatal(page, err)
	}
	// Retain an older movie view after current state is cleared.
	_, err = pool.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT user_id,media_id,'baseline',state,observed_at FROM watch_current WHERE user_id=1 AND media_id=(SELECT id FROM catalog_media WHERE cinemeta_id='tt1000001')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE watch_current SET state='{"played":false,"position_ticks":0}' WHERE user_id=1 AND media_id=(SELECT id FROM catalog_media WHERE cinemeta_id='tt1000001')`); err != nil {
		t.Fatal(err)
	}
	rating := 6
	save(t, ctx, pool, 1, "series", "tt1000002", "rating", false, &rating)
	page, err = s.List(ctx, 1, "0", "", "", false)
	if err != nil || len(page.Titles) != 1 || page.Titles[0].ID != "tt1000001" {
		t.Fatal(page, err)
	}
	save(t, ctx, pool, 1, "movie", "tt1000005", "rating", false, &rating) // unrated titles can be rated without viewing
	page, err = s.List(ctx, 1, "6", "", "", false)
	if err != nil || len(page.Titles) != 2 {
		t.Fatal(page, err)
	}
	page, err = s.List(ctx, 2, "0", "", "", false)
	if err != nil || len(page.Titles) != 1 || page.Titles[0].ID != "tt1000004" {
		t.Fatal("joint account pooled member activity", page, err)
	}
}
func TestWatchListPaginationFilteringAndRandomSampling(t *testing.T) {
	ctx, pool, s := fixture(t)
	for i := 0; i < 57; i++ {
		save(t, ctx, pool, 1, "movie", fmt.Sprintf("tt%07d", 2000000+i), "watchlist", true, nil)
	}
	save(t, ctx, pool, 1, "series", "tt3000000", "watchlist", true, nil)
	page, err := s.List(ctx, 1, "watchlist", "movie", "", false)
	if err != nil || len(page.Titles) != 50 || page.Next == "" {
		t.Fatal(page, err)
	}
	next, err := s.List(ctx, 1, "watchlist", "movie", page.Next, false)
	if err != nil || len(next.Titles) != 7 || next.Next != "" {
		t.Fatal(next, err)
	}
	seen := map[string]bool{}
	for _, p := range []Page{page, next} {
		for _, title := range p.Titles {
			if seen[title.ID] || title.Type != "movie" {
				t.Fatal(title)
			}
			seen[title.ID] = true
		}
	}
	if _, err = s.List(ctx, 1, "watchlist", "series", page.Next, false); err != ErrInvalid {
		t.Fatal("cursor crossed filters", err)
	}
	orders := map[string]bool{}
	for i := 0; i < 4; i++ {
		random, err := s.List(ctx, 1, "watchlist", "", "", true)
		if err != nil || len(random.Titles) != 50 || random.Next != "" {
			t.Fatal(random, err)
		}
		orders[random.Titles[0].ID] = true
	}
	if len(orders) < 2 {
		t.Fatal("random home samples repeated identical first title")
	}
}
