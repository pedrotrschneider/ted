package watchstate

import (
	"context"
	"time"

	"entertainmentdistrict/internal/catalog"
)

// Completion evidence is distinct from last playback activity and import
// receipt time. An old played flag plus a new progress event is not completion.
type NativeDownloadWatch struct {
	State       NativeState `json:"state"`
	InProgress  bool        `json:"in_progress"`
	ObservedAt  *time.Time  `json:"observed_at"`
	CompletedAt *time.Time  `json:"completed_at"`
}

func (s *Store) NativeDownloadWatches(ctx context.Context, user int64, media []catalog.MediaIdentity) ([]NativeDownloadWatch, error) {
	if len(media) < 1 || len(media) > 200 {
		return nil, ErrInvalid
	}
	kinds, ids := make([]string, 0, len(media)), make([]string, 0, len(media))
	seen := map[catalog.MediaIdentity]bool{}
	for _, item := range media {
		if !item.Valid() || item.Kind == "series" || seen[item] {
			return nil, ErrInvalid
		}
		seen[item] = true
		kinds, ids = append(kinds, item.Kind), append(ids, item.ID)
	}
	rows, err := s.pool.Query(ctx, `WITH requested AS (
 SELECT kind,title_id,ord FROM unnest($2::text[],$3::text[]) WITH ORDINALITY AS r(kind,title_id,ord)
), matched AS (
 SELECT r.*,c.id AS media_id FROM requested r LEFT JOIN catalog_media c ON c.kind=r.kind AND c.cinemeta_id=r.title_id
), completed AS (
 SELECT h.media_id,max(h.observed_at) AS at FROM watch_history h JOIN matched m ON m.media_id=h.media_id
 LEFT JOIN native_watch_events e ON e.id=h.native_event_id
 WHERE h.user_id=$1 AND h.kind IN ('playback_completed','ted_watched','manual_watched')
 AND COALESCE((h.state->>'played')::boolean,false) AND (h.native_event_id IS NULL OR e.outcome='applied')
 GROUP BY h.media_id
)
 SELECT m.kind,m.title_id,COALESCE(w.state,'{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'::jsonb),
 COALESCE(w.revision,0),w.duration_ms,COALESCE((`+NativeUnfinishedWatchSQL+`),false),w.observed_at,
 GREATEST(h.at,r.watched_at)
 FROM matched m LEFT JOIN watch_current w ON w.media_id=m.media_id AND w.user_id=$1
 LEFT JOIN completed h ON h.media_id=m.media_id
 LEFT JOIN stremio_episode_recency r ON r.media_id=m.media_id AND r.user_id=$1
 ORDER BY m.ord`, user, kinds, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NativeDownloadWatch, 0, len(media))
	for rows.Next() {
		var value NativeDownloadWatch
		var identity catalog.MediaIdentity
		var state State
		var revision int64
		var duration *int64
		if err = rows.Scan(&identity.Kind, &identity.ID, &state, &revision, &duration, &value.InProgress, &value.ObservedAt, &value.CompletedAt); err != nil {
			return nil, err
		}
		value.State = nativeState(identity, revision, state, duration)
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
