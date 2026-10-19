package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/titleprefs"
	"entertainmentdistrict/internal/watchstate"
	"github.com/jackc/pgx/v5"
)

type nativeCatalogIssue struct {
	Code  string                 `json:"code"`
	Media *catalog.MediaIdentity `json:"media,omitempty"`
}
type nativeCatalogPage struct {
	Titles    []nativeTitleDetail `json:"titles"`
	NextSkip  *int                `json:"next_skip"`
	NextAfter string              `json:"next_after,omitempty"`
}
type nativePersonalPage struct {
	Titles    []nativeTitleDetail  `json:"titles"`
	NextAfter string               `json:"next_after"`
	Issues    []nativeCatalogIssue `json:"issues"`
}

func (a *API) nativeCatalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/client/v1/catalog/{kind}", a.nativeProtected(a.nativeCatalogBrowse))
	mux.HandleFunc("GET /api/client/v1/titles/series/{id}/episodes", a.nativeProtected(a.nativeEpisodes))
	mux.HandleFunc("GET /api/client/v1/titles/episode/{id}/adjacent", a.nativeProtected(a.nativeAdjacentEpisodes))
	mux.HandleFunc("GET /api/client/v1/titles/series/{id}/watch-state", a.nativeProtected(a.nativeSeriesTitleWatch))
	mux.HandleFunc("POST /api/client/v1/titles/series/{id}/watch-state", a.nativeProtected(a.nativeSeriesTitleWatch))
	mux.HandleFunc("GET /api/client/v1/titles/series/{id}/seasons/{season}/watch-state", a.nativeProtected(a.nativeSeasonWatch))
	mux.HandleFunc("POST /api/client/v1/titles/series/{id}/seasons/{season}/watch-state", a.nativeProtected(a.nativeSeasonWatch))
	mux.HandleFunc("GET /api/client/v1/rows/{row}", a.nativeProtected(a.nativePersonalRows))
	mux.HandleFunc("GET /api/client/v1/home", a.nativeProtected(a.nativeHome))
}

func (a *API) nativeAdjacentEpisodes(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	media := catalog.MediaIdentity{Kind: "episode", ID: r.PathValue("id")}
	if !media.Valid() {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	show, status, err := a.loadNativeSeries(r.Context(), watchstate.NativeParentID(media), false)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	var current *cinemeta.Episode
	for i := range show.Episodes {
		if show.Episodes[i].ID == media.ID {
			current = &show.Episodes[i]
			break
		}
	}
	if current == nil {
		a.catalogFailure(w, r, cinemeta.ErrNotFound)
		return
	}
	loc := a.CalendarLocation
	if loc == nil {
		loc = time.UTC
	}
	today := time.Now().In(loc).Format(time.DateOnly)
	ordered := append([]cinemeta.Episode(nil), show.Episodes...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Season != ordered[j].Season {
			return ordered[i].Season < ordered[j].Season
		}
		return ordered[i].Number < ordered[j].Number
	})
	var previous, next *nativeTitleDetail
	for _, episode := range ordered {
		// Specials have their own lane; regular playback never jumps into them.
		if (episode.Season == 0) != (current.Season == 0) || episode.ReleaseDate == "" || episode.ReleaseDate > today {
			continue
		}
		before := episode.Season < current.Season || episode.Season == current.Season && episode.Number < current.Number
		after := episode.Season > current.Season || episode.Season == current.Season && episode.Number > current.Number
		if before {
			value := nativeEpisodeDetail(show.Movie, episode, status)
			previous = &value
		}
		if after && next == nil {
			value := nativeEpisodeDetail(show.Movie, episode, status)
			next = &value
		}
	}
	for _, value := range []*nativeTitleDetail{previous, next} {
		if value == nil {
			continue
		}
		page := []nativeTitleDetail{*value}
		if err = a.decorateNative(r.Context(), g.User.ID, page); err != nil {
			a.nativeFail(w, r, err)
			return
		}
		*value = page[0]
	}
	a.respondNativeCatalog(w, r, map[string]any{"previous": previous, "next": next})
}

