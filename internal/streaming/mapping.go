package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type MappingOperation struct {
	PublicationMode  string `json:"publication_mode"`
	ID               string `json:"id"`
	Phase            string `json:"phase"`
	Error            string `json:"error"`
	Total            int    `json:"total"`
	Validated        int    `json:"validated"`
	Batch            int    `json:"batch"`
	Batches          int    `json:"batches"`
	CompletedBatches int    `json:"completed_batches"`
	Committed        int    `json:"committed"`
	Retryable        bool   `json:"retryable"`
}
type mappingArgs struct {
	ID string `json:"id"`
}

func (mappingArgs) Kind() string { return "mapping_publication" }

type mappingWorker struct {
	river.WorkerDefaults[mappingArgs]
	s *Service
}

// SubmitMapping persists the complete selection. Each bounded batch commits
// atomically and queues its successor in that same transaction.
func (s *Service) SubmitMapping(ctx context.Context, who accounts.Identity, id, kind string, selection catalog.SeriesSelection) (MappingOperation, error) {
	if !cinemeta.ValidID(id) || (kind != "movie" && kind != "series") || len(selection.Files) == 0 || len(selection.Files) > 10000 || (kind == "movie" && len(selection.Files) != 1) || (kind == "series" && selection.Category != "shows" && selection.Category != "anime") {
		return MappingOperation{}, catalog.ErrInvalid
	}
	seen := map[string]bool{}
	for _, f := range selection.Files {
		if seen[f.EpisodeID] || (kind == "series" && (!cinemeta.ValidVideoID(f.EpisodeID) || len(f.EpisodeID) <= len(id) || f.EpisodeID[:len(id)+1] != id+":")) || (kind == "movie" && f.EpisodeID != id) || f.FileID <= 0 || f.Bytes <= 0 || f.Path == "" || len(f.Hash) != 40 || f.TorrentID == "" {
			return MappingOperation{}, catalog.ErrInvalid
		}
		seen[f.EpisodeID] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MappingOperation{}, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return MappingOperation{}, err
	}
	if err = s.lockLifecycle(ctx, tx); err != nil {
		return MappingOperation{}, err
	}
	if err = titleLock(ctx, tx, id); err != nil {
		return MappingOperation{}, err
	}
	var removing bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_removals WHERE title_id=$1 AND kind=$2 AND NOT retired AND phase!='complete')", id, kind).Scan(&removing); err != nil {
		return MappingOperation{}, err
	}
	if removing {
		return MappingOperation{}, catalog.ErrConflict
	}
	// Only an explicit new mapping submission may supersede a completed tombstone.
	if _, err = tx.Exec(ctx, "UPDATE library_removals SET retired=true WHERE title_id=$1 AND kind=$2 AND NOT retired AND phase='complete'", id, kind); err != nil {
		return MappingOperation{}, err
	}
	if kind == "series" {
		var conflict bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_series sh WHERE sh.cinemeta_id=$1 AND sh.category!=$2 AND EXISTS(SELECT 1 FROM catalog_seasons se JOIN catalog_episodes e ON e.season_id=se.id JOIN media_mappings m ON m.movie_id=e.media_id WHERE se.series_id=sh.id))`, id, selection.Category).Scan(&conflict); err != nil {
			return MappingOperation{}, err
		}
		if conflict {
			return MappingOperation{}, catalog.ErrConflict
		}
	}
	// A pending batch reserves the title across the complete submission.
	raw, _ := json.Marshal(selection)
	var existing, parent string
	var old []byte
	err = tx.QueryRow(ctx, `SELECT o.id::text,COALESCE(o.submission_id,o.id)::text,COALESCE(sub.request,o.request)
 FROM mapping_operations o LEFT JOIN mapping_submissions sub ON sub.id=o.submission_id
 WHERE o.title_id=$1 AND o.kind=$2 AND o.phase NOT IN ('complete','failed')`, id, kind).Scan(&existing, &parent, &old)
	var op MappingOperation
	if err == nil {
		var prior catalog.SeriesSelection
		if json.Unmarshal(old, &prior) != nil {
			return op, ErrUnavailable
		}
		normalized, _ := json.Marshal(prior)
		if string(normalized) != string(raw) {
			return op, catalog.ErrConflict
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		actor, _ := json.Marshal(who)
		err = tx.QueryRow(ctx, `INSERT INTO mapping_submissions(title_id,kind,actor,request,batch_size,publication_mode)
   VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, id, kind, actor, raw, s.mappingBatchSize, s.publicationMode).Scan(&parent)
		if err != nil {
			return op, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO mapping_submission_files(submission_id,episode_id,batch_number)
   SELECT $1,f->>'episode_id',((ordinality-1)/$3)::integer+1
   FROM jsonb_array_elements($2::jsonb->'files') WITH ORDINALITY AS files(f,ordinality)`, parent, raw, s.mappingBatchSize)
		if err != nil {
			return op, err
		}
		if err = s.insertMappingBatch(ctx, tx, parent, 1, true); err != nil {
			return op, err
		}
	} else {
		return op, err
	}
	if err = tx.Commit(ctx); err != nil {
		return op, err
	}
	return s.MappingStatus(ctx, id, kind)
}
func (w *mappingWorker) Work(ctx context.Context, job *river.Job[mappingArgs]) error {
	if err := w.dispatch(ctx, job); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return w.s.failMapping(job.Args.ID, job.ID, job.Attempt, job.MaxAttempts, err)
	}
	return nil
}

func (w *mappingWorker) dispatch(ctx context.Context, job *river.Job[mappingArgs]) error {
	s := w.s
	var phase string
	if err := s.pool.QueryRow(ctx, `SELECT phase FROM mapping_operations WHERE id=$1`, job.Args.ID).Scan(&phase); err != nil {
		return ErrUnavailable
	}
	if phase == "complete" || phase == "failed" || phase == "publishing" {
		return nil
	}
	if phase == "queued" {
		if err := s.stageMapping(ctx, job.Args.ID); err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = s.queueMappingFiles(ctx, tx, job.Args.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// stageMapping verifies observations before storing inactive versions. Catalog
// identities may be reserved, but no active mappings or local files change here.
func (s *Service) stageMapping(ctx context.Context, op string) error {
	var id, kind string
	var who accounts.Identity
	var selected catalog.SeriesSelection
	if err := s.pool.QueryRow(ctx, `SELECT title_id,kind,actor,request FROM mapping_operations WHERE id=$1`, op).Scan(&id, &kind, &who, &selected); err != nil {
		return err
	}
	rd, ok := s.rd.(catalog.Sources)
	if !ok {
		return ErrUnavailable
	}
	if stored, ok := rd.(interface {
		Snapshot(context.Context) (*realdebrid.Client, error)
	}); ok {
		snapshot, err := stored.Snapshot(ctx)
		if err != nil {
			return err
		}
		rd = snapshot
	}
	account, err := rd.AccountID(ctx)
	if err != nil {
		return err
	}
	var title cinemeta.Movie
	episodes := map[string]cinemeta.Episode{}
	regularEpisodes := 0
	if kind == "movie" {
		title, err = s.mappingMovies.Movie(ctx, id)
	} else {
		var show cinemeta.Series
		show, err = s.mappingSeries.Series(ctx, id)
		title = show.Movie
		regularEpisodes = cinemeta.RegularEpisodeCount(show.Episodes)
		for _, e := range show.Episodes {
			episodes[e.ID] = e
		}
	}
	if err != nil {
		return err
	}
	sources := map[string]realdebrid.Detail{}
	for _, f := range selected.Files {
		if kind == "series" {
			if _, ok := episodes[f.EpisodeID]; !ok {
				return catalog.ErrInvalid
			}
		}
		d, ok := sources[f.TorrentID]
		if !ok {
			d, err = rd.Torrent(ctx, f.TorrentID)
			if err != nil {
				return err
			}
			sources[f.TorrentID] = d
		}
		found := false
		for _, actual := range d.Files {
			if d.Hash == f.Hash && actual.ID == f.FileID && actual.Path == f.Path && actual.Bytes == f.Bytes && actual.Video {
				found = true
				break
			}
		}
		if !found {
			return catalog.ErrChanged
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	var phase string
	if err = tx.QueryRow(ctx, `SELECT phase FROM mapping_operations WHERE id=$1 FOR UPDATE`, op).Scan(&phase); err != nil {
		return err
	}
	if phase != "queued" {
		return nil
	}
	metadata, _ := json.Marshal(title)
	var seriesID string
	if kind == "series" {
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES($1,$2,$3) ON CONFLICT(cinemeta_id) DO NOTHING`, id, selected.Category, metadata); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE catalog_series SET metadata=jsonb_set(jsonb_set(metadata,'{episode_count}',to_jsonb($2::int)),'{regular_episode_count}',to_jsonb($3::int)) WHERE cinemeta_id=$1`, id, len(episodes), regularEpisodes); err != nil {
			return err
		}
		var category string
		if err = tx.QueryRow(ctx, `SELECT id::text,category FROM catalog_series WHERE cinemeta_id=$1 FOR UPDATE`, id).Scan(&seriesID, &category); err != nil {
			return err
		}
		if category != selected.Category {
			var mapped bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_seasons se JOIN catalog_episodes e ON e.season_id=se.id JOIN media_mappings m ON m.movie_id=e.media_id WHERE se.series_id=$1)`, seriesID).Scan(&mapped); err != nil {
				return err
			}
			if mapped {
				return catalog.ErrConflict
			}
			if _, err = tx.Exec(ctx, `UPDATE catalog_series SET category=$2 WHERE id=$1`, seriesID, selected.Category); err != nil {
				return err
			}
		}
	}
	sort.Slice(selected.Files, func(i, j int) bool { return selected.Files[i].EpisodeID < selected.Files[j].EpisodeID })
	for _, f := range selected.Files {
		mediaKind := "movie"
		ep := episodes[f.EpisodeID]
		data := metadata
		if kind == "series" {
			mediaKind = "episode"
			data, _ = json.Marshal(map[string]any{"id": ep.ID, "type": "episode", "name": ep.Name, "year": title.Year})
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata,kind) VALUES($1,$2,$3) ON CONFLICT(cinemeta_id) DO NOTHING`, f.EpisodeID, data, mediaKind); err != nil {
			return err
		}
		var mediaID string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1 FOR UPDATE`, f.EpisodeID).Scan(&mediaID); err != nil {
			return err
		}
		if kind == "series" {
			if _, err = tx.Exec(ctx, `INSERT INTO catalog_seasons(series_id,number) VALUES($1,$2) ON CONFLICT DO NOTHING`, seriesID, ep.Season); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO catalog_episodes(media_id,season_id,number) SELECT $1,id,$3 FROM catalog_seasons WHERE series_id=$2 AND number=$4 ON CONFLICT DO NOTHING`, mediaID, seriesID, ep.Number, ep.Season); err != nil {
				return err
			}
		}
		snapshot, _ := json.Marshal(sources[f.TorrentID])
		var mappingID string
		if err = tx.QueryRow(ctx, `INSERT INTO mapping_versions(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,false) RETURNING id::text`, mediaID, account, f.TorrentID, f.Hash, f.FileID, f.Path, f.Bytes, snapshot, who.ID).Scan(&mappingID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mapping_candidates(operation_id,media_id,mapping_id,previous_id) VALUES($1,$2,$3,(SELECT id FROM media_mappings WHERE movie_id=$2))`, op, mediaID, mappingID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE mapping_operations SET phase='validating',validation_started_at=now(),updated_at=now() WHERE id=$1`, op); err != nil {
		return err
	}
	if err = s.queueMappingFiles(ctx, tx, op); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Called with the operation locked after validation. Activation and successor
// insertion are one commit.
func (s *Service) activateMappingTx(ctx context.Context, tx pgx.Tx, id, mode string) error {
	var phase, owned string
	if err := tx.QueryRow(ctx, `SELECT phase,publication_mode FROM mapping_operations WHERE id=$1 FOR UPDATE`, id).Scan(&phase, &owned); err != nil {
		return err
	}
	if owned != mode {
		return ErrInvalid
	}
	if phase == "complete" {
		return nil
	}
	if phase != "publishing" {
		return ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT c.media_id::text,c.mapping_id::text,c.previous_id::text,c.probe FROM mapping_candidates c WHERE operation_id=$1 ORDER BY media_id`, id)
	if err != nil {
		return err
	}
	type candidate struct {
		media, mapping string
		old            *string
		probe          json.RawMessage
	}
	items := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.media, &c.mapping, &c.old, &c.probe); err != nil {
			rows.Close()
			return err
		}
		items = append(items, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return ErrInvalid
	}
	var expected int
	if err = tx.QueryRow(ctx, "SELECT jsonb_array_length(request->'files') FROM mapping_operations WHERE id=$1", id).Scan(&expected); err != nil {
		return err
	}
	if len(items) != expected {
		return ErrInvalid
	}
	for _, c := range items {
		if !json.Valid(c.probe) || string(c.probe) == "null" {
			return ErrInvalid
		}
	}
	for _, c := range items {
		if _, err = tx.Exec(ctx, `SELECT id FROM catalog_media WHERE id=$1 FOR UPDATE`, c.media); err != nil {
			return err
		}
		var matches bool
		if err = tx.QueryRow(ctx, `SELECT (SELECT id FROM media_mappings WHERE movie_id=$1) IS NOT DISTINCT FROM $2::uuid`, c.media, c.old).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return catalog.ErrConflict
		}
		var job *int64
		err = tx.QueryRow(ctx, `SELECT job_id FROM streaming_items WHERE movie_id=$1 FOR UPDATE`, c.media).Scan(&job)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if job != nil {
			if _, err = s.queue.JobCancelTx(ctx, tx, *job); err != nil {
				return err
			}
		}
		var changed bool
		if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM mapping_versions old JOIN mapping_versions next ON next.id=$2 WHERE old.id=$1 AND (old.rd_account_id,old.torrent_id,old.info_hash,old.file_id,old.file_path,old.file_bytes)=(next.rd_account_id,next.torrent_id,next.info_hash,next.file_id,next.file_path,next.file_bytes))`, c.old, c.mapping).Scan(&changed); err != nil {
			return err
		}
		local, e := localState(ctx, tx, c.media)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil && changed {
			if local.JobID > 0 {
				if _, err = s.queue.JobCancelTx(ctx, tx, local.JobID); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `INSERT INTO orphan_files(media_id,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.media, local.Generation); err != nil {
				return err
			}
			// A new generation is chosen by the next Download. Old workers are fenced by
			// job_id and cannot publish, checkpoint, or reactivate this local generation.
			if _, err = tx.Exec(ctx, `UPDATE local_files SET available=false,phase='absent',job_id=0,completed_bytes=0,validated_bytes=0,transfer_bytes_per_second=0,transfer_sampled_at=NULL,error_code='',updated_at=now() WHERE movie_id=$1`, c.media); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM local_chunks WHERE movie_id=$1`, c.media); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE mapping_versions SET active=false WHERE movie_id=$1 AND active`, c.media); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE mapping_versions SET active=true WHERE id=$1`, c.mapping); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,probe,published) VALUES($1,$2,'ready',$3,true) ON CONFLICT(movie_id) DO UPDATE SET mapping_id=$2,phase='ready',probe=$3,published=true,job_id=NULL,error_code='',updated_at=now()`, c.media, c.mapping, c.probe); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE mapping_operations SET phase='complete',error_code='',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if err = s.advanceMappingSubmission(ctx, tx, id); err != nil {
		return err
	}
	return nil
}
