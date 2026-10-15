package watchstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"

	"github.com/jackc/pgx/v5"
)

type TitleItem struct {
	Writable  bool   `json:"writable"`
	ID        string `json:"id"`
	Media     string `json:"-"`
	Played    bool   `json:"played"`
	Available bool   `json:"available"`
	Pending   bool   `json:"pending"`
	Error     string `json:"error"`
	Revision  int64  `json:"revision"`
	State     State  `json:"-"`
}
type TitleState struct {
	Items   []TitleItem `json:"items"`
	Version string      `json:"version"`
}

func titleState(ctx context.Context, tx pgx.Tx, user int64, id string, all ...bool) (TitleState, error) {
	out := TitleState{Items: []TitleItem{}}
	rows, err := tx.Query(ctx, `SELECT c.cinemeta_id,c.id::text,COALESCE(w.state,'{"played":false,"position_ticks":0,"play_count":0,"last_played":null}'),COALESCE(w.revision,0),COALESCE(s.published,false),true,false,''
 FROM catalog_media c
 LEFT JOIN streaming_items s ON s.movie_id=c.id
 LEFT JOIN watch_current w ON w.media_id=c.id AND w.user_id=$1
 WHERE ((c.cinemeta_id=$2 AND c.kind IN ('movie','episode')) OR (c.kind='episode' AND split_part(c.cinemeta_id,':',1)=$2))
 AND ($3 OR NOT EXISTS(SELECT 1 FROM watch_title_catalog WHERE title_id=$2) OR EXISTS(SELECT 1 FROM watch_title_catalog WHERE title_id=$2 AND entries ? c.cinemeta_id))
 ORDER BY c.cinemeta_id`, user, id, len(all) > 0 && all[0])
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item TitleItem
		if err = rows.Scan(&item.ID, &item.Media, &item.State, &item.Revision, &item.Available, &item.Writable, &item.Pending, &item.Error); err != nil {
			return out, err
		}
		item.Played = item.State.Played
		out.Items = append(out.Items, item)
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
func (s *Store) Title(ctx context.Context, who accounts.Identity, id string) (TitleState, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return TitleState{}, err
	}
	defer database.Rollback(tx)
	if err = authorize(ctx, tx, who, false); err != nil {
		return TitleState{}, err
	}
	out, err := titleState(ctx, tx, who.ID, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// SetTitle applies the reviewed scope atomically and queues Stremio delivery.
func (s *Store) SetTitle(ctx context.Context, who accounts.Identity, id, version, episode string, season *int, played bool, dateMode string, forgetHistory ...bool) error {
	forget := len(forgetHistory) > 0 && forgetHistory[0]
	if forget && (played || episode != "" || season != nil || dateMode != "" || strings.Contains(id, ":")) {
		return ErrInvalid
	}
	if !validDateMode(played, dateMode) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, accounts.MutationLockID); err != nil {
		return err
	}
	if err = authorize(ctx, tx, who, false); err != nil {
		return err
	}
	// Lock saved rows against concurrent Stremio imports as well as native edits.
	rows, err := tx.Query(ctx, `SELECT w.media_id FROM watch_current w JOIN catalog_media c ON c.id=w.media_id WHERE w.user_id=$1 AND (c.cinemeta_id=$2 OR (c.kind='episode' AND split_part(c.cinemeta_id,':',1)=$2)) ORDER BY w.media_id FOR UPDATE OF w`, who.ID, id)
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
	state, err := titleState(ctx, tx, who.ID, id)
	if err != nil {
		return err
	}
	if state.Version != version {
		return ErrConflict
	}
	if forget {
		state, err = titleState(ctx, tx, who.ID, id, true)
		if err != nil {
			return err
		}
	}
	selected := 0
	for _, item := range state.Items {
		if episode != "" && item.ID != episode {
			continue
		}
		if season != nil {
			parts := strings.Split(item.ID, ":")
			if len(parts) != 3 || parts[1] != strconv.Itoa(*season) {
				continue
			}
		}
		if item.Pending {
			return ErrConflict
		}

		selected++
		if played && item.Played {
			continue
		}
		action := "unwatched"
		if played {
			action = "watched"
		}
		if err = s.saveLocalDecision(ctx, tx, who.ID, item, action, dateMode, forget); err != nil {
			return err
		}

	}
	if selected == 0 {
		return ErrUnavailable
	}
	return tx.Commit(ctx)
}

func (s *Store) saveLocalDecision(ctx context.Context, tx pgx.Tx, user int64, item TitleItem, action, dateMode string, forget bool) error {
	desired := editedState(item.State, action, dateMode)
	raw, err := json.Marshal(desired)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO watch_current(user_id,media_id,state,source,observed_at) VALUES($1,$2,$3,'TEDLocal',now()) ON CONFLICT(user_id,media_id) DO UPDATE SET state=EXCLUDED.state,source='TEDLocal',revision=watch_current.revision+1,needs_review=false,observed_at=now(),updated_at=now()`, user, item.Media, raw)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO watch_history(user_id,media_id,kind,state,observed_at) VALUES($1,$2,$3,$4,now())`, user, item.Media, "ted_"+action, raw); err != nil {
		return err
	}
	if forget {
		if err = forgetHistory(ctx, tx, user, item.Media); err != nil {
			return err
		}
	}
	if action != "reset" {
		if err = queueStremio(ctx, tx, user, item.Media, desired.Played); err != nil {
			return err
		}
	}
	return nil
}

// Remove user-visible viewing evidence after accepting the local decision,
// retaining an explicit current-state tombstone for old imports.
func forgetHistory(ctx context.Context, tx pgx.Tx, user int64, media string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM watch_history WHERE user_id=$1 AND media_id=$2`, user, media); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM stremio_episode_recency WHERE user_id=$1 AND media_id=$2`, user, media); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE watch_current SET source='TEDForget' WHERE user_id=$1 AND media_id=$2`, user, media)
	return err
}
