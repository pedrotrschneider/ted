package metadata

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOriginalLanguageEnvelopeAndLazyBackfill(t *testing.T) {
	ctx := context.Background()
	doc := Document{ProviderID: "123", OriginalLanguage: "jpn", OriginalLanguageChecked: true, Item: Item{Name: "English translated title"}}
	s := metadataFixture(t, &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return doc, nil }})
	req := Request{Kind: "series", TitleID: "tt1234567"}
	v, err := s.Ensure(ctx, "Tvdb", req, Item{Type: "Series", Name: "Initial"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	v, err = s.Get(ctx, v.Item.ID)
	if err != nil || v.OriginalLanguage != "jpn" || v.Item.Name != "English translated title" {
		t.Fatalf("language/translation: %+v %v", v, err)
	}
	encoded, _ := json.Marshal(v.Item)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if _, ok := fields["OriginalLanguage"]; ok {
		t.Fatal("language must remain outside the descriptive item DTO")
	}
	child, err := s.Ensure(ctx, "Tvdb", Request{Kind: "episode", TitleID: req.TitleID, Season: 1, Number: 2}, Item{Type: "Episode"})
	if err != nil || child.OriginalLanguage != "jpn" {
		t.Fatalf("episode inheritance: %+v %v", child, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE metadata_entities SET request=request-'original_language'-'original_language_checked' WHERE id=$1`, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	backfill, err := s.Ensure(ctx, "Tvdb", req, Item{})
	if err != nil || backfill.Status != "queued" || backfill.Item.Name != v.Item.Name {
		t.Fatalf("retained lazy backfill: %+v %v", backfill, err)
	}
	doc.OriginalLanguage = "" // A confirmed unknown origin is cached, not retried per visit.
	runEntity(t, s, v.Item.ID)
	ready, err := s.Ensure(ctx, "Tvdb", req, Item{})
	if err != nil || ready.Status != "ready" || ready.Generation != backfill.Generation {
		t.Fatalf("unknown cache: %+v %v", ready, err)
	}
}

func TestOriginalLanguageValidation(t *testing.T) {
	for input, want := range map[string]string{"ja": "ja", "JPN": "jpn", "en-US": "en", "fr_CA": "fr", "": "", "https://secret": "", "English": ""} {
		if got := originalLanguage(input); got != want {
			t.Errorf("%q: %q, want %q", input, got, want)
		}
	}
}
