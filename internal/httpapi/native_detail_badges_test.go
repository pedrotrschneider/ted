package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
	"testing"
	"time"
)

func TestNativeSeasonBadgesUseReleasedSnapshotAndRespectSeasonScope(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	show.Episodes = append(show.Episodes, cinemeta.Episode{ID: show.ID + ":1:3", Season: 1, Number: 3, Name: "Future", ReleaseDate: "2099-01-01"}, cinemeta.Episode{ID: show.ID + ":2:1", Season: 2, Number: 1, Name: "Unknown"})
	if err := f.api.catalog.SaveSchedule(context.Background(), show, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	read := func(path string) map[string]nativeEpisodeCounts {
		r := nativeCall(t, h, "GET", path, pair.AccessToken, nil, nil)
		requireStatus(t, r, 200)
		var value struct {
			Counts map[string]nativeEpisodeCounts `json:"season_counts"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value.Counts
	}
	base := "/api/client/v1/titles/series/" + show.ID
	counts := read(base + "/watch-state")
	if counts["1"].Total != 2 || counts["1"].Watched != 2 || len(counts) != 1 {
		t.Fatal(counts)
	}
	if counts = read(base + "/seasons/2/watch-state"); len(counts) != 0 {
		t.Fatal("unknown/future dates fabricated a season badge", counts)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE watch_current SET state=state||'{"played":false}'::jsonb WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, show.ID+":1:2"); err != nil {
		t.Fatal(err)
	}
	counts = read(base + "/seasons/1/watch-state")
	if counts["1"].Total != 2 || counts["1"].Watched != 1 {
		t.Fatal(counts)
	}
}

func TestNativeFilmographySeparatesAnyViewingFromReleasedCompletionBadges(t *testing.T) {
	f, show := nativeSeasonWatchFixture(t)
	installMetadata(t, f)
	ctx := context.Background()
	person, err := f.api.Metadata.Ensure(ctx, "Tvdb", metadata.Request{Kind: "person", ProviderID: "10"}, metadata.Item{Type: "Person", Name: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	ref := "22222222-2222-4222-8222-222222222222"
	if _, err = f.pool.Exec(ctx, `INSERT INTO metadata_credits(person_id,provider,kind,provider_id,reference_id,summary,cinemeta_id,identity_status,roles,generation) VALUES($1,'Tvdb','series','20',$2::uuid,jsonb_build_object('Id',$2::text,'Type','Series','Name','Show'),$3,'ready','["Character"]',1)`, person.Item.ID, ref, show.ID); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	read := func() metadata.FilmographyEntry {
		result := nativeCall(t, h, "GET", "/api/client/v1/metadata/people/"+person.Item.ID+"/filmography", pair.AccessToken, nil, nil)
		requireStatus(t, result, 200)
		var page metadata.Filmography
		if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		return page.Items[0]
	}
	entry := read()
	if entry.EpisodeCounts == nil || entry.EpisodeCounts.Watched != 2 || entry.EpisodeCounts.Total != 2 || entry.Viewed == nil || !*entry.Viewed {
		t.Fatal(entry)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE watch_current SET state=state||'{"played":false}'::jsonb WHERE media_id=(SELECT id FROM catalog_media WHERE cinemeta_id=$1)`, show.ID+":1:2"); err != nil {
		t.Fatal(err)
	}
	entry = read()
	if entry.EpisodeCounts.Watched != 1 || entry.EpisodeCounts.Total != 2 || !*entry.Watched || !*entry.Viewed {
		t.Fatal("one episode is viewing, not full completion", entry)
	}
}
