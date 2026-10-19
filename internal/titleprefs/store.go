// Package titleprefs owns account-local whole-title watch lists and ratings.
// Viewing never writes this table; membership changes only on explicit action.
package titleprefs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalid = errors.New("invalid title preference")

type State struct {
	Watchlist bool `json:"watchlist"`
	Rating    *int `json:"user_rating"`
}
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool} }
func Valid(m catalog.MediaIdentity) bool {
	return (m.Kind == "movie" || m.Kind == "series") && cinemeta.ValidID(m.ID)
}

// Set runs inside the caller's authorized transaction. Independent field writes
// use distinct upserts, so a simultaneous save and rating cannot lose each other.
func Set(ctx context.Context, tx pgx.Tx, user int64, title cinemeta.Movie, field string, saved bool, rating *int) (State, error) {
	var out State
	if !Valid(catalog.MediaIdentity{Kind: title.Type, ID: title.ID}) || title.Name == "" || (rating != nil && (*rating < 1 || *rating > 7)) {
		return out, ErrInvalid
	}
	raw, err := json.Marshal(title)
	if err != nil {
		return out, err
	}
	var sql string
	var value any
	switch field {
	case "watchlist":
		sql = `INSERT INTO account_title_preferences(user_id,kind,title_id,metadata,watchlist) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(user_id,kind,title_id) DO UPDATE SET metadata=EXCLUDED.metadata,watchlist=EXCLUDED.watchlist,
 saved_at=CASE WHEN EXCLUDED.watchlist AND NOT account_title_preferences.watchlist THEN now() ELSE account_title_preferences.saved_at END
 RETURNING watchlist,rating`
		value = saved
	case "rating":
		sql = `INSERT INTO account_title_preferences(user_id,kind,title_id,metadata,rating) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(user_id,kind,title_id) DO UPDATE SET metadata=EXCLUDED.metadata,rating=EXCLUDED.rating,rated_at=now()
 RETURNING watchlist,rating`
		value = rating
	default:
		return out, ErrInvalid
	}
	err = tx.QueryRow(ctx, sql, user, title.Type, title.ID, raw, value).Scan(&out.Watchlist, &out.Rating)
	return out, err
}

func (s *Store) States(ctx context.Context, user int64, media []catalog.MediaIdentity) (map[string]State, error) {
	out := map[string]State{}
	kinds, ids := []string{}, []string{}
	for _, m := range media {
		if Valid(m) {
			kinds = append(kinds, m.Kind)
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT p.kind,p.title_id,p.watchlist,p.rating FROM account_title_preferences p JOIN unnest($2::text[],$3::text[]) AS t(kind,id) ON p.kind=t.kind AND p.title_id=t.id WHERE p.user_id=$1`, user, kinds, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var state State
		if err = rows.Scan(&kind, &id, &state.Watchlist, &state.Rating); err != nil {
			return nil, err
		}
		out[kind+"/"+id] = state
	}
	return out, rows.Err()
}

type cursor struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	ID   string    `json:"id"`
	List string    `json:"list"`
}
type Page struct {
	Titles []cinemeta.Movie
	Next   string
}

// List includes retained started viewing in Unrated, including specials and
// historical views after unwatch. No joint projection participates in this list.
func (s *Store) List(ctx context.Context, user int64, list, kind, after string, random bool) (Page, error) {
	out := Page{Titles: []cinemeta.Movie{}}
	if (kind != "" && kind != "movie" && kind != "series") || (list != "watchlist" && list != "0" && list != "1" && list != "2" && list != "3" && list != "4" && list != "5" && list != "6" && list != "7") || len(after) > 512 || random && (list != "watchlist" || after != "") {
		return out, ErrInvalid
	}
	c := cursor{At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), Kind: "zz", ID: "zz"}
	if after != "" {
		raw, e := base64.RawURLEncoding.DecodeString(after)
		if e != nil || json.Unmarshal(raw, &c) != nil || !Valid(catalog.MediaIdentity{Kind: c.Kind, ID: c.ID}) || c.List != list+"/"+kind || c.At.IsZero() {
			return out, ErrInvalid
		}
	}
	ordering := "at DESC,kind DESC,id DESC"
	if random {
		ordering = "random()"
	}
	rows, err := s.pool.Query(ctx, `WITH observations AS (
 SELECT c.kind,c.cinemeta_id,w.state,w.observed_at FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=$1 AND $2='0'
 UNION ALL SELECT c.kind,c.cinemeta_id,h.state,h.observed_at FROM watch_history h JOIN catalog_media c ON c.id=h.media_id WHERE h.user_id=$1 AND $2='0'
 ), viewed AS (
 SELECT CASE WHEN kind='episode' THEN 'series' ELSE kind END AS kind,split_part(cinemeta_id,':',1) AS id,
 max(COALESCE((state->>'last_played')::timestamptz,observed_at)) AS at FROM observations
 WHERE COALESCE((state->>'played')::bool,false) OR COALESCE((state->>'position_ticks')::bigint,0)>0 OR COALESCE((state->>'play_count')::int,0)>0 GROUP BY 1,2
 ), selected AS (
 SELECT kind,title_id AS id,CASE WHEN $2='watchlist' THEN saved_at ELSE rated_at END AS at,metadata
 FROM account_title_preferences WHERE user_id=$1 AND (($2='watchlist' AND watchlist) OR ($2 NOT IN ('watchlist','0') AND rating::text=$2))
 UNION ALL SELECT v.kind,v.id,v.at,NULL::jsonb FROM viewed v WHERE $2='0' AND NOT EXISTS(
 SELECT 1 FROM account_title_preferences p WHERE p.user_id=$1 AND p.kind=v.kind AND p.title_id=v.id AND p.rating IS NOT NULL)
 ), candidates AS (
 SELECT s.kind,s.id,s.at,COALESCE(n.metadata,s.metadata,cs.metadata,cm.metadata,'{}'::jsonb) AS metadata FROM selected s
 LEFT JOIN native_catalog_titles n ON n.kind=s.kind AND n.title_id=s.id
 LEFT JOIN catalog_series cs ON s.kind='series' AND cs.cinemeta_id=s.id
 LEFT JOIN LATERAL (SELECT CASE WHEN c.kind='episode' THEN c.metadata->'series' ELSE c.metadata END AS metadata FROM catalog_media c WHERE
 (s.kind='movie' AND c.kind='movie' AND c.cinemeta_id=s.id) OR (s.kind='series' AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=s.id) ORDER BY c.cinemeta_id LIMIT 1) cm ON true
 WHERE ($3='' OR s.kind=$3) AND (s.at,s.kind,s.id)<($4,$5,$6)
 ) SELECT kind,id,at,metadata FROM candidates ORDER BY `+ordering+` LIMIT 51`, user, list, kind, c.At, c.Kind, c.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last cursor
	for rows.Next() {
		var title cinemeta.Movie
		var k, id string
		var at time.Time
		if err = rows.Scan(&k, &id, &at, &title); err != nil {
			return out, err
		}
		if len(out.Titles) == 50 {
			if !random {
				raw, _ := json.Marshal(last)
				out.Next = base64.RawURLEncoding.EncodeToString(raw)
			}
			break
		}
		title.Type, title.ID = k, id
		if title.Name == "" {
			title.Name = id
		}
		if title.Genres == nil {
			title.Genres = []string{}
		}
		out.Titles = append(out.Titles, title)
		last = cursor{at, k, id, list + "/" + kind}
	}
	return out, rows.Err()
}
