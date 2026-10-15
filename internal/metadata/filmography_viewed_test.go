package metadata

import (
	"context"
	"testing"
)

func TestFilmographyAnyViewingSeparateFromCompletion(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	person, err := s.Ensure(ctx, "Tmdb", Request{Kind: "person", ProviderID: "3894"}, Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	entity, err := s.readEntity(ctx, person.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	credits := []Credit{}
	for i, v := range []struct{ kind, id string }{{"movie", "tt1000001"}, {"series", "tt1000002"}, {"movie", "tt1000003"}, {"movie", "tt1000004"}} {
		typ := "Movie"
		if v.kind == "series" {
			typ = "Series"
		}
		credits = append(credits, Credit{Provider: "Tmdb", Kind: v.kind, ID: string(rune('1' + i)), TitleID: v.id, Item: Item{Type: typ, Name: v.id}})
	}
	if err = s.apply(ctx, entity, Document{ProviderID: "3894", Item: Item{Name: "Actor"}, Credits: credits}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'admin','Admin','unused','admin'),(2,'other','Other','unused','user')`); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ id, kind, state, source string }{
		{"tt1000001", "movie", `{"played":false,"position_ticks":70000000,"play_count":0,"last_played":null}`, "TED"},
		{"tt1000002:1:3", "episode", `{"played":false,"position_ticks":0,"play_count":0,"last_played":"2026-10-11T00:00:00Z"}`, "TEDNativePlayback"},
		{"tt1000003", "movie", `{"played":false,"position_ticks":0,"play_count":0,"last_played":null}`, "TED"},
		{"tt1000004", "movie", `{"played":false,"position_ticks":0,"play_count":0,"last_played":null}`, "TED"},
	} {
		if _, err = s.pool.Exec(ctx, `WITH c AS(INSERT INTO catalog_media(cinemeta_id,kind,metadata) VALUES($1,$2,'{}') RETURNING id) INSERT INTO watch_current(user_id,media_id,state,source,observed_at) SELECT 1,id,$3,$4,now() FROM c`, v.id, v.kind, v.state, v.source); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) SELECT 1,id,'playback_progress','{"played":false,"position_ticks":10000000,"play_count":0}',now() FROM catalog_media WHERE cinemeta_id='tt1000003'`); err != nil {
		t.Fatal(err)
	}
	page, err := s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range page.Items {
		if v.Watched == nil || *v.Watched {
			t.Fatal("started/history-only title became completed", v)
		}
		want := v.Media.ID != "tt1000004"
		if v.Viewed == nil || *v.Viewed != want {
			t.Fatal("any-viewing split", v)
		}
	}
	// Later watched edits are read from TED, without replacing provider summaries.
	if _, err = s.pool.Exec(ctx, `UPDATE watch_current SET state='{"played":true,"position_ticks":0,"play_count":1}' WHERE user_id=1 AND media_id=(SELECT id FROM catalog_media WHERE cinemeta_id='tt1000004')`); err != nil {
		t.Fatal(err)
	}
	page, err = s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range page.Items {
		if v.Viewed == nil || !*v.Viewed {
			t.Fatal("fresh viewing state was not reflected", v)
		}
	}
	other, err := s.Filmography(ctx, 2, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range other.Items {
		if v.Viewed == nil || *v.Viewed {
			t.Fatal("another user's viewing leaked", v)
		}
	}
}
