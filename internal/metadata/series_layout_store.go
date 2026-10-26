package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
)

func (s *Service) applySeriesStructure(ctx context.Context, tx pgx.Tx, v entity, layout *cinemeta.Series, canonical []cinemeta.Episode, children []Child) error {
	if layout.ID != v.Request.TitleID || layout.Type != "series" || len(layout.Episodes) > 5000 {
		return ErrAmbiguous
	}
	raw, err := json.Marshal(layout.Episodes)
	if err != nil {
		return err
	}
	original, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	var same bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE(canonical_episodes,episodes)=$2::jsonb FROM catalog_episode_schedules WHERE title_id=$1 FOR UPDATE`, layout.ID, original).Scan(&same); err != nil {
		return err
	}
	if !same {
		return ErrPartial
	}
	if _, err = tx.Exec(ctx, `INSERT INTO metadata_series_layouts(title_id,provider,generation,episodes) VALUES($1,$2,$3,$4) ON CONFLICT(title_id) DO UPDATE SET provider=EXCLUDED.provider,generation=EXCLUDED.generation,episodes=EXCLUDED.episodes`, layout.ID, v.Provider, v.Generation, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM metadata_episode_layout WHERE title_id=$1`, layout.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE catalog_episode_schedules SET episodes=$2,fetched_at=now() WHERE title_id=$1`, layout.ID, raw); err != nil {
		return err
	}
	seasons := map[int]string{}
	seasonIDs := map[int]string{}
	for _, child := range children {
		if child.Request.Kind == "season" {
			seasonIDs[child.Request.Season] = child.Request.ProviderID
		}
	}
	for _, ep := range layout.Episodes {
		if !cinemeta.ValidVideoID(ep.ID) || !strings.HasPrefix(ep.ID, layout.ID+":") || ep.Season < 0 || ep.Number < 0 {
			return ErrAmbiguous
		}
		if _, err = tx.Exec(ctx, `INSERT INTO metadata_episode_layout(episode_id,title_id,season,number) VALUES($1,$2,$3,$4)`, ep.ID, layout.ID, ep.Season, ep.Number); err != nil {
			return err
		}
		parent := seasons[ep.Season]
		if parent == "" {
			r := Request{Kind: "season", TitleID: layout.ID, Season: ep.Season, ProviderID: seasonIDs[ep.Season], ParentProviderID: v.ProviderID}
			parent, err = s.seedLayoutEntity(ctx, tx, v.Provider, r, v.ID)
			if err != nil {
				return err
			}
			seasons[ep.Season] = parent
		}
		providerID := fmt.Sprint(ep.TVDBID)
		if v.Provider == "Tmdb" {
			providerID = fmt.Sprint(ep.TMDBID)
		}
		if !positiveID.MatchString(providerID) {
			return ErrAmbiguous
		}
		r := Request{Kind: "episode", TitleID: layout.ID, VideoID: ep.ID, Season: ep.Season, Number: ep.Number, ProviderID: providerID, ParentProviderID: v.ProviderID, EpisodeName: ep.Name, EpisodeDate: ep.ReleaseDate}
		if _, err = s.seedLayoutEntity(ctx, tx, v.Provider, r, parent); err != nil {
			return err
		}
	}
	return nil
}

// Seeding the verified identity index must not fetch child metadata or reset
// already-enriched children when a root/schedule refresh leaves them unchanged.
func (s *Service) seedLayoutEntity(ctx context.Context, tx pgx.Tx, provider string, r Request, parent string) (string, error) {
	id, err := s.insert(ctx, tx, provider, r, seed(r), parent)
	if err != nil {
		return "", err
	}
	var old Request
	var oldProvider, oldID string
	if err = tx.QueryRow(ctx, `SELECT provider,provider_id,request FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&oldProvider, &oldID, &old); err != nil {
		return "", err
	}
	changed := oldProvider != provider || oldID != r.ProviderID || old.ParentProviderID != r.ParentProviderID || old.Season != r.Season || old.Number != r.Number
	raw, _ := json.Marshal(r)
	index := r.Season
	if r.Kind == "episode" {
		index = r.Number
	}
	// The new seed contains no cast-admission flags. Preserve those on an
	// unchanged entity; replace them when its provider identity changes.
	_, err = tx.Exec(ctx, `UPDATE metadata_entities SET provider=$2,provider_id=$3,parent_id=$4::uuid,
 request=CASE WHEN $6 THEN $5::jsonb ELSE request||$5::jsonb END,
 status=CASE WHEN $6 THEN 'new' ELSE status END,
 generation=generation+CASE WHEN $6 THEN 1 ELSE 0 END,
 applied_generation=CASE WHEN $6 THEN 0 ELSE applied_generation END,
 fetched_at=CASE WHEN $6 THEN NULL ELSE fetched_at END,
 error_code=CASE WHEN $6 THEN '' ELSE error_code END,
 item=item||jsonb_build_object('IndexNumber',$7::int,'ParentId',$4::text)||
 CASE WHEN kind='episode' THEN jsonb_build_object('ParentIndexNumber',$8::int,'SeasonId',$4::text) ELSE '{}'::jsonb END
 WHERE id=$1`, id, provider, r.ProviderID, parent, raw, changed, index, r.Season)
	return id, err
}

// Reuse only previously verified external identities for the same stable TED
// episode. Descriptions/numbering remain fresh provider data, never hints.
func (s *Service) addSeriesIdentityHints(ctx context.Context, canonical *cinemeta.Series) error {
	var previous []cinemeta.Episode
	err := s.pool.QueryRow(ctx, `SELECT episodes FROM metadata_series_layouts WHERE title_id=$1`, canonical.ID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	byID := map[string]cinemeta.Episode{}
	for _, ep := range previous {
		byID[ep.ID] = ep
	}
	for i, ep := range canonical.Episodes {
		hint, ok := byID[ep.ID]
		if !ok || ep.TVDBID > 0 && hint.TVDBID > 0 && ep.TVDBID != hint.TVDBID || ep.TMDBID > 0 && hint.TMDBID > 0 && ep.TMDBID != hint.TMDBID {
			continue
		}
		if !(ep.TVDBID > 0 && ep.TVDBID == hint.TVDBID || ep.TMDBID > 0 && ep.TMDBID == hint.TMDBID) {
			continue
		}
		if ep.TVDBID == 0 {
			canonical.Episodes[i].TVDBID = hint.TVDBID
		}
		if ep.TMDBID == 0 {
			canonical.Episodes[i].TMDBID = hint.TMDBID
		}
	}
	return nil
}
