package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
)

type scopeReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func castScope(ctx context.Context, q scopeReader, r Request) ([]cinemeta.Episode, string, error) {
	video := r.VideoID
	if video == "" {
		video = fmt.Sprintf("%s:%d:%d", r.TitleID, r.Season, r.Number)
	}
	rows, err := q.Query(ctx, `SELECT ep FROM catalog_episode_schedules s,jsonb_array_elements(s.episodes) ep WHERE s.title_id=$1 AND (($3='season' AND (ep->>'season')::int=$2) OR ($3='episode' AND ep->>'id'=$4)) ORDER BY (ep->>'season')::int,(ep->>'number')::int,ep->>'id'`, r.TitleID, r.Season, r.Kind, video)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	eps := []cinemeta.Episode{}
	today := time.Now().UTC().Format(time.DateOnly)
	type signature struct {
		ID         string
		TVDB, TMDB int64
		Released   bool
	}
	parts := []signature{}
	for rows.Next() {
		var ep cinemeta.Episode
		if err = rows.Scan(&ep); err != nil {
			return nil, "", err
		}
		eps = append(eps, ep)
		parts = append(parts, signature{ep.ID, ep.TVDBID, ep.TMDBID, ep.ReleaseDate != "" && ep.ReleaseDate <= today})
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return eps, hex.EncodeToString(sum[:]), nil
}
