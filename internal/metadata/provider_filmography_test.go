package metadata

import (
	"context"
	"fmt"
	"testing"

	"github.com/riverqueue/river"
)

func TestFilmographyRespectsSeriesChoicesAndSeparatesMergedProviderCredits(t *testing.T) {
	ctx := context.Background()
	credit := func(provider, id, title, name string) Credit {
		return Credit{Provider: provider, Kind: "series", ID: id, TitleID: title, Item: Item{Type: "Series", Name: name, PreferredMetadataLanguage: "en"}, Roles: []string{"Role"}}
	}
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) {
		return Document{ProviderID: "5", TVDBAppearanceChecked: true, Item: Item{Name: "Actor", Overview: "Biography"}, Credits: []Credit{
			credit("Tvdb", "1000", "tt1234567", "Merged anthology"), credit("Tmdb", "2000", "", "Separate first"), credit("Tmdb", "2001", "", "Separate second"), credit("Tvdb", "3000", "tt1111111", "Unchanged TVDB series"), credit("Tmdb", "4000", "tt1111111", "Other provider name"),
		}}, nil
	}}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	person, err := s.Ensure(ctx, "Tvdb", Request{Kind: "person", ProviderID: "5"}, Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	for _, v := range []struct{ title, ids string }{{"tt1234567", `{"Tvdb":"1000","Tmdb":"2000"}`}, {"tt7654321", `{"Tmdb":"2001"}`}} {
		if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_series_providers(title_id,provider,provider_ids,changed_by) VALUES($1,'Tmdb',$2,1)`, v.title, v.ids); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, entry := range page.Items {
		if entry.Media == nil {
			t.Fatal("choice identity was not projected", entry)
		}
		names[entry.Item.Name] = entry.Media.ID
	}
	if len(names) != 3 || names["Separate first"] != "tt1234567" || names["Separate second"] != "tt7654321" || names["Unchanged TVDB series"] != "tt1111111" {
		t.Fatal("provider choice leaked or collapsed titles", names)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_series_providers SET provider='Tvdb',revision=revision+1 WHERE title_id='tt1234567'`); err != nil {
		t.Fatal(err)
	}
	page, err = s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	names = map[string]string{}
	for _, entry := range page.Items {
		names[entry.Item.Name] = entry.Media.ID
	}
	if _, ok := names["Separate first"]; ok || names["Merged anthology"] != "tt1234567" || names["Separate second"] != "tt7654321" {
		t.Fatal("switching back ignored policy", names)
	}
}

func TestUnknownSeriesCreditsStayHiddenUntilIdentityCanBeCheckedAgainstPolicy(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) {
		return Document{ProviderID: "5", TVDBAppearanceChecked: true, Item: Item{Name: "Actor", Overview: "Biography"}, Credits: []Credit{{Provider: "Tvdb", Kind: "series", ID: "1000", Item: Item{Name: "Unverified merged credit", PreferredMetadataLanguage: "en"}, Roles: []string{}}}}, nil
	}, resolve: func(context.Context, string, string, string) (string, error) { return "tt1234567", nil }}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	person, err := s.Ensure(ctx, "Tvdb", Request{Kind: "person", ProviderID: "5"}, Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, person.Item.ID)
	if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_series_providers(title_id,provider,provider_ids,changed_by) VALUES('tt1234567','Tmdb','{"Tmdb":"2000"}',1)`); err != nil {
		t.Fatal(err)
	}
	page, err := s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || len(page.Items) != 0 || !page.Pending {
		t.Fatal("unverified foreign title leaked", page, err)
	}
	var id string
	var generation int64
	if err = s.pool.QueryRow(ctx, `SELECT reference_id::text,generation FROM metadata_credits WHERE person_id=$1`, person.Item.ID).Scan(&id, &generation); err != nil {
		t.Fatal(err)
	}
	if err = (&creditWorker{s: s}).Work(ctx, &river.Job[creditArgs]{Args: creditArgs{id, generation}}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Filmography(ctx, 1, person.Item.ID, "")
	if err != nil || len(page.Items) != 0 || page.Pending {
		t.Fatal(fmt.Sprint(page), err)
	}
}
