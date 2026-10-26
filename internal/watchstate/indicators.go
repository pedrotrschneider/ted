package watchstate

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"strconv"
)

// IndicatorWatch is a read-only projection, never a mutable watch-state record.
type IndicatorWatch struct {
	Seen      bool     `json:"seen"`
	Watched   int      `json:"watched"`
	Available int      `json:"available"`
	Progress  *float64 `json:"progress"`
}
type UserIndicator struct {
	Rating        *int           `json:"rating"`
	AccountID     int64          `json:"account_id"`
	Name          string         `json:"name"`
	AvatarVersion string         `json:"avatar_version,omitempty"`
	Watch         IndicatorWatch `json:"watch"`
}

// Joint hosts are represented only by the viewer's existing direct joint badges.
// Each other person's sources exclude joint history already represented for the
// viewer. UNION and per-media aggregation prevent double-counting an episode.
const indicatorPeopleSQL = `people AS (
 SELECT u.id,u.display_name,u.avatar_version FROM users u WHERE NOT u.disabled AND u.id<>$1
 AND NOT EXISTS(SELECT 1 FROM joint_account_members j WHERE j.joint_id=u.id)
), sources AS (
 SELECT id AS account_id,id AS source_id FROM people
 UNION SELECT p.id,j.joint_id FROM people p JOIN joint_account_members j ON j.member_id=p.id
 JOIN users host ON host.id=j.joint_id AND NOT host.disabled
 WHERE j.joint_id<>$1 AND NOT EXISTS(SELECT 1 FROM joint_account_members shared WHERE shared.joint_id=j.joint_id AND shared.member_id=$1)
)`

func (s *Store) OtherIndicators(ctx context.Context, viewer int64, media []catalog.MediaIdentity, today string) (map[string][]UserIndicator, error) {
	keys, kinds, ids, seasons := []string{}, []string{}, []string{}, []int{}
	seen := map[string]bool{}
	for _, m := range media {
		if !m.Valid() && !(m.Kind == "series" && cinemeta.ValidID(m.ID)) {
			return nil, ErrInvalid
		}
		key := m.Kind + "/" + m.ID
		if !seen[key] {
			keys = append(keys, key)
			kinds = append(kinds, m.Kind)
			ids = append(ids, m.ID)
			seasons = append(seasons, -1)
			seen[key] = true
		}
	}
	return s.otherIndicators(ctx, viewer, keys, kinds, ids, seasons, today)
}
func (s *Store) OtherSeasonIndicators(ctx context.Context, viewer int64, series string, season *int, today string) (map[string][]UserIndicator, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT e.season FROM catalog_episode_schedules sh CROSS JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(season int,release_date date) WHERE sh.title_id=$1 AND e.release_date<=$2::date AND ($3::int IS NULL OR e.season=$3) ORDER BY e.season`, series, today, season)
	if err != nil {
		return nil, err
	}
	keys, kinds, ids, seasons := []string{}, []string{}, []string{}, []int{}
	for rows.Next() {
		var n int
		if err = rows.Scan(&n); err != nil {
			break
		}
		keys = append(keys, strconv.Itoa(n))
		kinds = append(kinds, "season")
		ids = append(ids, series)
		seasons = append(seasons, n)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return s.otherIndicators(ctx, viewer, keys, kinds, ids, seasons, today)
}
func (s *Store) otherIndicators(ctx context.Context, viewer int64, keys, kinds, ids []string, seasons []int, today string) (map[string][]UserIndicator, error) {
	out := map[string][]UserIndicator{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `WITH `+indicatorPeopleSQL+`, targets AS (
 SELECT * FROM unnest($2::text[],$3::text[],$4::text[],$5::int[]) AS t(key,kind,id,season)
 ), per_media AS (
 SELECT src.account_id,t.key,c.cinemeta_id,
 bool_or(COALESCE((wc.state->>'played')::bool,false)) AS played,
 bool_or(COALESCE((wc.state->>'played')::bool,false) OR COALESCE((wc.state->>'position_ticks')::bigint,0)>0 OR COALESCE((wc.state->>'play_count')::int,0)>0
 OR EXISTS(SELECT 1 FROM watch_history h WHERE h.user_id=src.source_id AND h.media_id=c.id AND (COALESCE((h.state->>'played')::bool,false) OR COALESCE((h.state->>'position_ticks')::bigint,0)>0 OR COALESCE((h.state->>'play_count')::int,0)>0))) AS seen,
 max(CASE WHEN wc.duration_ms>0 AND NOT COALESCE((wc.state->>'played')::bool,false) AND COALESCE((wc.state->>'position_ticks')::bigint,0)>0 THEN LEAST(100.0,(wc.state->>'position_ticks')::numeric/10000/wc.duration_ms*100) END)::float8 AS progress
 FROM targets t JOIN catalog_media c ON
 (t.kind IN ('movie','episode') AND c.kind=t.kind AND c.cinemeta_id=t.id) OR
 (t.kind IN ('series','season') AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=t.id AND (t.kind='series' OR ted_episode_season(c.cinemeta_id)=t.season))
 CROSS JOIN sources src LEFT JOIN watch_current wc ON wc.media_id=c.id AND wc.user_id=src.source_id
 GROUP BY src.account_id,t.key,c.cinemeta_id
 ) SELECT t.key,p.id,p.display_name,p.avatar_version,bool_or(m.seen),
 count(*) FILTER(WHERE m.played AND (t.kind IN ('movie','episode') OR EXISTS(
 SELECT 1 FROM catalog_episode_schedules sh CROSS JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(id text,season int,release_date date)
 WHERE sh.title_id=t.id AND e.id=m.cinemeta_id AND e.release_date<=$6::date AND ((t.kind='series' AND e.season>0) OR (t.kind='season' AND e.season=t.season)))))::int,
 CASE WHEN t.kind IN ('movie','episode') THEN 1 ELSE (SELECT count(DISTINCT e.id)::int FROM catalog_episode_schedules sh
 CROSS JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(id text,season int,release_date date)
 WHERE sh.title_id=t.id AND e.release_date<=$6::date AND ((t.kind='series' AND e.season>0) OR (t.kind='season' AND e.season=t.season))) END,
 CASE WHEN t.kind IN ('movie','episode') AND NOT bool_or(m.played) THEN max(m.progress) END,
 CASE WHEN t.kind IN ('movie','series') THEN COALESCE(
 (SELECT rating FROM account_title_preferences pref WHERE pref.user_id=p.id AND pref.kind=t.kind AND pref.title_id=t.id),
 (SELECT max(pref.rating) FROM sources src JOIN account_title_preferences pref ON pref.user_id=src.source_id AND pref.kind=t.kind AND pref.title_id=t.id WHERE src.account_id=p.id AND src.source_id<>p.id)) END
 FROM per_media m JOIN people p ON p.id=m.account_id JOIN targets t ON t.key=m.key
 GROUP BY t.key,t.kind,t.id,t.season,p.id,p.display_name,p.avatar_version HAVING bool_or(m.seen)
 ORDER BY lower(p.display_name),p.id`, viewer, keys, kinds, ids, seasons, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var v UserIndicator
		if err = rows.Scan(&key, &v.AccountID, &v.Name, &v.AvatarVersion, &v.Watch.Seen, &v.Watch.Watched, &v.Watch.Available, &v.Watch.Progress, &v.Rating); err != nil {
			return nil, err
		}
		out[key] = append(out[key], v)
	}
	return out, rows.Err()
}
