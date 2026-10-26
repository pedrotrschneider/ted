package httpapi

import (
	"context"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/titleprefs"
	"time"
)

type jointWatch struct {
	AccountID int64       `json:"account_id"`
	Name      string      `json:"name"`
	Color     string      `json:"color"`
	Watch     posterWatch `json:"watch"`
	Rating    *int        `json:"rating"`
}

// Members may read only their own direct joint accounts. A joint login does not
// inherit its members' state. These projections never merge personal state.
func (a *API) jointWatches(ctx context.Context, user int64, media []catalog.MediaIdentity) (map[string][]jointWatch, error) {
	out := map[string][]jointWatch{}
	unique := make([]catalog.MediaIdentity, 0, len(media))
	seen := map[string]bool{}
	for _, m := range media {
		key := m.Kind + "/" + m.ID
		if !seen[key] {
			seen[key] = true
			unique = append(unique, m)
		}
	}
	media = unique
	rows, err := a.pool.Query(ctx, `SELECT j.joint_id,u.display_name,j.color FROM joint_account_members j JOIN users u ON u.id=j.joint_id WHERE j.member_id=$1 AND NOT u.disabled ORDER BY j.created_at,j.joint_id`, user)
	if err != nil {
		return nil, err
	}
	people := []jointWatch{}
	for rows.Next() {
		var p jointWatch
		if err = rows.Scan(&p.AccountID, &p.Name, &p.Color); err != nil {
			break
		}
		people = append(people, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	ids, shows := []string{}, []string{}
	for _, m := range media {
		ids = append(ids, m.ID)
		if m.Kind == "series" {
			shows = append(shows, m.ID)
		}
	}
	for _, person := range people {
		prefs, e := titleprefs.New(a.pool).States(ctx, person.AccountID, media)
		if e != nil {
			return nil, e
		}
		counts, e := a.catalog.ReleasedCounts(ctx, shows, person.AccountID, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
		if e != nil {
			return nil, e
		}
		states := map[string]posterWatch{}
		rows, e := a.pool.Query(ctx, `SELECT t.id,COALESCE(bool_or(COALESCE((wc.state->>'played')::bool,false) OR COALESCE((wc.state->>'position_ticks')::bigint,0)>0 OR COALESCE((wc.state->>'play_count')::int,0)>0 OR EXISTS(SELECT 1 FROM watch_history h WHERE h.user_id=$2 AND h.media_id=c.id AND (COALESCE((h.state->>'played')::bool,false) OR COALESCE((h.state->>'position_ticks')::bigint,0)>0 OR COALESCE((h.state->>'play_count')::int,0)>0))),false),
  count(*) FILTER(WHERE COALESCE((wc.state->>'played')::bool,false))::int,
  max(CASE WHEN wc.duration_ms>0 AND NOT COALESCE((wc.state->>'played')::bool,false) AND COALESCE((wc.state->>'position_ticks')::bigint,0)>0 THEN LEAST(100.0,(wc.state->>'position_ticks')::numeric/10000/wc.duration_ms*100) END)::float8
  FROM unnest($1::text[]) t(id) LEFT JOIN catalog_media c ON c.cinemeta_id=t.id OR (t.id=ANY($3::text[]) AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=t.id AND ted_episode_season(c.cinemeta_id)>0)
  LEFT JOIN watch_current wc ON wc.media_id=c.id AND wc.user_id=$2 GROUP BY t.id`, ids, person.AccountID, shows)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			var w posterWatch
			if e = rows.Scan(&id, &w.Seen, &w.Watched, &w.Progress); e != nil {
				break
			}
			w.Available = 1
			if c, ok := counts[id]; ok {
				w.Watched = c.Watched
				w.Available = c.Available
				w.Progress = nil
			}
			states[id] = w
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, m := range media {
			v := person
			v.Watch = states[m.ID]
			if v.Watch.Seen {
				v.Rating = prefs[m.Kind+"/"+m.ID].Rating
			}
			out[m.Kind+"/"+m.ID] = append(out[m.Kind+"/"+m.ID], v)
		}
	}
	return out, nil
}
func (a *API) decorateJointWatch(ctx context.Context, user int64, titles []nativeTitleDetail) error {
	media := make([]catalog.MediaIdentity, len(titles))
	for i := range titles {
		media[i] = titles[i].Media
	}
	prefs, err := titleprefs.New(a.pool).States(ctx, user, media)
	if err != nil {
		return err
	}
	values, err := a.jointWatches(ctx, user, media)
	if err != nil {
		return err
	}
	other, err := a.watch.OtherIndicators(ctx, user, media, time.Now().In(a.CalendarLocation).Format(time.DateOnly))
	if err != nil {
		return err
	}
	for i := range titles {
		state := prefs[titles[i].Media.Kind+"/"+titles[i].Media.ID]
		titles[i].Watchlist, titles[i].UserRating = state.Watchlist, state.Rating
		titles[i].UserWatches = other[titles[i].Media.Kind+"/"+titles[i].Media.ID]
		titles[i].JointWatches = values[titles[i].Media.Kind+"/"+titles[i].Media.ID]
	}
	return nil
}

func (a *API) jointSeasonWatches(ctx context.Context, user int64, show string, season *int) (map[string][]jointWatch, error) {
	out := map[string][]jointWatch{}
	rows, err := a.pool.Query(ctx, `SELECT j.joint_id,u.display_name,j.color,e.season::text,
 count(*) FILTER(WHERE COALESCE((w.state->>'played')::bool,false))::int,count(*)::int,
 COALESCE(bool_or(COALESCE((w.state->>'played')::bool,false) OR COALESCE((w.state->>'position_ticks')::bigint,0)>0),false)
 FROM joint_account_members j JOIN users u ON u.id=j.joint_id AND NOT u.disabled
 JOIN catalog_episode_schedules sh ON sh.title_id=$2
 CROSS JOIN LATERAL jsonb_to_recordset(sh.episodes) AS e(id text,season int,release_date date)
 LEFT JOIN catalog_media c ON c.kind='episode' AND c.cinemeta_id=e.id
 LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=j.joint_id
 WHERE j.member_id=$1 AND e.release_date<=$3::date AND ($4::int IS NULL OR e.season=$4)
 GROUP BY j.joint_id,u.display_name,j.color,e.season,j.created_at ORDER BY j.created_at,j.joint_id`, user, show, time.Now().In(a.CalendarLocation).Format(time.DateOnly), season)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v jointWatch
		var number string
		if err = rows.Scan(&v.AccountID, &v.Name, &v.Color, &number, &v.Watch.Watched, &v.Watch.Available, &v.Watch.Seen); err != nil {
			return nil, err
		}
		out[number] = append(out[number], v)
	}
	return out, rows.Err()
}
