package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/catalog"
	"errors"
	"fmt"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func artworkPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, c)
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}
func artworkUser(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `INSERT INTO users(id,username,display_name,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'admin','Admin','unused','admin')`); err != nil {
		t.Fatal(err)
	}
}
func artworkTag(t *testing.T, s *Service, id string) string {
	t.Helper()
	file, _, tag, err := s.ImageFile(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	return tag
}
func TestArtworkEditorWaitsForColdMetadataAndPreservesUploadsAcrossRefreshRestart(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return movieDocument("Movie"), nil }}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	cold, err := s.TitleArtwork(ctx, "movie", "tt1345836", false)
	if err != nil || len(cold) != 0 {
		t.Fatal("cold editor became editable", cold, err)
	}
	v, err := s.Lookup(ctx, Request{Kind: "movie", TitleID: "tt1345836"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	images, err := s.TitleArtwork(ctx, "movie", "tt1345836", false)
	if err != nil {
		t.Fatal(err)
	}
	var poster EditableArtwork
	for _, a := range images {
		if a.Type == "Primary" {
			poster = a
		}
	}
	if poster.ID == "" {
		t.Fatal(images)
	}
	raw := artworkPNG(t, color.White)
	if err = s.UploadArtwork(ctx, 1, poster.ID, poster.Generation, raw); err != nil {
		t.Fatal(err)
	}
	tag := artworkTag(t, s, poster.ID)
	if err = s.UploadArtwork(ctx, 1, poster.ID, poster.Generation, raw); !errors.Is(err, ErrArtworkChanged) {
		t.Fatal("stale editor overwrote upload", err)
	}
	if err = s.UploadArtwork(ctx, 1, poster.ID, poster.Generation+1, []byte("<svg>bad</svg>")); !errors.Is(err, ErrImage) {
		t.Fatal("invalid upload accepted", err)
	}
	if tag != artworkTag(t, s, poster.ID) {
		t.Fatal("failed upload discarded old artwork")
	}
	if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	got, err := s.Get(ctx, v.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got.Images {
		if a.ID == poster.ID && (!a.UserModified || a.Tag != tag) {
			t.Fatal("refresh discarded custom artwork", a)
		}
	}
	again, err := New(s.pool, r, s.root.Name(), s.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	file, _, _, err := again.ImageFile(ctx, poster.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("restart lost artwork", err)
	}
	var edits int
	var catalog int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM metadata_artwork_edits),(SELECT count(*) FROM catalog_media)`).Scan(&edits, &catalog); err != nil || edits != 1 || catalog != 0 {
		t.Fatal("artwork changed playback identity or edit history", edits, catalog, err)
	}
}
func TestArtworkChoicesAreCachedScopedAndNewUploadFencesHeldRemoteWorker(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return movieDocument("Movie"), nil }, artwork: func(context.Context, string, Request) ([]ArtworkChoice, error) {
		return []ArtworkChoice{{Type: "Primary", URL: "https://image.tmdb.org/t/p/original/other.png", Preview: "https://image.tmdb.org/t/p/w300/other.png"}}, nil
	}}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	assets, _ := s.Get(ctx, v.Item.ID)
	a := assets.Images[0]
	if err = s.UploadArtwork(ctx, 1, a.ID, a.Generation, artworkPNG(t, color.White)); err != nil {
		t.Fatal(err)
	}
	v0, err := s.RequestArtworkChoices(ctx, v.Item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestArtworkChoices(ctx, v.Item.ID, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='native_artwork_choices'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate choice work", count, err)
	}
	if err = (&choiceWorker{s: s}).Work(ctx, &river.Job[choiceArgs]{Args: choiceArgs{ID: v.Item.ID, Generation: v0.Generation}}); err != nil {
		t.Fatal(err)
	}
	choices, err := s.RequestArtworkChoices(ctx, v.Item.ID, false)
	if err != nil || choices.Status != "ready" || len(choices.Images) != 1 {
		t.Fatal(choices, err)
	}
	if err = s.SelectArtwork(ctx, 1, a.ID, a.Generation+1, guid("unlisted")); !errors.Is(err, ErrInvalid) {
		t.Fatal("unlisted source accepted", err)
	}
	before := artworkTag(t, s, a.ID)
	if err = s.SelectArtwork(ctx, 1, a.ID, a.Generation+1, choices.Images[0].ID); err != nil {
		t.Fatal(err)
	}
	if artworkTag(t, s, a.ID) != before {
		t.Fatal("queued choice discarded old image")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write(artworkPNG(t, color.Black))
	}))
	defer server.Close()
	s.imageHTTP = &http.Client{Transport: rewriteTransport{base: server.Listener.Addr().String(), transport: http.DefaultTransport}, Timeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- s.fetchSelectedImage(ctx, Args{ID: a.ID, Generation: a.Generation + 2}, true) }()
	<-entered
	if err = s.UploadArtwork(ctx, 1, a.ID, a.Generation+2, artworkPNG(t, color.RGBA{R: 255, A: 255})); err != nil {
		close(release)
		t.Fatal(err)
	}
	newer := artworkTag(t, s, a.ID)
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if artworkTag(t, s, a.ID) != newer {
		t.Fatal("held remote worker replaced newer upload")
	}
	// A failed choices refresh retains its complete prior list and source URLs.
	r.artwork = func(context.Context, string, Request) ([]ArtworkChoice, error) { return nil, ErrPartial }
	next, err := s.RequestArtworkChoices(ctx, v.Item.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&choiceWorker{s: s}).Work(ctx, &river.Job[choiceArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3}, Args: choiceArgs{ID: v.Item.ID, Generation: next.Generation}}); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	waiting, err := s.RequestArtworkChoices(ctx, v.Item.ID, false)
	if err != nil || waiting.Status != "queued" || len(waiting.Images) != 1 {
		t.Fatal("retry lost its last complete list or stopped polling", waiting, err)
	}
	if err = (&choiceWorker{s: s}).Work(ctx, &river.Job[choiceArgs]{JobRow: &rivertype.JobRow{Attempt: 3, MaxAttempts: 3}, Args: choiceArgs{ID: v.Item.ID, Generation: next.Generation}}); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	failed, err := s.RequestArtworkChoices(ctx, v.Item.ID, false)
	if err != nil || failed.Status != "failed" || len(failed.Images) != 1 {
		t.Fatal("partial provider read erased choices", failed, err)
	}
}
func TestArtworkFailedCommitRetainsPublishedReferenceAndAudit(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) { return movieDocument("Movie"), nil }}
	s := metadataFixture(t, r)
	artworkUser(t, s)
	v, err := s.Ensure(ctx, "Tmdb", Request{Kind: "movie", TitleID: "tt1345836"}, Item{Type: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	got, _ := s.Get(ctx, v.Item.ID)
	a := got.Images[0]
	if err = s.UploadArtwork(ctx, 1, a.ID, a.Generation, artworkPNG(t, color.White)); err != nil {
		t.Fatal(err)
	}
	old := artworkTag(t, s, a.ID)
	if _, err = s.pool.Exec(ctx, `CREATE FUNCTION reject_artwork_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled commit failure'; END $$;
 CREATE CONSTRAINT TRIGGER artwork_commit_failure AFTER UPDATE ON metadata_artwork DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_artwork_commit()`); err != nil {
		t.Fatal(err)
	}
	if err = s.UploadArtwork(ctx, 1, a.ID, a.Generation+1, artworkPNG(t, color.Black)); err == nil {
		t.Fatal("failed commit reported success")
	}
	if artworkTag(t, s, a.ID) != old {
		t.Fatal("failed commit changed image")
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM metadata_artwork_edits`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// The old file's immutable bytes remain referenced after a rollback.
	var raw json.RawMessage
	if err = s.pool.QueryRow(ctx, `SELECT jsonb_build_object('generation',generation,'digest',digest) FROM metadata_artwork WHERE id=$1`, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
}

func TestArtworkEditorShowsOneTitleSlotAndPosterPerSeasonPreservingOldReferences(t *testing.T) {
	ctx := context.Background()
	r := &fixtureReader{fetch: func(context.Context, string, Request) (Document, error) {
		return Document{ProviderID: "7", OriginalLanguageChecked: true, TVDBCastTypesChecked: true,
			Item:   Item{Name: "Show", ProviderIDs: map[string]string{"Imdb": "tt0386676", "Tvdb": "7"}},
			People: []PersonSummary{}, Images: []Image{{Type: "Backdrop", URL: "https://artworks.thetvdb.com/backdrop.jpg"}},
			Children: []Child{{Request: Request{Kind: "season", TitleID: "tt0386676", ProviderID: "8", Season: 1}},
				{Request: Request{Kind: "season", TitleID: "tt0386676", ProviderID: "9", Season: 0}}}}, nil
	}}
	s := metadataFixture(t, r)
	v, err := s.Ensure(ctx, "Tvdb", Request{Kind: "series", TitleID: "tt0386676"}, Item{Type: "Series"})
	if err != nil {
		t.Fatal(err)
	}
	runEntity(t, s, v.Item.ID)
	ep, err := s.Ensure(ctx, "Tvdb", Request{Kind: "episode", TitleID: "tt0386676", ProviderID: "10", Season: 1, Number: 1}, Item{Type: "Episode"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate already-retained extra artwork; reducing the editor must not erase it.
	if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url,user_modified)
 VALUES($1,'Backdrop',1,'https://artworks.thetvdb.com/extra.jpg',true),($1,'Art',0,'https://artworks.thetvdb.com/art.jpg',false),
 ($2,'Primary',0,'https://artworks.thetvdb.com/episode.jpg',false),
 ((SELECT id FROM metadata_entities WHERE identity='season:tt0386676:1'),'Backdrop',0,'https://artworks.thetvdb.com/season-back.jpg',false)`, v.Item.ID, ep.Item.ID); err != nil {
		t.Fatal(err)
	}
	images, err := s.TitleArtwork(ctx, "series", "tt0386676", false)
	if err != nil || len(images) != 6 {
		t.Fatal("wrong gallery scope", images, err)
	}
	want := []string{"Primary", "Logo", "Banner", "Backdrop", "Primary", "Primary"}
	for i, img := range images {
		if img.Type != want[i] || img.Index != 0 || i < 4 && img.Kind != "series" || i >= 4 && img.Kind != "season" {
			t.Fatal("extra or unsupported gallery slot", images)
		}
	}
	if images[4].Name != "Season 1" || images[5].Name != "Specials" {
		t.Fatal("season posters missing or misordered", images)
	}
	var choices, jobs, retained int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM metadata_artwork_choices),
 (SELECT count(*) FROM river_job WHERE kind IN ('native_artwork_choices','native_metadata_artwork')),
 (SELECT count(*) FROM metadata_artwork WHERE entity_id=$1 AND image_index=1 AND user_modified)`, v.Item.ID).Scan(&choices, &jobs, &retained); err != nil || choices != 0 || jobs != 0 || retained != 1 {
		t.Fatal("gallery eagerly fetched alternatives or removed saved artwork", choices, jobs, retained, err)
	}
	for _, cached := range []string{"detail", "browse"} {
		var snapshot Snapshot
		if cached == "detail" {
			snapshot, err = s.Get(ctx, v.Item.ID)
		} else {
			m := catalog.MediaIdentity{Kind: "series", ID: "tt0386676"}
			var page map[catalog.MediaIdentity]Snapshot
			page, err = s.CatalogSnapshots(ctx, []catalog.MediaIdentity{m})
			snapshot = page[m]
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, img := range snapshot.Images {
			if img.Index != 0 {
				t.Fatal("cached presentation exposed extra alternatives", cached, img)
			}
		}
	}
}

func TestArtworkEditorRejectsUnsupportedSlotsBeforeFileOrProviderEffects(t *testing.T) {
	ctx := context.Background()
	s := metadataFixture(t, &fixtureReader{})
	artworkUser(t, s)
	for _, tc := range []struct {
		kind, imageType string
		index           int
	}{
		{"movie", "Backdrop", 1}, {"movie", "Art", 0}, {"series", "Thumb", 0},
		{"season", "Logo", 0}, {"episode", "Primary", 0},
	} {
		t.Run(tc.kind+tc.imageType+fmt.Sprint(tc.index), func(t *testing.T) {
			provider := "Tvdb"
			if tc.kind == "movie" {
				provider = "Tmdb"
			}
			v, err := s.Ensure(ctx, provider, Request{Kind: tc.kind, TitleID: "tt0386676", ProviderID: "7", Season: 1, Number: 1}, Item{})
			if err != nil {
				t.Fatal(err)
			}
			var id string
			if err = s.pool.QueryRow(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url) VALUES($1,$2,$3,'') RETURNING id::text`, v.Item.ID, tc.imageType, tc.index).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err = s.UploadArtwork(ctx, 1, id, 1, []byte("invalid image")); !errors.Is(err, ErrInvalid) {
				t.Fatal("unsupported upload reached image handling", err)
			}
			if err = s.SelectArtwork(ctx, 1, id, 1, guid("no-choice")); !errors.Is(err, ErrInvalid) {
				t.Fatal("unsupported slot reached choice lookup", err)
			}
			if tc.kind == "episode" {
				if _, err = s.RequestArtworkChoices(ctx, v.Item.ID, false); !errors.Is(err, ErrInvalid) {
					t.Fatal("episode picker scheduled", err)
				}
			}
		})
	}
	var edits, choices int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM metadata_artwork_edits),(SELECT count(*) FROM metadata_artwork_choices)`).Scan(&edits, &choices); err != nil || edits != 0 || choices != 0 {
		t.Fatal("unsupported edits changed persistent state", edits, choices, err)
	}
}
