package metadata

import (
	"context"
	"encoding/json"
	"errors"

	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
)

var ErrProviderChanged = errors.New("series metadata provider changed")

type SeriesProvider struct {
	Provider  string          `json:"provider"`
	Revision  int64           `json:"revision"`
	Overrides SeriesOverrides `json:"overrides"`
}

func (s *Service) SeriesProvider(ctx context.Context, title string) (SeriesProvider, error) {
	if !titleID.MatchString(title) {
		return SeriesProvider{}, ErrInvalid
	}
	v := SeriesProvider{Provider: "Tvdb"}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT provider,revision FROM metadata_series_providers WHERE title_id=$1`, title).Scan(&v.Provider, &v.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return v, err
	}
	v.Overrides, err = seriesOverrides(ctx, tx, title)
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

// All metadata admissions and provider changes serialize on the same durable,
// title-scoped lock. It covers uncached titles too, without a sentinel entity.
func lockSeriesProvider(ctx context.Context, tx pgx.Tx, title string) (string, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('metadata:series:'||$1,0))`, title); err != nil {
		return "", err
	}
	var provider string
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT provider FROM metadata_series_providers WHERE title_id=$1),'Tvdb')`, title).Scan(&provider)
	return provider, err
}

// ChangeSeriesProvider records the shared choice without provider network reads.
// Only the current series screen is queued; child enrichment stays lazy.
// Existing entities keep their stable TED IDs; generations fence old workers.
func (s *Service) ChangeSeriesProvider(ctx context.Context, user int64, show cinemeta.Series, expected, provider string, decision OverrideDecision) (Snapshot, error) {
	if user < 1 || !titleID.MatchString(show.ID) || show.Type != "series" || (provider != "Tmdb" && provider != "Tvdb") || (expected != "Tmdb" && expected != "Tvdb") || provider == expected || len(show.Episodes) > 5000 {
		return Snapshot{}, ErrInvalid
	}
	// Do not perform provider reads for an account that cannot administer TED.
	var allowed bool
	if err := s.pool.QueryRow(ctx, `SELECT role='admin' AND NOT disabled FROM users WHERE id=$1`, user).Scan(&allowed); err != nil {
		return Snapshot{}, err
	}
	if !allowed {
		return Snapshot{}, ErrArtworkForbidden
	}
	enabled, err := s.Configured(ctx, provider)
	if err != nil {
		return Snapshot{}, err
	}
	if !enabled {
		return Snapshot{}, ErrUnconfigured
	}
	r := Request{Kind: "series", TitleID: show.ID}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockArtworkEditor(ctx, tx, user); err != nil {
		return Snapshot{}, err
	}
	current, err := lockSeriesProvider(ctx, tx, show.ID)
	if err != nil {
		return Snapshot{}, err
	}
	if current != expected {
		return Snapshot{}, ErrProviderChanged
	}
	root, err := s.insert(ctx, tx, current, r, CinemetaSeed(show.Movie), "")
	if err != nil {
		return Snapshot{}, err
	}
	var oldProvider, oldID string
	if err = tx.QueryRow(ctx, `SELECT provider,provider_id FROM metadata_entities WHERE id=$1 FOR UPDATE`, root).Scan(&oldProvider, &oldID); err != nil {
		return Snapshot{}, err
	}
	overrides, err := seriesOverrides(ctx, tx, show.ID)
	if err != nil {
		return Snapshot{}, err
	}
	if err = decision.validate(overrides); err != nil {
		return Snapshot{}, err
	}
	ids := map[string]string{}
	if oldID != "" {
		ids[oldProvider] = oldID
	}
	rawIDs, _ := json.Marshal(ids)
	if _, err = tx.Exec(ctx, `INSERT INTO metadata_series_providers(title_id,provider,provider_ids,changed_by) VALUES($1,$2,$3,$4)
 ON CONFLICT(title_id) DO UPDATE SET provider=EXCLUDED.provider,provider_ids=metadata_series_providers.provider_ids||EXCLUDED.provider_ids,revision=metadata_series_providers.revision+1,changed_by=EXCLUDED.changed_by,changed_at=now()`, show.ID, provider, rawIDs, user); err != nil {
		return Snapshot{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,request,item FROM metadata_entities WHERE request->>'title_id'=$1 AND kind IN ('series','season','episode') ORDER BY id FOR UPDATE`, show.ID)
	if err != nil {
		return Snapshot{}, err
	}
	type reset struct {
		id  string
		req Request
		old Item
	}
	var targets []reset
	for rows.Next() {
		var v reset
		if err = rows.Scan(&v.id, &v.req, &v.old); err != nil {
			break
		}
		targets = append(targets, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return Snapshot{}, err
	}
	for _, v := range targets {
		req := Request{Kind: v.req.Kind, TitleID: show.ID, Season: v.req.Season, Number: v.req.Number, EpisodeName: v.req.EpisodeName, EpisodeDate: v.req.EpisodeDate}
		if v.req.Kind == "episode" {
			req.VideoID = v.req.VideoID
		}
		item := seed(req)
		if req.Kind == "series" {
			item = CinemetaSeed(show.Movie)
		}
		item.ID, item.ParentID, item.SeriesID, item.SeasonID = v.old.ID, v.old.ParentID, v.old.SeriesID, v.old.SeasonID
		item.IndexNumber, item.ParentIndexNumber = v.old.IndexNumber, v.old.ParentIndexNumber
		if decision.Action != "remove" {
			item.LockData, item.LockedFields = v.old.LockData, v.old.LockedFields
		}
		// Retain manual values only after the administrator explicitly chose to keep them.
		base, _ := json.Marshal(item)
		old, _ := json.Marshal(v.old)
		var fresh, retained map[string]json.RawMessage
		_ = json.Unmarshal(base, &fresh)
		_ = json.Unmarshal(old, &retained)
		if decision.Action != "remove" && v.old.LockData != nil && *v.old.LockData {
			fresh = retained
		} else if decision.Action != "remove" {
			for _, field := range v.old.LockedFields {
				if field == "Cast" {
					field = "People"
				}
				if field == "Runtime" {
					field = "RunTimeTicks"
				}
				if value, ok := retained[field]; ok {
					fresh[field] = value
				}
			}
		}
		base, _ = json.Marshal(fresh)
		request, _ := json.Marshal(req)
		if _, err = tx.Exec(ctx, `UPDATE metadata_entities SET provider=$2,provider_id='',item=$3,request=$4,field_sources='{}',status='new',generation=generation+1,applied_generation=0,fetched_at=NULL,error_code='' WHERE id=$1`, v.id, provider, base, request); err != nil {
			return Snapshot{}, err
		}
		if v.req.Kind == "series" {
			if err = s.queueEntity(ctx, tx, v.id, false, true); err != nil {
				return Snapshot{}, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE metadata_artwork SET user_modified=false,source_url='',digest='',content_type='',bytes=0,status='new',error_code='',generation=generation+1 WHERE (NOT user_modified OR $2) AND entity_id IN (SELECT id FROM metadata_entities WHERE request->>'title_id'=$1)`, show.ID, decision.Action == "remove"); err != nil {
		return Snapshot{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE metadata_artwork_choices SET images='[]',status='new',generation=generation+1,fetched_at=NULL,error_code='' WHERE entity_id IN (SELECT id FROM metadata_entities WHERE request->>'title_id'=$1)`, show.ID); err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx, root)
}