func sanitizeNativeMovie(m *cinemeta.Movie) {
	m.Poster = cinemeta.PosterURL(m.Poster)
	m.Background = cinemeta.PosterURL(m.Background)
	m.Logo = cinemeta.PosterURL(m.Logo)
}
func sanitizeNativeMetadata(m *nativeMetadata) {
	sanitizeNativeMovie(&m.Movie)
	if m.Series != nil {
		sanitizeNativeMovie(m.Series)
	}
	if m.Episode != nil {
		m.Episode.Image = cinemeta.PosterURL(m.Episode.Image)
	}
}
func nativeDetail(title cinemeta.Movie, status string) nativeTitleDetail {
	sanitizeNativeMovie(&title)
	identity := catalog.MediaIdentity{Kind: title.Type, ID: title.ID}
	return nativeTitleDetail{Media: identity, Metadata: nativeMetadata{Movie: title}, State: watchstate.NativeState{Media: identity}, MetadataStatus: status, Issues: []nativeCatalogIssue{}}
}
func nativeEpisodeDetail(show cinemeta.Movie, episode cinemeta.Episode, status string) nativeTitleDetail {
	detail := nativeDetail(show, status)
	detail.Media = catalog.MediaIdentity{Kind: "episode", ID: episode.ID}
	detail.State.Media = detail.Media
	detail.Parent = &catalog.MediaIdentity{Kind: "series", ID: show.ID}
	detail.Metadata.Series = &show
	detail.Metadata.Episode = &episode
	detail.Metadata.ID = episode.ID
	detail.Metadata.Type = "episode"
	detail.Metadata.Name = episode.Name
	detail.Metadata.Description = episode.Description
	sanitizeNativeMetadata(&detail.Metadata)
	return detail
}

// Decorate a bounded page with batched reads, including never-mapped identities.
// Metadata/discovery does not enroll items into the shared library or history.
func (a *API) decorateNative(ctx context.Context, user int64, titles []nativeTitleDetail) error {
	if err := a.enrichNativeCached(ctx, titles); err != nil {
		return err
	}
	return a.decorateNativeState(ctx, user, titles)
}

// Personal rows already own their viewing-session state. Share only the cached
// metadata decoration so artwork cannot replace that state with watched flags.
func (a *API) enrichNativeCached(ctx context.Context, titles []nativeTitleDetail) error {
	if a.Metadata != nil {
		media := make([]catalog.MediaIdentity, len(titles))
		for i := range titles {
			media[i] = titles[i].Media
		}
		cached, err := a.Metadata.CatalogSnapshots(ctx, media)
		if err != nil {
			return err
		}
		for i := range titles {
			if snapshot, ok := cached[titles[i].Media]; ok {
				titles[i].Enrichment = &snapshot
			}
		}
	}
	return nil
}

