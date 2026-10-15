package streaming

import (
	"context"
	"fmt"
	"testing"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
)

type largeMappingCatalog struct{ count int }

func (r largeMappingCatalog) Series(_ context.Context, id string) (cinemeta.Series, error) {
	result := cinemeta.Series{Movie: cinemeta.Movie{ID: id, Type: "series", Name: "Large show", Year: "2012", Genres: []string{}}}
	for n := 1; n <= r.count; n++ {
		result.Episodes = append(result.Episodes, cinemeta.Episode{ID: fmt.Sprintf("%s:1:%d", id, n), Season: 1, Number: n, Name: fmt.Sprint(n)})
	}
	return result, nil
}
func largeSelection(count int) catalog.SeriesSelection {
	result := catalog.SeriesSelection{Category: "anime"}
	for n := 1; n <= count; n++ {
		result.Files = append(result.Files, catalog.EpisodeSelection{EpisodeID: fmt.Sprintf("tt1245698:1:%d", n), Selection: mappingSelection("T2", "/new.mkv")})
	}
	return result
}
func currentBatch(t *testing.T, s *Service, submission string) MappingOperation {
	t.Helper()
	var op MappingOperation
	if err := s.pool.QueryRow(context.Background(), "SELECT id::text FROM mapping_operations WHERE submission_id=$1 ORDER BY sequence DESC LIMIT 1", submission).Scan(&op.ID); err != nil {
		t.Fatal(err)
	}
	return op
}
func TestMappingSubmissionBoundaries(t *testing.T) {
	for _, tc := range []struct{ size, total, batches, last int }{{200, 200, 1, 200}, {200, 201, 2, 1}, {200, 500, 3, 100}, {500, 500, 1, 500}, {500, 501, 2, 1}, {500, 1743, 4, 243}} {
		t.Run(fmt.Sprintf("%d/%d", tc.total, tc.size), func(t *testing.T) {
			s, _, who, _ := mappingTestService(t)
			s.mappingBatchSize = tc.size
			op, err := s.SubmitMapping(context.Background(), who, "tt1245698", "series", largeSelection(tc.total))
			if err != nil || op.Total != tc.total || op.Batches != tc.batches || op.Batch != 1 {
				t.Fatal(op, err)
			}
			var children, first, last int
			if err = s.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM mapping_operations WHERE submission_id=$1),
    (SELECT jsonb_array_length(request->'files') FROM mapping_operations WHERE submission_id=$1),
    (SELECT count(*) FROM mapping_submission_files WHERE submission_id=$1 AND batch_number=$2)`, op.ID, tc.batches).Scan(&children, &first, &last); err != nil || children != 1 || first != min(tc.size, tc.total) || last != tc.last {
				t.Fatal(children, first, last, err)
			}
		})
	}
}
