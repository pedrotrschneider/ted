package metadata

import (
	"context"
	"testing"

	"entertainmentdistrict/internal/catalog"
)

func TestTMDBCastCharactersRemovePerformanceSuffixWithoutChangingPeopleOrOtherAnnotations(t *testing.T) {
	people, err := castPeople([]tmPerson{
		{ID: 1, Name: "Shogo Sakata", Character: "Johnny Joestar (voice)"},
		{ID: 2, Name: "Yohei Azakami", Character: "Gyro Zeppeli (VOICE) "},
		{ID: 3, Name: "Voice actor", Character: "Character (uncredited)"},
		{ID: 4, Name: "Actor", Character: "The (voice) coach"},
	}, []tmPerson{{ID: 1, Name: "Shogo Sakata", Character: "Johnny Joestar"}})
	if err != nil || len(people) != 4 {
		t.Fatal(people, err)
	}
	for i, want := range []string{"Johnny Joestar", "Gyro Zeppeli", "Character (uncredited)", "The (voice) coach"} {
		if people[i].Role != want {
			t.Fatalf("character %d: got %q want %q", i, people[i].Role, want)
		}
	}
	if people[0].Name != "Shogo Sakata" || people[0].ID != "1" || people[0].Type != "Actor" {
		t.Fatal("person identity changed", people[0])
	}
}

func TestCachedTMDBCastAndFilmographyLabelsCleanWithoutProviderReadsOrStorageChanges(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(_ context.Context, p string, req Request) (Document, error) {
		if req.Kind == "person" {
			return Document{ProviderID: req.ProviderID, Item: Item{Name: "Shogo Sakata"}, Credits: []Credit{{Provider: "Tmdb", Kind: "series", ID: "45790", TitleID: "tt2359704", Item: Item{Type: "Series", Name: "JoJo"}, Roles: []string{"Johnny Joestar (voice)"}}}}, nil
		}
		d := movieDocument("Cached title")
		d.People = []PersonSummary{{Provider: p, ID: "1", Name: "Shogo Sakata", Role: "Johnny Joestar (voice)", Type: "Actor"}}
		if req.Kind == "season" {
			d.People[0].Provider = "Tmdb"
			d.FieldSources = map[string]string{"People": "Tmdb"}
		}
		return d, nil
	}}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	movie, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1234567"}, Item{Type: "Movie", Name: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, movie.Item.ID)
	series, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: "tt2359704"}, Item{Type: "Series", Name: "Series"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, series.Item.ID)
	season, err := s.Ensure(ctx, "Tvdb", Request{Kind: "season", TitleID: "tt2359704", Season: 6}, Item{Type: "Season", Name: "Season 6"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, season.Item.ID)
	for _, tc := range []struct{ id, want string }{{movie.Item.ID, "Johnny Joestar"}, {season.Item.ID, "Johnny Joestar"}, {series.Item.ID, "Johnny Joestar (voice)"}} {
		got, e := s.Get(ctx, tc.id)
		if e != nil || len(got.Item.People) != 1 || got.Item.People[0].Role != tc.want {
			t.Fatal(got, e)
		}
	}
	cached, err := s.CatalogSnapshots(ctx, []catalog.MediaIdentity{{Kind: "movie", ID: "tt1234567"}})
	if err != nil {
		t.Fatal(err)
	}
	got := cached[catalog.MediaIdentity{Kind: "movie", ID: "tt1234567"}]
	if len(got.Item.People) != 1 || got.Item.People[0].Role != "Johnny Joestar" {
		t.Fatal(got)
	}
	if _, err = s.RequestPerson(ctx, got.Item.People[0].ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, got.Item.People[0].ID)
	// No reads from retained presentation may invoke a provider to repair text.
	r.fetch = func(context.Context, string, Request) (Document, error) {
		t.Error("cached label initiated a provider read")
		return Document{}, ErrProvider
	}
	filmography, err := s.Filmography(ctx, 1, got.Item.People[0].ID, "")
	if err != nil || len(filmography.Items) != 1 || len(filmography.Items[0].Roles) != 1 || filmography.Items[0].Roles[0] != "Johnny Joestar" {
		t.Fatal(filmography, err)
	}
	var stored string
	if err = s.pool.QueryRow(ctx, `SELECT item->'People'->0->>'Role' FROM metadata_entities WHERE id=$1`, movie.Item.ID).Scan(&stored); err != nil || stored != "Johnny Joestar (voice)" {
		t.Fatal("presentation rewrote retained payload", stored, err)
	}
}