func (a *API) decorateNativeState(ctx context.Context, user int64, titles []nativeTitleDetail) error {
	if err := a.decorateJointWatch(ctx, user, titles); err != nil {
		return err
	}
	ids := make([]string, 0, len(titles))
	seriesIDs := []string{}
	seenSeries := map[string]bool{}
	indexes := map[string][]int{}
	for i, t := range titles {
		if t.Media.Kind == "series" {
			if !seenSeries[t.Media.ID] {
				seriesIDs = append(seriesIDs, t.Media.ID)
				seenSeries[t.Media.ID] = true
			}
		} else {
			ids = append(ids, t.Media.ID)
			indexes[t.Media.ID] = append(indexes[t.Media.ID], i)
		}
	}
	if len(seriesIDs) > 0 {
		counts, err := a.catalog.ReleasedCounts(ctx, seriesIDs, user, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
		if err != nil {
			return err
		}
		for i := range titles {
			t := &titles[i]
			if t.Media.Kind != "series" {
				continue
			}
			c := counts[t.Media.ID]
			if c.Known {
				t.EpisodeCounts = &nativeEpisodeCounts{Watched: c.Watched, Total: c.Available}
				t.State.Played = c.Available > 0 && c.Watched == c.Available
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT c.cinemeta_id,COALESCE(w.state,'{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'::jsonb),COALESCE(w.revision,0),w.duration_ms,
 EXISTS(SELECT 1 FROM streaming_items si JOIN media_mappings mm ON mm.id=si.mapping_id AND mm.movie_id=si.movie_id WHERE si.movie_id=c.id AND si.published AND si.phase='ready'),COALESCE(l.available,false)
 FROM catalog_media c LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=$1 LEFT JOIN local_files l ON l.movie_id=c.id WHERE c.cinemeta_id=ANY($2::text[])`, user, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var state watchstate.State
		var revision int64
		var duration *int64
		var ready, local bool
		if err = rows.Scan(&id, &state, &revision, &duration, &ready, &local); err != nil {
			return err
		}
		for _, i := range indexes[id] {
			t := &titles[i]
			t.State = watchstate.NativeState{Media: t.Media, Revision: revision, Played: state.Played, PositionMS: state.Position / 10000, DurationMS: duration, PlayCount: state.PlayCount, LastPlayed: state.LastPlayed}
			t.Ready = ready
			t.Local = local
		}
	}
	return rows.Err()
}

func (a *API) browseNative(ctx context.Context, user int64, kind, search string, skip int, filters cinemeta.Filters) (nativeCatalogPage, error) {
	page := nativeCatalogPage{Titles: []nativeTitleDetail{}}
	var movies []cinemeta.Movie
	var err error
	switch kind {
	case "movie":
		var upstream cinemeta.Page
		upstream, err = a.movies.Browse(ctx, search, skip, filters)
		movies = upstream.Movies
		page.NextSkip = upstream.NextSkip
	case "series":
		var upstream cinemeta.SeriesPage
		upstream, err = a.series.BrowseSeries(ctx, search, skip, filters)
		movies = upstream.Series
		page.NextSkip = upstream.NextSkip
	default:
		return page, cinemeta.ErrInvalid
	}
	if err != nil {
		return page, err
	}
	for _, m := range movies {
		page.Titles = append(page.Titles, nativeDetail(m, "fresh"))
	}
	err = a.decorateNative(ctx, user, page.Titles)
	return page, err
}
func (a *API) nativeCatalogBrowse(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	w.Header().Set("Cache-Control", "no-store")
	if handled := a.nativeFilteredCatalog(w, r, g); handled {
		return
	}
	skip := 0
	var err error
	if raw := r.URL.Query().Get("skip"); raw != "" {
		skip, err = strconv.Atoi(raw)
	}
	if err != nil {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	filters, err := catalogFilters(r)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	// Cinemeta's year catalogue uses the genre slot for the year. Intersect
	// its returned summaries locally, retaining the upstream paging cursor.
	genre := ""
	if filters.Catalog == "new" {
		genre, filters.Genre = filters.Genre, ""
		if !cinemeta.ValidGenre(genre) {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return
		}
		if filters.Year == 0 {
			filters.Year = time.Now().In(a.CalendarLocation).Year()
		}
	}
	page, err := a.browseNative(r.Context(), g.User.ID, r.PathValue("kind"), r.URL.Query().Get("search"), skip, filters)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if genre != "" {
		filtered := []nativeTitleDetail{}
		for _, title := range page.Titles {
			for _, value := range title.Metadata.Genres {
				if value == genre {
					filtered = append(filtered, title)
					break
				}
			}
		}
		page.Titles = filtered
	}
	a.respondNativeCatalog(w, r, page)
}
func (a *API) respondNativeCatalog(w http.ResponseWriter, r *http.Request, value any) {
	raw, err := accessHeader(r)
	if err == nil {
		_, err = a.NativeAuth.Authenticate(r.Context(), raw)
	}
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	respond(w, 200, value)
}

func (a *API) loadNativeSeries(ctx context.Context, id string, refresh bool) (cinemeta.Series, string, error) {
	if !cinemeta.ValidID(id) {
		return cinemeta.Series{}, "", cinemeta.ErrInvalid
	}
	if !refresh {
		if cached, err := a.catalog.NativeSeries(ctx, id); err == nil {
			return cached, "cached", nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return cinemeta.Series{}, "", err
		}
	}
	started := time.Now()
	show, err := a.series.Series(ctx, id)
	if err == nil {
		if show.ID != id {
			return cinemeta.Series{}, "", cinemeta.ErrIncomplete
		}
		if err = a.catalog.SaveSchedule(ctx, show, started); err != nil {
			return cinemeta.Series{}, "", err
		}
		return show, "fresh", nil
	}
	cached, cacheErr := a.catalog.NativeSeries(ctx, id)
	if cacheErr == nil {
		return cached, "cached", nil
	}
	if !errors.Is(cacheErr, pgx.ErrNoRows) {
		return cinemeta.Series{}, "", cacheErr
	}
	return cinemeta.Series{}, "", err
}

func (a *API) nativeCatalogDetail(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if kind == "series" {
		show, status, err := a.loadNativeSeries(r.Context(), id, true)
		if err != nil {
			a.catalogFailure(w, r, err)
			return
		}
		detail := nativeDetail(show.Movie, status)
		seen := map[int]bool{}
		for _, e := range show.Episodes {
			if !seen[e.Season] {
				detail.Seasons = append(detail.Seasons, e.Season)
				seen[e.Season] = true
			}
		}
		sort.Ints(detail.Seasons)
		if status == "cached" {
			detail.Issues = append(detail.Issues, nativeCatalogIssue{Code: "catalog_cached"})
		}
		titles := []nativeTitleDetail{detail}
		if err = a.decorateNative(r.Context(), g.User.ID, titles); err != nil {
			a.nativeFail(w, r, err)
			return
		}
		detail = titles[0]
		a.enrichNativeDetail(r, &detail)
		a.respondNativeCatalog(w, r, detail)
		return
	}
	media := catalog.MediaIdentity{Kind: kind, ID: id}
	if !media.Valid() {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	var retained nativeMetadata
	err := a.pool.QueryRow(r.Context(), `SELECT metadata FROM catalog_media WHERE kind=$1 AND cinemeta_id=$2`, kind, id).Scan(&retained)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.nativeFail(w, r, err)
		return
	}
	var detail nativeTitleDetail
	if kind == "movie" {
		// Ready legacy mappings already retain selected Cinemeta metadata. Serve it
		// without an external prerequisite, while unmapped details use durable cache.
		if retained.Name != "" {
			retained.ID = id
			retained.Type = kind
			detail = nativeDetail(retained.Movie, "retained")
		} else {
			started := time.Now()
			title, readErr := a.movies.Movie(r.Context(), id)
			if readErr == nil {
				if title.ID != id || title.Type != kind {
					a.catalogFailure(w, r, cinemeta.ErrIncomplete)
					return
				}
				if err = a.catalog.SaveNativeTitle(r.Context(), title, started); err != nil {
					a.nativeFail(w, r, err)
					return
				}
				detail = nativeDetail(title, "fresh")
			} else {
				cached, cacheErr := a.catalog.NativeTitle(r.Context(), kind, id)
				if cacheErr != nil {
					if errors.Is(cacheErr, pgx.ErrNoRows) {
						a.catalogFailure(w, r, readErr)
					} else {
						a.nativeFail(w, r, cacheErr)
					}
					return
				}
				detail = nativeDetail(cached, "cached")
				detail.Issues = append(detail.Issues, nativeCatalogIssue{Code: "catalog_cached"})
			}
		}
	} else {
		show, status, readErr := a.loadNativeSeries(r.Context(), watchstate.NativeParentID(media), true)
		if readErr != nil {
			if retained.Episode == nil {
				a.catalogFailure(w, r, readErr)
				return
			}
			detail = nativeTitleDetail{Media: media, Metadata: retained, MetadataStatus: "retained", Issues: []nativeCatalogIssue{{Code: "catalog_cached"}}, State: watchstate.NativeState{Media: media}}
			parent := catalog.MediaIdentity{Kind: "series", ID: watchstate.NativeParentID(media)}
			detail.Parent = &parent
			sanitizeNativeMetadata(&detail.Metadata)
		} else {
			found := false
			for _, e := range show.Episodes {
				if e.ID == id {
					detail = nativeEpisodeDetail(show.Movie, e, status)
					found = true
					break
				}
			}
			if !found {
				a.catalogFailure(w, r, cinemeta.ErrNotFound)
				return
			}
			if status == "cached" {
				detail.Issues = append(detail.Issues, nativeCatalogIssue{Code: "catalog_cached"})
			}
		}
	}
	titles := []nativeTitleDetail{detail}
	if err = a.decorateNative(r.Context(), g.User.ID, titles); err != nil {
		a.nativeFail(w, r, err)
		return
	}
	a.enrichNativeDetail(r, &titles[0])
	a.respondNativeCatalog(w, r, titles[0])
}

func (a *API) nativeEpisodes(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	season, err := strconv.Atoi(r.URL.Query().Get("season"))
	after := 0
	if err != nil || season < 0 || season > 9999 {
		a.catalogFailure(w, r, cinemeta.ErrInvalid)
		return
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.Atoi(raw)
		if err != nil || after < 0 || after > 99999 {
			a.catalogFailure(w, r, cinemeta.ErrInvalid)
			return
		}
	}
	show, status, err := a.loadNativeSeries(r.Context(), r.PathValue("id"), false)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	page := struct {
		Titles         []nativeTitleDetail  `json:"titles"`
		NextAfter      *int                 `json:"next_after"`
		MetadataStatus string               `json:"metadata_status"`
		Enrichment     *metadata.Snapshot   `json:"enrichment,omitempty"`
		Issues         []nativeCatalogIssue `json:"issues"`
	}{Titles: []nativeTitleDetail{}, MetadataStatus: status, Issues: []nativeCatalogIssue{}}
	if a.Metadata != nil {
		req := metadata.Request{Kind: "season", TitleID: show.ID, Season: season}
		v, err := a.Metadata.Ensure(r.Context(), "Tvdb", req, metadata.Item{Type: "Season", Name: "Season " + strconv.Itoa(season), IndexNumber: &season})
		if err != nil {
			page.Issues = append(page.Issues, nativeCatalogIssue{Code: "metadata_unavailable"})
		} else {
			page.Enrichment = &v
		}
	}
	// The first page must include episode number zero if Cinemeta actually supplies it.
	for _, e := range show.Episodes {
		if e.Season == season && (r.URL.Query().Get("after") == "" || e.Number > after) {
			page.Titles = append(page.Titles, nativeEpisodeDetail(show.Movie, e, status))
		}
	}
	if len(page.Titles) > 50 {
		n := page.Titles[49].Metadata.Episode.Number
		page.NextAfter = &n
		page.Titles = page.Titles[:50]
	}
	if err = a.decorateNative(r.Context(), g.User.ID, page.Titles); err != nil {
		a.nativeFail(w, r, err)
		return
	}
	if a.Metadata != nil {
		for i := range page.Titles {
			ep := page.Titles[i].Metadata.Episode
			v, e := a.Metadata.Lookup(r.Context(), metadata.Request{Kind: "episode", TitleID: show.ID, Season: ep.Season, Number: ep.Number})
			if e == nil {
				page.Titles[i].Enrichment = &v
			} else if !errors.Is(e, pgx.ErrNoRows) {
				page.Issues = append(page.Issues, nativeCatalogIssue{Code: "metadata_unavailable"})
				break
			}
		}
	}
	a.respondNativeCatalog(w, r, page)
}

func (a *API) personalNative(ctx context.Context, user int64, row, after string) (nativePersonalPage, error) {
	if row == "watchlist" {
		page, err := titleprefs.New(a.pool).List(ctx, user, "watchlist", "", after, true)
		if err != nil {
			return nativePersonalPage{}, err
		}
		return a.preferenceNativePage(ctx, user, page)
	}
	loc := a.CalendarLocation
	if loc == nil {
		loc = time.UTC
	}
	page, err := a.watch.PersonalRows(ctx, user, row, after, time.Now().In(loc).Format(time.DateOnly), loc)
	result := nativePersonalPage{Titles: []nativeTitleDetail{}, Issues: []nativeCatalogIssue{}, NextAfter: page.Next}
	if err != nil {
		return result, err
	}
	for _, item := range page.Items {
		if item.MissingSchedule {
			media := catalog.MediaIdentity{Kind: "series", ID: item.Key}
			result.Issues = append(result.Issues, nativeCatalogIssue{Code: "schedule_unavailable", Media: &media})
			continue
		}
		var meta nativeMetadata
		if err = json.Unmarshal(item.Metadata, &meta); err != nil {
			return result, err
		}
		var show cinemeta.Movie
		if err = json.Unmarshal(item.SeriesMetadata, &show); err != nil {
			return result, err
		}
		if item.Media.Kind == "episode" {
			if show.Name == "" && meta.Series != nil {
				show = *meta.Series
			}
			show.ID = watchstate.NativeParentID(item.Media)
			show.Type = "series"
			episode := item.Episode
			if episode == nil {
				episode = meta.Episode
			}
			if episode != nil {
				meta = nativeEpisodeDetail(show, *episode, "cached").Metadata
			}
			if show.Name == "" {
				result.Issues = append(result.Issues, nativeCatalogIssue{Code: "metadata_unavailable", Media: &item.Media})
			}
		}
		meta.ID = item.Media.ID
		meta.Type = item.Media.Kind
		if meta.Name == "" {
			if item.Media.Kind == "movie" {
				result.Issues = append(result.Issues, nativeCatalogIssue{Code: "metadata_unavailable", Media: &item.Media})
			}
			meta.Name = item.Media.ID
		}
		detail := nativeTitleDetail{Media: item.Media, Metadata: meta, State: item.State, Ready: item.Ready, Local: item.Local, MetadataStatus: "cached", Issues: []nativeCatalogIssue{}}
		if item.Media.Kind == "episode" {
			detail.Parent = &catalog.MediaIdentity{Kind: "series", ID: watchstate.NativeParentID(item.Media)}
		}
		sanitizeNativeMetadata(&detail.Metadata)
		result.Titles = append(result.Titles, detail)
	}
	if err := a.decorateJointWatch(ctx, user, result.Titles); err != nil {
		return result, err
	}
	return result, a.enrichNativeCached(ctx, result.Titles)
}
func (a *API) nativePersonalRows(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	page, err := a.personalNative(r.Context(), g.User.ID, r.PathValue("row"), r.URL.Query().Get("after"))
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, page)
}

func (a *API) nativeHome(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	type homeRow struct {
		ID          string               `json:"id"`
		Titles      []nativeTitleDetail  `json:"titles"`
		NextAfter   string               `json:"next_after"`
		NextSkip    *int                 `json:"next_skip"`
		Issues      []nativeCatalogIssue `json:"issues"`
		Unavailable bool                 `json:"unavailable"`
	}
	rows := make([]homeRow, 0, 6)
	for _, name := range []string{"continue-watching", "new-releases", "next-up", "watchlist"} {
		page, err := a.personalNative(r.Context(), g.User.ID, name, "")
		if err != nil {
			a.nativeWatchFailure(w, r, err)
			return
		}
		rows = append(rows, homeRow{ID: name, Titles: page.Titles, NextAfter: page.NextAfter, Issues: page.Issues})
	}
	var pages [2]nativeCatalogPage
	var failures [2]error
	var wg sync.WaitGroup
	for i, kind := range []string{"movie", "series"} {
		wg.Add(1)
		go func(i int, kind string) {
			defer wg.Done()
			pages[i], failures[i] = a.browseNative(r.Context(), g.User.ID, kind, "", 0, cinemeta.Filters{})
		}(i, kind)
	}
	wg.Wait()
	for i, kind := range []string{"movie", "series"} {
		row := homeRow{ID: "popular-" + kind, Titles: pages[i].Titles, NextSkip: pages[i].NextSkip, Issues: []nativeCatalogIssue{}}
		if failures[i] != nil {
			// A failed catalog row is visibly unavailable; partial upstream contents are never a smaller successful row.
			row.Titles = []nativeTitleDetail{}
			row.NextSkip = nil
			row.Unavailable = true
			code := "catalog_unavailable"
			if errors.Is(failures[i], cinemeta.ErrIncomplete) {
				code = "catalog_incomplete"
			}
			row.Issues = append(row.Issues, nativeCatalogIssue{Code: code})
			a.logger.Warn("native home discovery unavailable", "kind", kind, "code", code)
		}
		rows = append(rows, row)
	}
	a.respondNativeCatalog(w, r, map[string]any{"rows": rows})
}
