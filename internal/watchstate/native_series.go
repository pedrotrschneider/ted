package watchstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
)

type NativeSeriesWatch struct {
	Version      string            `json:"version"`
	Items        []NativeState     `json:"items"`
	ReleaseDates map[string]string `json:"-"`
}

func nativeSeriesWatch(ctx context.Context, tx pgx.Tx, user int64, id string, season *int) (NativeSeriesWatch, error) {
	if !cinemeta.ValidID(id) || season != nil && (*season < 0 || *season > 9999) {
		return NativeSeriesWatch{}, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT ep.id,COALESCE(ep.release_date,''),COALESCE(w.revision,0),COALESCE(w.state,'{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'::jsonb),w.duration_ms
 FROM catalog_episode_schedules sh CROSS JOIN LATERAL jsonb_to_recordset(sh.episodes) ep(id text,season int,number int,release_date text)
 LEFT JOIN catalog_media c ON c.kind='episode' AND c.cinemeta_id=ep.id
 LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=$1
 WHERE sh.title_id=$2 AND ($3::int IS NULL OR ep.season=$3) ORDER BY ep.season,ep.number,ep.id`, user, id, season)
	if err != nil {
		return NativeSeriesWatch{}, err
	}
	defer rows.Close()
	out := NativeSeriesWatch{Items: []NativeState{}, ReleaseDates: map[string]string{}}
	for rows.Next() {
		var ep, released string
		var rev int64
		var state State
		var duration *int64
		if err = rows.Scan(&ep, &released, &rev, &state, &duration); err != nil {
			return out, err
		}
		out.ReleaseDates[ep] = released
		out.Items = append(out.Items, nativeState(catalog.MediaIdentity{Kind: "episode", ID: ep}, rev, state, duration))
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	raw, err := json.Marshal(out.Items)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(raw)
	out.Version = hex.EncodeToString(sum[:])
	return out, nil
}

func (s *Store) NativeSeriesWatch(ctx context.Context, user int64, id string) (NativeSeriesWatch, error) {
	return s.nativeSeriesWatch(ctx, user, id, nil)
}

func (s *Store) NativeSeasonWatch(ctx context.Context, user int64, id string, season int) (NativeSeriesWatch, error) {
	return s.nativeSeriesWatch(ctx, user, id, &season)
}

func (s *Store) nativeSeriesWatch(ctx context.Context, user int64, id string, season *int) (NativeSeriesWatch, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return NativeSeriesWatch{}, err
	}
	defer database.Rollback(tx)
	out, err := nativeSeriesWatch(ctx, tx, user, id, season)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Review/version and mutation cover the complete canonical schedule, including
// specials. One transaction preserves history and durable Stremio delivery and
// rejects a stale whole-show decision rather than partially applying it.
func (s *Store) EditNativeSeries(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, id, version string, watched bool) error {
	return s.editNativeSeries(ctx, auth, grant, id, nil, version, watched)
}

func (s *Store) EditNativeSeason(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, id string, season int, version string, watched bool) error {
	return s.editNativeSeries(ctx, auth, grant, id, &season, version, watched)
}

func (s *Store) editNativeSeries(ctx context.Context, auth *accounts.NativeAuth, grant accounts.NativeGrant, id string, season *int, version string, watched bool) error {
	if !cinemeta.ValidID(id) || len(version) != 64 || season != nil && (*season < 0 || *season > 9999) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, grant); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT title_id FROM catalog_episode_schedules WHERE title_id=$1 FOR SHARE`, id); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT w.media_id FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=$1 AND c.kind='episode' AND split_part(c.cinemeta_id,':',1)=$2 ORDER BY w.media_id FOR UPDATE OF w`, grant.User.ID, id)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	state, err := nativeSeriesWatch(ctx, tx, grant.User.ID, id, season)
	if err != nil {
		return err
	}
	if state.Version != version {
		return ErrConflict
	}
	if len(state.Items) == 0 {
		return ErrUnavailable
	}
	action := "unwatched"
	if watched {
		action = "watched"
	}
	for _, item := range state.Items {
		if watched && item.Played && item.PositionMS == 0 {
			continue
		}
		if _, err = s.editNativeTx(ctx, tx, grant, NativeEdit{Media: item.Media, Revision: item.Revision, Action: action}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
