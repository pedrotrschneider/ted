package streaming

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestLibraryRemovalIncludesWholeSeriesAndPreservesUnrelatedMovie(t *testing.T) {
	ctx := context.Background()
	s, m, who, _ := mappingTestService(t)
	s.mappingSeries = largeMappingCatalog{2}
	op, err := s.SubmitMapping(ctx, who, "tt1245698", "series", largeSelection(2))
	if err != nil {
		t.Fatal(err)
	}
	validateMapping(t, s, op)
	removal, err := s.RemoveTitle(ctx, who, "tt1245698", "series")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM library_removal_items WHERE removal_id=$1 AND kind='episode'", removal.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM media_mappings WHERE movie_id=$1", m.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("unrelated movie removed", n, err)
	}
	if err = (&removalWorker{s: s}).Work(ctx, &river.Job[removalArgs]{JobRow: &rivertype.JobRow{}, Args: removalArgs{removal.ID}}); err != nil {
		t.Fatal(err)
	}
	status, err := s.RemovalStatus(ctx, "tt1245698", "series")
	if err != nil || status.Phase != "complete" {
		t.Fatal(status, err)
	}
}
func TestRemovalRefusesPendingMapping(t *testing.T) {
	ctx := context.Background()
	s, m, who, _ := mappingTestService(t)
	op, err := s.SubmitMapping(ctx, who, m.IMDB, "movie", catalog.SeriesSelection{Files: []catalog.EpisodeSelection{{EpisodeID: m.IMDB, Selection: mappingSelection("T2", "/new.mkv")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RemoveTitle(ctx, who, m.IMDB, "movie"); !errors.Is(err, catalog.ErrConflict) {
		t.Fatal(op, err)
	}
}
