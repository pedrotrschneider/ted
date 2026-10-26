package httpapi

import (
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/titleprefs"
	"entertainmentdistrict/internal/watchstate"
	"net/http"
	"strings"
	"time"
)

// Shared by purple poster eyes and the Watched browse filter. c, wc and $3 are
// respectively the media row, current user state and authenticated TED user ID.
const previouslyViewedSQL = `(COALESCE((wc.state->>'played')::boolean,false) OR COALESCE((wc.state->>'position_ticks')::bigint,0)>0 OR COALESCE((wc.state->>'play_count')::int,0)>0 OR
 EXISTS(SELECT 1 FROM watch_history h WHERE h.user_id=$3 AND h.media_id=c.id AND ((h.state->>'played')::boolean OR (h.state->>'position_ticks')::bigint>0 OR (h.state->>'play_count')::int>0)))`

type posterWatch struct {
	Seen      bool     `json:"seen"`
	Watched   int      `json:"watched"`
	Available int      `json:"available"`
	Progress  *float64 `json:"progress"`
}

type titleStatus struct {
	Watchlist       bool                       `json:"watchlist"`
	UserRating      *int                       `json:"user_rating"`
	UserWatches     []watchstate.UserIndicator `json:"user_watches,omitempty"`
	JointWatches    []jointWatch               `json:"joint_watches,omitempty"`
	PublicationMode string                     `json:"publication_mode"`
	WatchCatalog    bool                       `json:"-"`
	Watch           posterWatch                `json:"watch"`
	ID              string                     `json:"id"`
	State           string                     `json:"state"`
	Added           int                        `json:"added"`
	Total           *int                       `json:"total"`
	Phase           string                     `json:"phase"`
	Validated       int                        `json:"validated"`
}

