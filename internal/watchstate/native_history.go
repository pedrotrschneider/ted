package watchstate

import (
	"context"
	"time"

	"entertainmentdistrict/internal/catalog"
)

type NativeHistoryEntry struct {
	ID         int64                 `json:"id"`
	Media      catalog.MediaIdentity `json:"media"`
	Kind       string                `json:"kind"`
	State      NativeState           `json:"state"`
	ObservedAt time.Time             `json:"observed_at"`
	Origin     string                `json:"origin"`
	DeviceID   *string               `json:"device_id"`
	SessionID  *string               `json:"session_id"`
}

type NativeHistory struct {
	Entries    []NativeHistoryEntry `json:"entries"`
	NextBefore *int64               `json:"next_before,omitempty"`
}

func (s *Store) NativeHistory(ctx context.Context, user, before int64) (NativeHistory, error) {
	if before < 0 {
		return NativeHistory{}, ErrInvalid
	}
	out := NativeHistory{Entries: []NativeHistoryEntry{}}
	rows, err := s.pool.Query(ctx, `SELECT h.id,c.kind,c.cinemeta_id,h.kind,h.state,h.observed_at,COALESCE(e.envelope->>'origin','integration_or_manual'),p.device_id,p.id::text,p.duration_ms
 FROM watch_history h JOIN catalog_media c ON c.id=h.media_id
 LEFT JOIN native_watch_events e ON e.id=h.native_event_id LEFT JOIN native_playback_sessions p ON p.id=e.session_id
 WHERE h.user_id=$1 AND ($2::bigint=0 OR h.id<$2) ORDER BY h.id DESC LIMIT 51`, user, before)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry NativeHistoryEntry
		var state State
		var duration *int64
		if err = rows.Scan(&entry.ID, &entry.Media.Kind, &entry.Media.ID, &entry.Kind, &state, &entry.ObservedAt, &entry.Origin, &entry.DeviceID, &entry.SessionID, &duration); err != nil {
			return out, err
		}
		entry.State = nativeState(entry.Media, 0, state, duration)
		out.Entries = append(out.Entries, entry)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Entries) > 50 {
		out.Entries = out.Entries[:50]
		out.NextBefore = &out.Entries[49].ID
	}
	return out, nil
}
