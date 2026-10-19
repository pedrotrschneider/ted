package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/addons"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
	"entertainmentdistrict/internal/streaming"
	"entertainmentdistrict/internal/watchstate"
)

var nativeUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *API) nativePlaybackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/client/v1/playback/preparations", a.nativeProtected(a.nativePrepareSource))
	mux.HandleFunc("DELETE /api/client/v1/playback/preparations/{id}", a.nativeProtected(a.nativeDiscardSource))
	mux.HandleFunc("POST /api/client/v1/playback/sessions", a.nativeProtected(a.nativeSession))
	mux.HandleFunc("POST /api/client/v1/playback/sources", a.nativeProtected(a.nativeSources))
	mux.HandleFunc("PUT /api/client/v1/playback/sources/{id}/favorite", a.nativeProtected(a.nativeFavorite))
	mux.HandleFunc("GET /api/client/v1/addons", a.nativeProtected(a.nativeAddons))
	mux.HandleFunc("GET /api/client/v1/playback/sessions/{id}/subtitles/{index}", a.nativeProtected(a.nativeSubtitle))
	mux.HandleFunc("GET /api/client/v1/playback/sessions/{id}/media", a.nativeProtected(a.nativeMedia))
	mux.HandleFunc("POST /api/client/v1/playback/events", a.nativeProtected(a.nativeEvents))
	mux.HandleFunc("POST /api/client/v1/downloads/resolve", a.nativeProtected(a.nativeDownloadResolve))
	mux.HandleFunc("POST /api/client/v1/downloads/watch-state", a.nativeProtected(a.nativeDownloadWatch))
	mux.HandleFunc("GET /api/client/v1/downloads/{id}/media", a.nativeProtected(a.nativeDownloadMedia))
	mux.HandleFunc("GET /api/client/v1/watch-state/{kind}/{id}", a.nativeProtected(a.nativeWatchState))
	mux.HandleFunc("POST /api/client/v1/watch-state", a.nativeProtected(a.nativeWatchEdit))
	mux.HandleFunc("GET /api/client/v1/titles/mapped", a.nativeProtected(a.nativeMappedTitles))
	mux.HandleFunc("GET /api/client/v1/titles/{kind}/{id}", a.nativeProtected(a.nativeTitle))
	mux.HandleFunc("GET /api/client/v1/watch-history", a.nativeProtected(a.nativeHistory))
	a.nativeCatalogRoutes(mux)
}

// The initial player lists explicit ready mappings. Full discovery and personal
// home rows belong to T3; this read never creates library membership.
func (a *API) nativeMappedTitles(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	after := r.URL.Query().Get("after")
	if after != "" && !nativeUUID.MatchString(after) {
		a.nativeWatchFailure(w, r, watchstate.ErrInvalid)
		return
	}
	rows, err := a.pool.Query(r.Context(), `SELECT c.id::text,c.kind,c.cinemeta_id,c.metadata,COALESCE(l.available,false)
 FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id
 JOIN streaming_items s ON s.movie_id=c.id AND s.mapping_id=m.id AND s.published AND s.phase='ready'
 LEFT JOIN local_files l ON l.movie_id=c.id
 WHERE ($1='' OR c.id>NULLIF($1,'')::uuid) ORDER BY c.id LIMIT 51`, after)
	if err != nil {
		a.nativePlaybackFailure(w, r, err)
		return
	}
	// Close the cursor before state queries, including small one-connection pools.
	type retained struct {
		key   string
		media catalog.MediaIdentity
		raw   []byte
		local bool
	}
	items := make([]retained, 0, 51)
	for rows.Next() {
		var item retained
		if err = rows.Scan(&item.key, &item.media.Kind, &item.media.ID, &item.raw, &item.local); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		a.nativePlaybackFailure(w, r, err)
		return
	}
	var next *string
	if len(items) > 50 {
		key := items[49].key
		next = &key
		items = items[:50]
	}
	titles := make([]nativeTitleDetail, 0, len(items))
	for _, item := range items {
		detail, err := a.retainedNativeTitle(r, g, item.media, item.raw, true, item.local)
		if err != nil {
			a.nativePlaybackFailure(w, r, err)
			return
		}
		titles = append(titles, detail)
	}
	respond(w, 200, map[string]any{"titles": titles, "next_after": next})
}

type nativeMetadata struct {
	cinemeta.Movie
	Series  *cinemeta.Movie   `json:"series,omitempty"`
	Episode *cinemeta.Episode `json:"episode,omitempty"`
}
type nativeTitleDetail struct {
	Watchlist      bool                       `json:"watchlist"`
	UserRating     *int                       `json:"user_rating"`
	UserWatches    []watchstate.UserIndicator `json:"user_watches,omitempty"`
	JointWatches   []jointWatch               `json:"joint_watches,omitempty"`
	Media          catalog.MediaIdentity      `json:"media"`
	Metadata       nativeMetadata             `json:"metadata"`
	State          watchstate.NativeState     `json:"watch_state"`
	EpisodeCounts  *nativeEpisodeCounts       `json:"episode_counts,omitempty"`
	Ready          bool                       `json:"mapped_ready"`
	Local          bool                       `json:"local_available"`
	Parent         *catalog.MediaIdentity     `json:"parent,omitempty"`
	Seasons        []int                      `json:"seasons,omitempty"`
	MetadataStatus string                     `json:"metadata_status"`
	Issues         []nativeCatalogIssue       `json:"issues"`
	Enrichment     *metadata.Snapshot         `json:"enrichment,omitempty"`
}

