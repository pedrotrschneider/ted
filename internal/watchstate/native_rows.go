package watchstate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
)

type NativeRow struct {
	Media                    catalog.MediaIdentity
	Metadata, SeriesMetadata []byte
	Episode                  *cinemeta.Episode
	State                    NativeState
	Ready, Local             bool
	At                       time.Time
	Key                      string
	MissingSchedule          bool
}
type NativeRowPage struct {
	Items []NativeRow
	Next  string
}
type nativeCursor struct {
	Row string    `json:"row"`
	At  time.Time `json:"at"`
	Key string    `json:"key"`
}

// Use the same unfinished-session rule for Continue Watching and for excluding
// its shows from Next Up, including qualified viewing seeked back to zero and
// unfinished rewatches. Brief previews never publish a current watch row.
// NativeUnfinishedWatchSQL is shared by personal rows and catalogue filters.
// The query must bind the current watch row as w; session revision/time checks
// keep interrupted rewatches distinct from their earlier completed viewing.
const NativeUnfinishedWatchSQL = `(NOT COALESCE((w.state->>'played')::boolean,false)
 OR (w.source='TEDNativePlayback' AND EXISTS(SELECT 1 FROM native_playback_sessions p
 WHERE p.user_id=w.user_id AND p.media_id=w.media_id AND p.applied_revision=w.revision
 AND p.last_event_at=w.observed_at AND p.started AND NOT p.completed)))
 AND (COALESCE((w.state->>'position_ticks')::bigint,0)>0 OR (w.source='TEDNativePlayback' AND w.state->>'last_played' IS NOT NULL))`

func rowCursor(raw, row string) (nativeCursor, error) {
	if raw == "" {
		return nativeCursor{Row: row, At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), Key: "~"}, nil
	}
	if len(raw) > 512 {
		return nativeCursor{}, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nativeCursor{}, ErrInvalid
	}
	var c nativeCursor
	if json.Unmarshal(data, &c) != nil || c.Row != row || c.At.IsZero() || c.At.Year() < 1970 || c.At.Year() > 9999 ||
		(row == "continue-watching" && !uuid.MatchString(c.Key)) || (row != "continue-watching" && !cinemeta.ValidID(c.Key)) {
		return nativeCursor{}, ErrInvalid
	}
	return c, nil
}

