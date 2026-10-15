package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/watchstate"
	"net/http"
	"strconv"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
)

type SeriesReader interface {
	BrowseSeries(context.Context, string, int, cinemeta.Filters) (cinemeta.SeriesPage, error)
	Series(context.Context, string) (cinemeta.Series, error)
}

func (a *API) seriesBrowse(w http.ResponseWriter, r *http.Request, u accounts.User) {
	skip := 0
	if raw := r.URL.Query().Get("skip"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return
		}
		skip = n
	}
	filters, err := catalogFilters(r)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	p, err := a.series.BrowseSeries(r.Context(), r.URL.Query().Get("search"), skip, filters)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if a.recheck(w, r, u, false) {
		if err = a.webArtwork(r.Context(), p.Series); err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, p)
	}
}
func (a *API) seriesDetail(w http.ResponseWriter, r *http.Request, u accounts.User) {
	started := time.Now()
	s, err := a.series.Series(r.Context(), r.PathValue("id"))
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if err = a.rememberWatchSeries(r.Context(), identity(u), s, started); err != nil {
		a.fail(w, r, err)
		return
	}
	if _, err = a.pool.Exec(r.Context(), `UPDATE catalog_series SET metadata=jsonb_set(jsonb_set(metadata,'{episode_count}',to_jsonb($2::int)),'{regular_episode_count}',to_jsonb($3::int)) WHERE cinemeta_id=$1`, s.ID, len(s.Episodes), cinemeta.RegularEpisodeCount(s.Episodes)); err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, false) {
		if err = a.catalog.EnrichTitle(r.Context(), s.Movie); err != nil {
			a.fail(w, r, err)
			return
		}
		titles := []cinemeta.Movie{s.Movie}
		if err = a.webArtwork(r.Context(), titles); err != nil {
			a.fail(w, r, err)
			return
		}
		s.Movie = titles[0]
		respond(w, 200, s)
	}
}
func (a *API) episodeMappings(w http.ResponseWriter, r *http.Request, u accounts.User) {
	if !cinemeta.ValidID(r.PathValue("id")) {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	m, err := a.catalog.EpisodeMappings(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, true) {
		respond(w, 200, m)
	}
}
func (a *API) episodeMappingSave(w http.ResponseWriter, r *http.Request, u accounts.User) {
	var selected catalog.SeriesSelection
	if !decodeLimit(w, r, &selected, 8<<20) {
		return
	}
	if a.Streaming == nil {
		respond(w, 503, map[string]string{"error": "TED media workers are unavailable."})
		return
	}
	op, err := a.Streaming.SubmitMapping(r.Context(), identity(u), r.PathValue("id"), "series", selected)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	respond(w, 202, op)
}

func (a *API) savedSeries(w http.ResponseWriter, r *http.Request, u accounts.User) {
	rows, err := a.pool.Query(r.Context(), `SELECT metadata FROM catalog_series sh WHERE EXISTS(SELECT 1 FROM catalog_seasons se JOIN catalog_episodes e ON e.season_id=se.id JOIN media_mappings m ON m.movie_id=e.media_id WHERE se.series_id=sh.id) ORDER BY metadata->>'name',cinemeta_id`)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []cinemeta.Movie{}
	for rows.Next() {
		var title cinemeta.Movie
		if err = rows.Scan(&title); err != nil {
			a.fail(w, r, err)
			return
		}
		items = append(items, title)
	}
	if err = rows.Err(); err != nil {
		a.fail(w, r, err)
		return
	}
	if a.recheck(w, r, u, false) {
		if err = a.webArtwork(r.Context(), items); err != nil {
			a.fail(w, r, err)
			return
		}
		respond(w, 200, map[string]any{"series": items})
	}
}

func (a *API) rememberWatchSeries(ctx context.Context, who accounts.Identity, s cinemeta.Series, observed ...time.Time) error {
	started := time.Now()
	if len(observed) > 0 {
		started = observed[0]
	}
	if err := a.catalog.SaveSchedule(ctx, s, started); err != nil {
		return err
	}
	entries := make([]watchstate.CatalogEntry, 0, len(s.Episodes))
	for _, episode := range s.Episodes {
		raw, err := json.Marshal(map[string]any{"name": episode.Name, "series_name": s.Name, "poster": s.Poster, "series": s.Movie})
		if err != nil {
			return err
		}
		entries = append(entries, watchstate.CatalogEntry{ID: episode.ID, Metadata: raw})
	}
	return a.watch.RememberCatalog(ctx, who, s.ID, "series", entries, cinemeta.RegularEpisodeCount(s.Episodes))
}
