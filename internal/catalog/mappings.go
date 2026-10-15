// Package catalog owns explicit mappings and retained TED catalog metadata.
// Metadata enrichment never creates mappings or library entries.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrChanged  = errors.New("selected source changed; inspect it again")
	ErrConflict = errors.New("movie already has a different mapping")
	ErrInvalid  = errors.New("invalid movie mapping")
)

type Movies interface {
	Movie(context.Context, string) (cinemeta.Movie, error)
}
type Sources interface {
	AccountID(context.Context) (string, error)
	Torrent(context.Context, string) (realdebrid.Detail, error)
}
type Selection struct {
	TorrentID string `json:"torrent_id"`
	Hash      string `json:"hash"`
	FileID    int64  `json:"file_id"`
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
}
type Mapping struct {
	ID        string         `json:"id"`
	MovieID   string         `json:"movie_id"`
	Movie     cinemeta.Movie `json:"movie"`
	AccountID string         `json:"rd_account_id"`
	Selection
	SourceName string    `json:"source_name"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `m.id::text,c.id::text,c.metadata,m.rd_account_id,m.torrent_id,m.info_hash,m.file_id,m.file_path,m.file_bytes,m.source_snapshot->>'name',m.created_at`

func scan(row pgx.Row) (Mapping, error) {
	var m Mapping
	var metadata []byte
	err := row.Scan(&m.ID, &m.MovieID, &metadata, &m.AccountID, &m.TorrentID, &m.Hash, &m.FileID, &m.Path, &m.Bytes, &m.SourceName, &m.CreatedAt)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(metadata, &m.Movie); err != nil {
		return m, err
	}
	m.Status = "draft"
	return m, nil
}
func (s *Store) Get(ctx context.Context, id string) (*Mapping, error) {
	m, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.cinemeta_id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

type Page struct {
	Mappings  []Mapping `json:"mappings"`
	NextAfter string    `json:"next_after,omitempty"`
}

func (s *Store) List(ctx context.Context, after string) (Page, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.kind='movie' AND c.cinemeta_id>$1 ORDER BY c.cinemeta_id LIMIT 101`, after)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Mappings: []Mapping{}}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return Page{}, err
		}
		page.Mappings = append(page.Mappings, m)
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Mappings) > 100 {
		page.Mappings = page.Mappings[:100]
		page.NextAfter = page.Mappings[99].Movie.ID
	}
	return page, nil
}

// Save rereads the selected file and catalog on the backend. Browser metadata is
// only an optimistic identity check, never authoritative source provenance.
func (s *Store) Save(ctx context.Context, who accounts.Identity, id string, selected Selection, movies Movies, rd Sources) (Mapping, error) {
	if stored, ok := rd.(interface {
		Snapshot(context.Context) (*realdebrid.Client, error)
	}); ok {
		snapshot, err := stored.Snapshot(ctx)
		if err != nil {
			return Mapping{}, err
		}
		rd = snapshot
	}
	if !cinemeta.ValidID(id) || selected.TorrentID == "" || len(selected.Hash) != 40 || selected.FileID <= 0 || selected.Bytes <= 0 || selected.Path == "" {
		return Mapping{}, ErrInvalid
	}
	movie, err := movies.Movie(ctx, id)
	if err != nil {
		return Mapping{}, err
	}
	account, err := rd.AccountID(ctx)
	if err != nil {
		return Mapping{}, err
	}
	source, err := rd.Torrent(ctx, selected.TorrentID)
	if err != nil {
		return Mapping{}, err
	}
	matched := false
	for _, file := range source.Files {
		if file.ID == selected.FileID && file.Path == selected.Path && file.Bytes == selected.Bytes && file.Video && source.Hash == selected.Hash {
			matched = true
			break
		}
	}
	if !matched {
		return Mapping{}, ErrChanged
	}
	metadata, err := json.Marshal(movie)
	if err != nil {
		return Mapping{}, err
	}
	snapshot, err := json.Marshal(source)
	if err != nil {
		return Mapping{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Mapping{}, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return Mapping{}, err
	}
	// The unique catalog key serializes concurrent first saves, across processes.
	// Existing metadata and IDs remain intact; this operation cannot replace a source.
	_, err = tx.Exec(ctx, `INSERT INTO catalog_media(cinemeta_id,metadata) VALUES($1,$2) ON CONFLICT(cinemeta_id) DO NOTHING`, id, metadata)
	if err != nil {
		return Mapping{}, err
	}
	var movieID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM catalog_media WHERE cinemeta_id=$1 FOR UPDATE`, id).Scan(&movieID); err != nil {
		return Mapping{}, err
	}
	old, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.id=$1`, movieID))
	if err == nil {
		if old.AccountID != account || old.Selection != selected {
			return Mapping{}, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Mapping{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_mappings(movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, movieID, account, selected.TorrentID, source.Hash, selected.FileID, selected.Path, selected.Bytes, snapshot, who.ID)
	if err != nil {
		return Mapping{}, err
	}
	m, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM media_mappings m JOIN catalog_media c ON c.id=m.movie_id WHERE c.id=$1`, movieID))
	if err != nil {
		return Mapping{}, err
	}
	return m, tx.Commit(ctx)
}