// PersonalRows reads current state, schedule and availability in one PostgreSQL
// snapshot. Removed/unmapped items retain their place; no library join filters
// personal history. A current unfinished native session can be a rewatch; its
// older watched flag is independent. Revision/time matching excludes superseded
// sessions after explicit edits or another device. Cursor ties use stable keys.
func (s *Store) PersonalRows(ctx context.Context, user int64, row, after, today string, calendar ...*time.Location) (NativeRowPage, error) {
	if row != "continue-watching" && row != "next-up" && row != "new-releases" {
		return NativeRowPage{}, ErrInvalid
	}
	cursor, err := rowCursor(after, row)
	if err != nil {
		return NativeRowPage{}, err
	}
	if _, err = time.Parse(time.DateOnly, today); err != nil {
		return NativeRowPage{}, ErrInvalid
	}
	// Select the representative before applying the page cursor. Otherwise an
	// older unfinished episode of the same show can reappear on a later page.
	query := `WITH unfinished AS (
 SELECT DISTINCT ON(c.kind,split_part(c.cinemeta_id,':',1)) w.*
 FROM watch_current w JOIN catalog_media c ON c.id=w.media_id
 WHERE w.user_id=$1 AND ` + NativeUnfinishedWatchSQL + `
 ORDER BY c.kind,split_part(c.cinemeta_id,':',1),w.observed_at DESC,
 CASE WHEN c.kind='episode' THEN split_part(c.cinemeta_id,':',2)::int ELSE 0 END DESC,
 CASE WHEN c.kind='episode' THEN split_part(c.cinemeta_id,':',3)::int ELSE 0 END DESC,c.id::text DESC)
 SELECT c.id::text,c.kind,c.cinemeta_id,COALESCE(n.metadata,c.metadata),COALESCE(p.metadata,c.metadata->'series','{}'::jsonb),ep.value,
 w.state,w.revision,w.duration_ms,
 EXISTS(SELECT 1 FROM streaming_items si JOIN media_mappings mm ON mm.id=si.mapping_id AND mm.movie_id=si.movie_id WHERE si.movie_id=c.id AND si.published AND si.phase='ready'),
 COALESCE(l.available,false),w.observed_at,false
 FROM unfinished w JOIN catalog_media c ON c.id=w.media_id
 LEFT JOIN native_catalog_titles n ON n.kind='movie' AND n.title_id=c.cinemeta_id
 LEFT JOIN native_catalog_titles p ON p.kind='series' AND p.title_id=split_part(c.cinemeta_id,':',1)
 LEFT JOIN catalog_episode_schedules sh ON sh.title_id=split_part(c.cinemeta_id,':',1) AND c.kind='episode'
 LEFT JOIN LATERAL (SELECT value FROM jsonb_array_elements(sh.episodes) WHERE value->>'id'=c.cinemeta_id LIMIT 1) ep ON true
 LEFT JOIN local_files l ON l.movie_id=c.id
 WHERE (w.observed_at,c.id::text)<($2::timestamptz,$3::text)
 ORDER BY w.observed_at DESC,c.id::text DESC LIMIT 51`
	if row == "next-up" || row == "new-releases" {
		// A completed rewatch can move the anchor back into an already watched
		// season. Historical import receipt times are fallback evidence only.
		query = `WITH unfinished AS (
 SELECT DISTINCT split_part(c.cinemeta_id,':',1) AS title_id
 FROM watch_current w JOIN catalog_media c ON c.id=w.media_id
 WHERE w.user_id=$1 AND c.kind='episode' AND ` + NativeUnfinishedWatchSQL + `), watched AS (
 SELECT split_part(c.cinemeta_id,':',1) AS title_id,split_part(c.cinemeta_id,':',2)::int AS season,split_part(c.cinemeta_id,':',3)::int AS number,
 COALESCE(GREATEST(h.at,r.watched_at),w.observed_at) AS at,h.at IS NOT NULL OR r.watched_at IS NOT NULL AS known,
 COALESCE((w.state->>'last_played')::timestamptz,CASE WHEN w.source IN ('StremioImport','StremioPoll') THEN r.watched_at END,u.created_at) AS watch_at
 FROM watch_current w JOIN catalog_media c ON c.id=w.media_id
 LEFT JOIN stremio_episode_recency r ON r.user_id=w.user_id AND r.media_id=w.media_id
 JOIN users u ON u.id=w.user_id
 LEFT JOIN LATERAL (
 SELECT max(CASE WHEN h.kind='stremio_import' THEN NULL WHEN h.kind IN ('baseline','state_observed','kept_imported','watch_date_corrected') THEN (h.state->>'last_played')::timestamptz ELSE h.observed_at END) AS at
 FROM watch_history h LEFT JOIN native_watch_events e ON e.id=h.native_event_id
 WHERE h.user_id=w.user_id AND h.media_id=w.media_id AND COALESCE((h.state->>'played')::boolean,false)
 AND h.kind IN ('playback_completed','ted_watched','manual_watched','watch_date_corrected','stremio_import','stremio_observed','baseline','state_observed','kept_imported')
 AND (h.native_event_id IS NULL OR e.outcome='applied')) h ON true
 WHERE w.user_id=$1 AND c.kind='episode' AND COALESCE((w.state->>'played')::boolean,false)
 AND COALESCE(NULLIF(split_part(c.cinemeta_id,':',2),''),'0')::int>0
 AND NOT EXISTS(SELECT 1 FROM unfinished u WHERE u.title_id=split_part(c.cinemeta_id,':',1))
 ), active AS (
 SELECT DISTINCT ON(title_id) title_id,season,number,at,watch_at
 FROM watched ORDER BY title_id,known DESC,at DESC,season DESC,number DESC)
 SELECT a.title_id,CASE WHEN ep.value IS NULL THEN 'series' ELSE 'episode' END,COALESCE(ep.value->>'id',a.title_id),COALESCE(c.metadata,'{}'::jsonb),COALESCE(n.metadata,cs.metadata,'{}'::jsonb),ep.value,
 COALESCE(w.state,'{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'::jsonb),COALESCE(w.revision,0),w.duration_ms,
 EXISTS(SELECT 1 FROM streaming_items si JOIN media_mappings mm ON mm.id=si.mapping_id AND mm.movie_id=si.movie_id WHERE si.movie_id=c.id AND si.published AND si.phase='ready'),
 COALESCE(l.available,false),a.at,sh.title_id IS NULL
 FROM active a LEFT JOIN catalog_episode_schedules sh USING(title_id)
 LEFT JOIN native_catalog_titles n ON n.kind='series' AND n.title_id=a.title_id
 LEFT JOIN catalog_series cs ON cs.cinemeta_id=a.title_id
 LEFT JOIN LATERAL (
 SELECT e.value FROM jsonb_array_elements(sh.episodes) e(value)
 WHERE (e.value->>'season')::int>0 AND NULLIF(e.value->>'release_date','')::date<=$4::date
 AND ((e.value->>'season')::int,(e.value->>'number')::int)>(a.season,a.number)
 AND NOT EXISTS (
 SELECT 1 FROM catalog_media candidate JOIN watch_current cw ON cw.media_id=candidate.id AND cw.user_id=$1
 WHERE candidate.kind='episode' AND candidate.cinemeta_id=e.value->>'id'
 AND COALESCE((cw.state->>'played')::boolean,false))
 ORDER BY (e.value->>'season')::int,(e.value->>'number')::int,e.value->>'id' LIMIT 1) ep ON true
 LEFT JOIN catalog_media c ON c.kind='episode' AND c.cinemeta_id=ep.value->>'id'
 LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=$1 LEFT JOIN local_files l ON l.movie_id=c.id
 WHERE (ep.value IS NOT NULL OR sh.title_id IS NULL) AND (a.at,a.title_id)<($2::timestamptz,$3::text)
 ORDER BY a.at DESC,a.title_id DESC LIMIT 51`
		if row == "new-releases" {
			// Filter the same Next Up representative before applying LIMIT/cursor.
			// Undated flags use TED account creation, never title intake or poll time.
			query = strings.Replace(query, "WHERE (ep.value IS NOT NULL OR sh.title_id IS NULL)",
				"WHERE ep.value IS NOT NULL AND (ep.value->>'release_date')::date>(a.watch_at AT TIME ZONE $5)::date", 1)
		}
	}
	args := []any{user, cursor.At, cursor.Key}
	if row == "next-up" || row == "new-releases" {
		args = append(args, today)
	}
	if row == "new-releases" {
		loc := time.UTC
		if len(calendar) > 0 && calendar[0] != nil {
			loc = calendar[0]
		}
		args = append(args, loc.String())
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return NativeRowPage{}, err
	}
	defer rows.Close()
	result := NativeRowPage{Items: []NativeRow{}}
	for rows.Next() {
		var item NativeRow
		var state State
		var revision int64
		var duration *int64
		var episode []byte
		if err = rows.Scan(&item.Key, &item.Media.Kind, &item.Media.ID, &item.Metadata, &item.SeriesMetadata, &episode, &state, &revision, &duration, &item.Ready, &item.Local, &item.At, &item.MissingSchedule); err != nil {
			return NativeRowPage{}, err
		}
		if len(episode) > 0 && string(episode) != "null" {
			if err = json.Unmarshal(episode, &item.Episode); err != nil {
				return NativeRowPage{}, err
			}
		}
		item.State = nativeState(item.Media, revision, state, duration)
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return NativeRowPage{}, err
	}
	if len(result.Items) > 50 {
		last := result.Items[49]
		data, err := json.Marshal(nativeCursor{Row: row, At: last.At, Key: last.Key})
		if err != nil {
			return NativeRowPage{}, err
		}
		result.Next = base64.RawURLEncoding.EncodeToString(data)
		result.Items = result.Items[:50]
	}
	return result, nil
}

func NativeParentID(media catalog.MediaIdentity) string { return strings.SplitN(media.ID, ":", 2)[0] }
