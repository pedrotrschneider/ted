package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/cinemeta"
)

type libraryWatchAccount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Both the displayed choices and the actual history query use this allowlist.
// Supplying another user's ID never grants access to their viewing history.
const libraryWatchAccountsSQL = `SELECT id,display_name AS name,'#FFFFFF'::text AS color FROM users WHERE id=$3
 UNION ALL SELECT u.id,u.display_name,j.color FROM joint_account_members j JOIN users u ON u.id=j.joint_id
 WHERE j.member_id=$3 AND NOT u.disabled`

// libraryTitles filters the full persisted collection before pagination, so
// Watched includes retained viewing history outside the current discovery page.
func (a *API) libraryTitles(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	kind, after, search, genre := q.Get("kind"), q.Get("after"), strings.TrimSpace(q.Get("search")), q.Get("genre")
	saved, watched := q.Get("saved") == "true", q.Get("watched") == "true"
	if (kind != "movie" && kind != "series") || (!saved && !watched) || (after != "" && !cinemeta.ValidID(after)) || len(search) > 200 || !cinemeta.ValidGenre(genre) || (q.Get("saved") != "" && q.Get("saved") != "false" && !saved) || (q.Get("watched") != "" && q.Get("watched") != "false" && !watched) {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	// Omitted means all current choices; explicit empty means no accounts.
	var selected []int64
	if values, ok := q["watch_accounts"]; ok {
		if len(values) != 1 || !watched || len(values[0]) > 4096 {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return
		}
		selected = []int64{}
		if values[0] != "" {
			for _, raw := range strings.Split(values[0], ",") {
				id, err := strconv.ParseInt(raw, 10, 64)
				if err != nil || id <= 0 {
					a.catalogFailure(w, r, cinemeta.ErrInvalid)
					return
				}
				selected = append(selected, id)
			}
		}
	}
	people, err := a.pool.Query(r.Context(), `SELECT * FROM (`+strings.ReplaceAll(libraryWatchAccountsSQL, "$3", "$1")+`) choices ORDER BY (id=$1) DESC,name,id`, u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	choices := []libraryWatchAccount{}
	for people.Next() {
		var choice libraryWatchAccount
		if err = people.Scan(&choice.ID, &choice.Name, &choice.Color); err != nil {
			break
		}
		choices = append(choices, choice)
	}
	if err == nil {
		err = people.Err()
	}
	people.Close()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	viewed := strings.ReplaceAll(previouslyViewedSQL, "$3", "viewer.id")
	rows, err := a.pool.Query(r.Context(), `WITH allowed AS (`+libraryWatchAccountsSQL+`), selected AS (
 SELECT id FROM allowed WHERE $8::bigint[] IS NULL OR id=ANY($8)
 ), media AS (
 SELECT c.cinemeta_id,EXISTS(SELECT 1 FROM media_mappings m WHERE m.movie_id=c.id) AS saved,($1!='series' OR ted_episode_season(c.cinemeta_id)>0) AND
 EXISTS(SELECT 1 FROM selected viewer LEFT JOIN watch_current wc ON wc.user_id=viewer.id AND wc.media_id=c.id WHERE `+viewed+`) AS viewed
 FROM catalog_media c
 WHERE ($1='movie' AND c.kind='movie') OR ($1='series' AND c.kind='episode')
 ), flags AS (SELECT split_part(cinemeta_id,':',1) AS id,bool_or(saved) AS saved,bool_or(viewed) AS viewed FROM media GROUP BY 1),
 titles AS (SELECT cinemeta_id AS id,metadata FROM catalog_media WHERE $1='movie' AND kind='movie'
 UNION ALL SELECT cinemeta_id,metadata FROM catalog_series WHERE $1='series'
 UNION ALL SELECT DISTINCT ON (split_part(cinemeta_id,':',1)) split_part(cinemeta_id,':',1),metadata->'series' FROM catalog_media c
 WHERE $1='series' AND kind='episode' AND metadata ? 'series' AND NOT EXISTS(SELECT 1 FROM catalog_series s WHERE s.cinemeta_id=split_part(c.cinemeta_id,':',1)))
 SELECT t.id,t.metadata FROM titles t JOIN flags f ON f.id=t.id WHERE ($2='' OR (lower(t.metadata->>'name'),t.id) > (SELECT lower(metadata->>'name'),id FROM titles WHERE id=$2)) AND (NOT $4 OR f.saved) AND (NOT $5 OR f.viewed)
 AND ($6='' OR strpos(lower(t.metadata->>'name'),lower($6))>0) AND ($7='' OR COALESCE(t.metadata->'genres','[]'::jsonb) ? $7)
 ORDER BY lower(t.metadata->>'name'),t.id LIMIT 101`, kind, after, u.ID, saved, watched, search, genre, selected)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	page := struct {
		Titles        []cinemeta.Movie      `json:"titles"`
		Next          string                `json:"next_after,omitempty"`
		WatchAccounts []libraryWatchAccount `json:"watch_accounts"`
	}{Titles: []cinemeta.Movie{}, WatchAccounts: choices}
	for rows.Next() {
		var id string
		var title cinemeta.Movie
		if err = rows.Scan(&id, &title); err != nil {
			a.fail(w, r, err)
			return
		}
		page.Titles = append(page.Titles, persistedCatalogTitle(id, kind, title))
	}
	if err = rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	if len(page.Titles) > 100 {
		page.Titles = page.Titles[:100]
		page.Next = page.Titles[99].ID
	}
	if a.recheck(w, r, u, false) {
		if err = a.webArtwork(r.Context(), page.Titles); err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, page)
	}
}

// Imported/recovered descriptions need not contain a catalog identity. The
// persisted catalog columns own that identity; absent genres are optional.
func persistedCatalogTitle(id, kind string, title cinemeta.Movie) cinemeta.Movie {
	title.ID, title.Type = id, kind
	if title.Genres == nil {
		title.Genres = []string{}
	}
	return title
}