// A known schedule may have zero released main-season episodes. Missing counts
// mean the schedule is unknown, not that the series is complete.
type nativeEpisodeCounts struct {
	Watched int `json:"watched"`
	Total   int `json:"total"`
}

func (a *API) retainedNativeTitle(r *http.Request, g accounts.NativeGrant, identity catalog.MediaIdentity, raw []byte, ready, local bool) (nativeTitleDetail, error) {
	var metadata nativeMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nativeTitleDetail{}, err
	}
	state, err := a.watch.NativeState(r.Context(), g.User.ID, identity)
	detail := nativeTitleDetail{Media: identity, Metadata: metadata, State: state, Ready: ready, Local: local, MetadataStatus: "retained", Issues: []nativeCatalogIssue{}}
	sanitizeNativeMetadata(&detail.Metadata)
	return detail, err
}

func (a *API) nativeHistory(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			a.nativeWatchFailure(w, r, watchstate.ErrInvalid)
			return
		}
	}
	page, err := a.watch.NativeHistory(r.Context(), g.User.ID, before)
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	respond(w, 200, page)
}

func (a *API) nativeSession(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Media     catalog.MediaIdentity `json:"media"`
		SourceID  string                `json:"source_id"`
		Beginning bool                  `json:"start_from_beginning"`
	}
	if !decode(w, r, &input) {
		return
	}
	if a.Streaming == nil {
		a.nativePlaybackFailure(w, r, streaming.ErrUnavailable)
		return
	}
	var session streaming.NativeSession
	var err error
	if input.SourceID != "" {
		session, err = a.addons.CreateSession(r.Context(), a.NativeAuth, g, input.Media, input.SourceID)
	} else {
		session, err = a.Streaming.CreateNativeSession(r.Context(), a.NativeAuth, g, input.Media)
	}
	if err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	if input.Beginning {
		session.ResumeMS = 0
	}
	if a.Metadata != nil {
		media := input.Media
		if media.Kind == "episode" {
			media.Kind, media.ID = "series", strings.Split(media.ID, ":")[0]
		}
		if cached, e := a.Metadata.Lookup(r.Context(), metadata.Request{Kind: media.Kind, TitleID: media.ID}); e == nil {
			session.OriginalLanguage = cached.OriginalLanguage
		}
	}
	respond(w, 201, session)
}

func (a *API) nativeMedia(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	if a.Streaming == nil {
		a.nativePlaybackFailure(w, r, streaming.ErrUnavailable)
		return
	}
	err := a.addons.Serve(w, r, g, r.PathValue("id"))
	if errors.Is(err, addons.ErrNotAddon) {
		err = a.Streaming.ServeNativeSession(w, r, g, r.PathValue("id"))
	}
	if err != nil {
		a.nativeSourceFailure(w, r, err)
	}
}

func (a *API) nativeEvents(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Events   []watchstate.NativeEvent          `json:"events"`
		Sessions []watchstate.NativeOfflineSession `json:"sessions"`
	}
	if !decodeLimit(w, r, &input, 64<<10) {
		return
	}
	results, err := a.watch.IngestNativeOffline(r.Context(), a.NativeAuth, g, input.Events, input.Sessions)
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"events": results})
}

func (a *API) nativeWatchState(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	state, err := a.watch.NativeState(r.Context(), g.User.ID, catalog.MediaIdentity{Kind: r.PathValue("kind"), ID: r.PathValue("id")})
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	respond(w, 200, state)
}

func (a *API) nativeWatchEdit(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input watchstate.NativeEdit
	if !decode(w, r, &input) {
		return
	}
	state, err := a.watch.EditNative(r.Context(), a.NativeAuth, g, input)
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	respond(w, 200, state)
}

func (a *API) nativeTitle(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	a.nativeCatalogDetail(w, r, g)
}

func (a *API) nativePlaybackFailure(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 503, "source_unavailable"
	switch {
	case errors.Is(err, streaming.ErrReleaseChanged):
		status, code = 409, "release_changed"
	case errors.Is(err, streaming.ErrNotReady):
		status, code = 409, "mapping_not_ready"
	case errors.Is(err, streaming.ErrInvalid):
		status, code = 400, "invalid_media"
	case errors.Is(err, streaming.ErrUnavailable):
	default:
		a.nativeFail(w, r, err)
		return
	}
	if a.logger != nil {
		a.logger.WarnContext(r.Context(), "native playback request failed", "operation", r.Pattern, "status", status, "code", code)
	}
	respond(w, status, map[string]string{"code": code, "error": err.Error()})
}

func (a *API) nativeWatchFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, accounts.ErrUnauthorized) {
		a.nativeFail(w, r, err)
		return
	}
	a.watchFailure(w, r, err)
}

func (a *API) nativePrepareSource(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	var input struct {
		Media    catalog.MediaIdentity `json:"media"`
		SourceID string                `json:"source_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := a.addons.PrepareSource(r.Context(), a.NativeAuth, g, input.Media, input.SourceID); err != nil {
		a.nativeSourceFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"ready": true})
}
func (a *API) nativeDiscardSource(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	a.addons.DiscardPreparation(g, r.PathValue("id"))
	respond(w, 200, map[string]any{"discarded": true})
}
