package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"github.com/jackc/pgx/v5"
)

type SeriesReader interface {
	Series(context.Context, string) (cinemeta.Series, error)
}
type EpisodeSelection struct {
	EpisodeID string `json:"episode_id"`
	Selection
}
type SeriesSelection struct {
	Category string             `json:"category"`
	Files    []EpisodeSelection `json:"files"`
}
type EpisodeMapping struct {
	SourceName string `json:"source_name"`
	EpisodeID  string `json:"episode_id"`
	MediaID    string `json:"media_id"`
	Selection
}
type SeriesMappings struct {
	Category string           `json:"category"`
	Mappings []EpisodeMapping `json:"mappings"`
}

func (s *Store) EpisodeMappings(ctx context.Context, id string) (SeriesMappings, error) {
	result := SeriesMappings{Mappings: []EpisodeMapping{}}
	err := s.pool.QueryRow(ctx, `SELECT category FROM catalog_series WHERE cinemeta_id=$1`, id).Scan(&result.Category)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	rows, err := s.pool.Query(ctx, `SELECT c.cinemeta_id,c.id::text,m.torrent_id,m.info_hash,m.file_id,m.file_path,m.file_bytes,m.source_snapshot->>'name' FROM catalog_series s JOIN catalog_seasons se ON se.series_id=s.id JOIN catalog_episodes e ON e.season_id=se.id JOIN catalog_media c ON c.id=e.media_id JOIN media_mappings m ON m.movie_id=c.id WHERE s.cinemeta_id=$1 ORDER BY se.number,e.number`, id)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var m EpisodeMapping
		if err = rows.Scan(&m.EpisodeID, &m.MediaID, &m.TorrentID, &m.Hash, &m.FileID, &m.Path, &m.Bytes, &m.SourceName); err != nil {
			return result, err
		}
		result.Mappings = append(result.Mappings, m)
	}
	if len(result.Mappings) == 0 {
		result.Category = ""
	}
	return result, rows.Err()
}

// SaveEpisodes commits one reviewed source batch atomically. Existing assignments
// are never replaced implicitly, including when a different release is selected.
func (s *Store) SaveEpisodes(ctx context.Context, who accounts.Identity, id string, selected SeriesSelection, reader SeriesReader, rd Sources) error {
	if stored, ok := rd.(interface {
		Snapshot(context.Context) (*realdebrid.Client, error)
	}); ok {
		snapshot, err := stored.Snapshot(ctx)
		if err != nil {
			return err
		}
		rd = snapshot
	}
	if !cinemeta.ValidID(id) || (selected.Category != "shows" && selected.Category != "anime") || len(selected.Files) == 0 || len(selected.Files) > 500 {
		return ErrInvalid
	}
	show, err := reader.Series(ctx, id)
	if err != nil {
		return err
	}
	episodes := map[string]cinemeta.Episode{}
	for _, e := range show.Episodes {
		episodes[e.ID] = e
	}
	files := append([]EpisodeSelection(nil), selected.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].EpisodeID < files[j].EpisodeID })
	first := files[0]
	seenEpisodes := map[string]bool{}
	for _, f := range files {
		if _, ok := episodes[f.EpisodeID]; !ok || seenEpisodes[f.EpisodeID] || f.TorrentID != first.TorrentID || f.Hash != first.Hash || f.FileID <= 0 || f.Bytes <= 0 {
			return ErrInvalid
		}
		seenEpisodes[f.EpisodeID] = true
	}
	account, err := rd.AccountID(ctx)
	if err != nil {
		return err
	}
	source, err := rd.Torrent(ctx, first.TorrentID)
	if err != nil {
		return err
	}
	if source.Hash != first.Hash {
		return ErrChanged
	}
	for _, f := range files {
		matched := false
		for _, actual := range source.Files {
			if actual.ID == f.FileID && actual.Path == f.Path && actual.Bytes == f.Bytes && actual.Video {
				matched = true
				break
			}
		}
		if !matched {
			return ErrChanged
		}
	}
	metadata, err := json.Marshal(show.Movie)
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(source)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_series(cinemeta_id,category,metadata) VALUES($1,$2,$3) ON CONFLICT(cinemeta_id) DO NOTHING`, id, selected.Category, metadata); err != nil {
		return err
	}
	var seriesID, category string
	if err = tx.QueryRow(ctx, `SELECT id::text,category FROM catalog_series WHERE cinemeta_id=$1 FOR UPDATE`, id).Scan(&seriesID, &category); err != nil {
		return err
	}
	if category != selected.Category {
		return ErrConflict
	}
	for _, f := range files {
		ep := episodes[f.EpisodeID]
		epMetadata, _ := json.Marshal(map[string]any{"id": ep.ID, "type": "episode", "name": ep.Name, "year": show.Year})
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata,kind) VALUES($1,$2,'episode') ON CONFLICT(cinemeta_id) DO NOTHING`, ep.ID, epMetadata); err != nil {
			return err
		}
		var mediaID string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1 FOR UPDATE`, ep.ID).Scan(&mediaID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_seasons(series_id,number) VALUES($1,$2) ON CONFLICT DO NOTHING`, seriesID, ep.Season); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_episodes(media_id,season_id,number) SELECT $1,id,$3 FROM catalog_seasons WHERE series_id=$2 AND number=$4 ON CONFLICT(media_id) DO NOTHING`, mediaID, seriesID, ep.Number, ep.Season); err != nil {
			return err
		}
		var old Selection
		var oldAccount string
		err = tx.QueryRow(ctx, `SELECT rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes FROM media_mappings WHERE movie_id=$1`, mediaID).Scan(&oldAccount, &old.TorrentID, &old.Hash, &old.FileID, &old.Path, &old.Bytes)
		if err == nil {
			if oldAccount != account || old != f.Selection {
				return ErrConflict
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, mediaID, account, f.TorrentID, f.Hash, f.FileID, f.Path, f.Bytes, snapshot, who.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