// Read only public progress, never source names, credentials, or operation errors.
// One snapshot counts each media identity once even while replacing its source.
func (a *API) titleStatuses(w http.ResponseWriter, r *http.Request, u accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	kind := r.URL.Query().Get("kind")
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if (kind != "movie" && kind != "series") || len(ids) > 100 {
		respond(w, 400, map[string]string{"error": "Invalid title status request."})
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !cinemeta.ValidID(id) || seen[id] {
			respond(w, 400, map[string]string{"error": "Invalid title identities."})
			return
		}
		seen[id] = true
	}
	rows, err := a.pool.Query(r.Context(), `WITH titles AS (SELECT unnest($1::text[]) AS id)
 SELECT t.id,CASE WHEN removed THEN '' WHEN phase IN ('queued','validating','publishing') THEN 'pending' WHEN phase='failed' THEN 'failed' WHEN added>0 THEN 'added' ELSE '' END,
 CASE WHEN removed THEN 0 WHEN $2='series' THEN regular_added ELSE added END,
 CASE WHEN phase IN ('queued','validating','publishing') AND NOT removed THEN progress.total WHEN $2='movie' THEN 1 ELSE COALESCE(wcat.regular_episode_count,(sh.metadata->>'regular_episode_count')::int) END,
 CASE WHEN removed THEN '' ELSE COALESCE(phase,'') END, COALESCE(progress.validated,0), viewing.seen,viewing.watched,CASE WHEN $2='series' THEN COALESCE(wcat.regular_episode_count,(sh.metadata->>'regular_episode_count')::int,viewing.available) ELSE viewing.available END,viewing.progress,wcat.title_id IS NOT NULL
 FROM titles t
 LEFT JOIN catalog_series sh ON $2='series' AND sh.cinemeta_id=t.id
 LEFT JOIN watch_title_catalog wcat ON wcat.title_id=t.id AND wcat.kind=$2
 LEFT JOIN LATERAL (SELECT id,phase,submission_id,request FROM mapping_operations WHERE title_id=t.id AND kind=$2 ORDER BY sequence DESC LIMIT 1) op ON true
 LEFT JOIN mapping_submissions sub ON sub.id=op.submission_id
 CROSS JOIN LATERAL (SELECT
 jsonb_array_length(COALESCE(sub.request,op.request)->'files') AS total,
 COALESCE((SELECT sum(jsonb_array_length(prev.request->'files')) FROM mapping_operations prev WHERE prev.submission_id=op.submission_id AND prev.phase='complete' AND prev.id!=op.id),0)
 +(SELECT count(*) FROM mapping_candidates c WHERE c.operation_id=op.id AND c.probe IS NOT NULL) AS validated) progress
 CROSS JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM library_removals WHERE title_id=t.id AND kind=$2 AND NOT retired) AS removed) rem
 CROSS JOIN LATERAL (SELECT count(*)::int AS added, count(*) FILTER(WHERE c.kind='episode' AND ted_episode_season(c.cinemeta_id)>0)::int AS regular_added FROM catalog_media c
 WHERE (($2='movie' AND c.kind='movie' AND c.cinemeta_id=t.id) OR ($2='series' AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=t.id))
 AND EXISTS(SELECT 1 FROM streaming_items s JOIN media_mappings m ON m.id=s.mapping_id WHERE s.movie_id=c.id AND s.published)) counts
 CROSS JOIN LATERAL (
 SELECT COALESCE(bool_or(ever),false) AS seen,
 count(*) FILTER(WHERE COALESCE((state->>'played')::boolean,false))::int AS watched,
 count(*) FILTER(WHERE $2='series' OR available)::int AS available,
 max(CASE WHEN $2!='movie' OR COALESCE((state->>'played')::boolean,false) THEN NULL
 WHEN COALESCE((state->>'position_ticks')::numeric,0)>0 THEN
 CASE WHEN duration>0 THEN LEAST(100::numeric,(state->>'position_ticks')::numeric/10000000/duration*100) ELSE NULL END
 ELSE NULL END)::float8 AS progress
 FROM (
 SELECT wc.state,
 `+previouslyViewedSQL+` AS ever,
 NOT removed AND COALESCE(si.published,false) AS available,
 CASE WHEN si.probe->'format'->>'duration' ~ '^[0-9]+([.][0-9]+)?$' THEN (si.probe->'format'->>'duration')::numeric ELSE NULL END AS duration
 FROM catalog_media c LEFT JOIN watch_current wc ON wc.media_id=c.id AND wc.user_id=$3 LEFT JOIN streaming_items si ON si.movie_id=c.id
 WHERE (($2='movie' AND c.kind='movie' AND c.cinemeta_id=t.id) OR ($2='series' AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=t.id AND ted_episode_season(c.cinemeta_id)>0))
 AND (wcat.entries IS NULL OR wcat.entries ? c.cinemeta_id)
 ) observations) viewing`, ids, kind, u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	result := []titleStatus{}
	for rows.Next() {
		var s titleStatus
		s.PublicationMode = "native"
		if a.Streaming != nil {
			s.PublicationMode = a.Streaming.PublicationMode()
		}
		if err = rows.Scan(&s.ID, &s.State, &s.Added, &s.Total, &s.Phase, &s.Validated, &s.Watch.Seen, &s.Watch.Watched, &s.Watch.Available, &s.Watch.Progress, &s.WatchCatalog); err != nil {
			break
		}
		if kind == "movie" {
			s.Watch.Available = 1
		}
		result = append(result, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Existing libraries predate the count snapshot. Populate it once from the
	// catalog, not from mapped rows (which omit precisely the missing episodes).
	for i := range result {
		s := &result[i]
		if kind == "series" && ((!s.WatchCatalog && s.Watch.Seen) || s.Total == nil && s.State != "" && s.State != "pending") {
			started := time.Now()
			show, e := a.series.Series(r.Context(), s.ID)
			if e != nil {
				if s.Watch.Seen {
					a.catalogFailure(w, r, e)
					return
				}
				continue
			}
			if e = a.rememberWatchSeries(r.Context(), identity(u), show, started); e != nil {
				a.fail(w, r, e)
				return
			}
			n := cinemeta.RegularEpisodeCount(show.Episodes)
			if _, e = a.pool.Exec(r.Context(), `UPDATE catalog_series SET metadata=jsonb_set(metadata,'{regular_episode_count}',to_jsonb($2::int)) WHERE cinemeta_id=$1`, s.ID, n); e != nil {
				a.fail(w, r, e)
				return
			}
			if s.State != "pending" {
				s.Total = &n
			}
			s.Watch.Available = n
			regular := []string{}
			for _, episode := range show.Episodes {
				if episode.Season > 0 {
					regular = append(regular, episode.ID)
				}
			}
			e = a.pool.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE COALESCE((wc.state->>'played')::boolean,false)),COALESCE(bool_or(`+strings.ReplaceAll(previouslyViewedSQL, "$3", "$2")+`),false) FROM catalog_media c LEFT JOIN watch_current wc ON wc.media_id=c.id AND wc.user_id=$2 WHERE c.cinemeta_id=ANY($1::text[])`, regular, u.ID).Scan(&s.Watch.Watched, &s.Watch.Seen)
			if e != nil {
				a.fail(w, r, e)
				return
			}

		}
	}
	if kind == "series" {
		counts, e := a.catalog.ReleasedCounts(r.Context(), ids, u.ID, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		for i := range result {
			c := counts[result[i].ID]
			result[i].Watch.Watched = c.Watched
			result[i].Watch.Available = c.Available
			if result[i].State != "pending" {
				if c.Known {
					total := c.Available
					result[i].Total = &total
				} else {
					result[i].Total = nil
				}
				if result[i].State != "" {
					result[i].Added = c.Added
				}
			}
		}
	}
	media := make([]catalog.MediaIdentity, len(result))
	for i := range result {
		media[i] = catalog.MediaIdentity{Kind: kind, ID: result[i].ID}
	}
	prefs, err := titleprefs.New(a.pool).States(r.Context(), u.ID, media)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	joint, err := a.jointWatches(r.Context(), u.ID, media)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	other, err := a.watch.OtherIndicators(r.Context(), u.ID, media, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for i := range result {
		state := prefs[kind+"/"+result[i].ID]
		result[i].Watchlist, result[i].UserRating = state.Watchlist, state.Rating
		result[i].UserWatches = other[kind+"/"+result[i].ID]
		result[i].JointWatches = joint[kind+"/"+result[i].ID]
	}
	if a.recheck(w, r, u, false) {
		respond(w, 200, map[string]any{"statuses": result})
	}
}
