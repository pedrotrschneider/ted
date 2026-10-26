package httpapi

import (
	"net/http"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/watchstate"
)

// Personal filters operate on the whole retained collection, not one page of
// discovery results. Native/web Watched share the same viewing-history rule.
func (a *API) nativeFilteredCatalog(w http.ResponseWriter, r *http.Request, grant accounts.NativeGrant) bool {
	q := r.URL.Query()
	mapped, watched := q.Get("mapped"), q.Get("watched")
	if mapped != "" && mapped != "true" && mapped != "false" ||
		watched != "" && watched != "all" && watched != "watched" && watched != "fully" && watched != "partial" {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return true
	}
	personal := mapped == "true" || watched != "" && watched != "all"
	if !personal {
		if q.Get("after") != "" {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return true
		}
		return false
	}
	kind, after, genre := r.PathValue("kind"), q.Get("after"), q.Get("genre")
	if kind != "movie" && kind != "series" || after != "" && !cinemeta.ValidID(after) ||
		!cinemeta.ValidGenre(genre) || q.Get("search") != "" || q.Get("year") != "" ||
		q.Get("skip") != "" && q.Get("skip") != "0" || q.Get("catalog") != "" && q.Get("catalog") != "popular" && q.Get("catalog") != "new" {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return true
	}
	viewed := strings.ReplaceAll(previouslyViewedSQL, "wc.", "w.")
	rows, err := a.pool.Query(r.Context(), `WITH media AS (
 SELECT c.cinemeta_id,EXISTS(SELECT 1 FROM media_mappings m WHERE m.movie_id=c.id) AS mapped,
 ($1!='series' OR ted_episode_season(c.cinemeta_id)>0) AND (`+viewed+` OR COALESCE((`+watchstate.NativeUnfinishedWatchSQL+`),false)) AS viewed,
 COALESCE((w.state->>'played')::boolean,false) AS played,
 COALESCE((`+watchstate.NativeUnfinishedWatchSQL+`),false) AS unfinished
 FROM catalog_media c LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=$3
 WHERE ($1='movie' AND c.kind='movie') OR ($1='series' AND c.kind='episode')
 ), flags AS (
 SELECT split_part(cinemeta_id,':',1) AS id,bool_or(mapped) AS mapped,bool_or(viewed) AS viewed,
 bool_or(played) AS played,bool_or(unfinished) AS unfinished FROM media GROUP BY 1
 ), summaries AS (
 SELECT title_id AS id,metadata,0 AS priority FROM native_catalog_titles WHERE kind=$1
 UNION ALL SELECT cinemeta_id,metadata,1 FROM catalog_media WHERE $1='movie' AND kind='movie'
 UNION ALL SELECT cinemeta_id,metadata,1 FROM catalog_series WHERE $1='series'
 UNION ALL SELECT split_part(cinemeta_id,':',1),metadata->'series',2 FROM catalog_media WHERE $1='series' AND kind='episode' AND metadata ? 'series'
 ), titles AS (SELECT DISTINCT ON(id) id,metadata FROM summaries ORDER BY id,priority),
 counts AS (
 SELECT sh.title_id,count(e.id)::int AS total,count(e.id) FILTER(WHERE COALESCE((cw.state->>'played')::bool,false))::int AS watched
 FROM catalog_episode_schedules sh
 LEFT JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(id text,season int,release_date date) ON e.season>0 AND e.release_date<=$7::date
 LEFT JOIN catalog_media c ON c.kind='episode' AND c.cinemeta_id=e.id
 LEFT JOIN watch_current cw ON cw.media_id=c.id AND cw.user_id=$3
 WHERE $1='series' GROUP BY sh.title_id
 )
 SELECT t.id,t.metadata FROM titles t JOIN flags f ON f.id=t.id LEFT JOIN counts n ON n.title_id=t.id
 WHERE ($2='' OR (lower(t.metadata->>'name'),t.id)>(SELECT lower(metadata->>'name'),id FROM titles WHERE id=$2))
 AND (NOT $4 OR f.mapped) AND ($6='' OR COALESCE(t.metadata->'genres','[]'::jsonb) ? $6)
 AND ( $5 IN ('','all') OR $5='watched' AND f.viewed
 OR $5='fully' AND CASE WHEN $1='movie' THEN f.played ELSE n.total>0 AND n.watched=n.total END
 OR $5='partial' AND CASE WHEN $1='movie' THEN f.unfinished ELSE f.viewed AND n.watched<n.total END )
 ORDER BY lower(t.metadata->>'name'),t.id LIMIT 51`, kind, after, grant.User.ID, mapped == "true", watched, genre, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
	if err != nil {
		a.nativeFail(w, r, err)
		return true
	}
	page := nativeCatalogPage{Titles: []nativeTitleDetail{}}
	for rows.Next() {
		var title cinemeta.Movie
		var id string
		if err = rows.Scan(&id, &title); err != nil {
			break
		}
		// Retained playback/import metadata can be minimal. TED's catalogue
		// key and kind own identity, independently of descriptive JSON fields.
		title.ID, title.Type = id, kind
		if title.Name == "" {
			title.Name = id
		}
		page.Titles = append(page.Titles, nativeDetail(title, "retained"))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		a.nativeFail(w, r, err)
		return true
	}
	if len(page.Titles) > 50 {
		page.Titles = page.Titles[:50]
		page.NextAfter = page.Titles[49].Media.ID
	}
	if err = a.decorateNative(r.Context(), grant.User.ID, page.Titles); err != nil {
		a.nativeFail(w, r, err)
		return true
	}
	a.respondNativeCatalog(w, r, page)
	return true
}
