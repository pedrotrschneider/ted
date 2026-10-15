package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"

	"github.com/riverqueue/river"
)

type FilmographyEntry struct {
	Item   Item     `json:"item"`
	Roles  []string `json:"roles"`
	Images []Asset  `json:"images"`
	Media  *struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"media,omitempty"`
	IdentityStatus string `json:"identity_status"`
	Watched        *bool  `json:"watched"`
	Viewed         *bool  `json:"viewed"`
	EpisodeCounts  *struct {
		Watched int `json:"watched"`
		Total   int `json:"total"`
	} `json:"episode_counts,omitempty"`
}
type Filmography struct {
	Items     []FilmographyEntry `json:"items"`
	NextAfter string             `json:"next_after,omitempty"`
}

func (s *Service) Filmography(ctx context.Context, user int64, person, after string) (Filmography, error) {
	result := Filmography{Items: []FilmographyEntry{}}
	if !uuid.MatchString(person) || after != "" && !uuid.MatchString(after) {
		return result, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	// Serialize first-time identifier requests without keeping a read cursor open.
	rows, e := tx.Query(ctx, `SELECT reference_id::text,summary,kind,cinemeta_id,identity_status,roles,provider FROM metadata_credits WHERE person_id=$1 AND ($2='' OR reference_id>NULLIF($2,'')::uuid) ORDER BY reference_id LIMIT 21`, person, after)
	if e != nil {
		return result, e
	}
	type retained struct {
		id, kind, imdb, status, provider string
		item                             Item
		roles                            []string
	}
	entries := []retained{}
	for rows.Next() {
		var v retained
		if e = rows.Scan(&v.id, &v.item, &v.kind, &v.imdb, &v.status, &v.roles, &v.provider); e != nil {
			break
		}
		entries = append(entries, v)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return result, e
	}
	if len(entries) > 20 {
		result.NextAfter = entries[19].id
		entries = entries[:20]
	}
	for _, v := range entries {
		row := FilmographyEntry{Item: v.item, Roles: v.roles, Images: []Asset{}, IdentityStatus: v.status}
		assets, err := tx.Query(ctx, `SELECT id::text,image_type,image_index,status,digest,error_code FROM metadata_artwork WHERE credit_id=$1 ORDER BY image_type,image_index`, v.id)
		if err != nil {
			return result, err
		}
		for assets.Next() {
			var img Asset
			if err = assets.Scan(&img.ID, &img.Type, &img.Index, &img.Status, &img.Tag, &img.Error); err != nil {
				break
			}
			img.URL = "/api/client/v1/metadata/images/" + img.ID
			row.Images = append(row.Images, img)
			if img.Tag != "" {
				row.Item.ImageTags = map[string]string{img.Type: img.Tag}
			}
		}
		if err == nil {
			err = assets.Err()
		}
		assets.Close()
		if err != nil {
			return result, err
		}
		if v.imdb != "" {
			var cachedName *string
			if err = tx.QueryRow(ctx, `SELECT (SELECT NULLIF(item->>'Name','') FROM metadata_entities WHERE request->>'title_id'=$1 AND kind=$2 AND status IN ('ready','partial') AND item->>'PreferredMetadataLanguage' LIKE 'en%' ORDER BY fetched_at DESC LIMIT 1)`, v.imdb, v.kind).Scan(&cachedName); err != nil {
				return result, err
			}
			if cachedName != nil {
				row.Item.Name = *cachedName
			}
			row.Media = &struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			}{v.kind, v.imdb}
			var played, viewed bool
			// Completion and any viewing are separate. Retained legitimate history
			// still counts after a progress reset or an explicit unwatched edit.
			e = tx.QueryRow(ctx, `WITH viewing AS (
 SELECT COALESCE((w.state->>'played')::boolean,false) AS played,
 COALESCE((w.state->>'position_ticks')::bigint,0)>0 OR COALESCE((w.state->>'play_count')::int,0)>0
 OR (w.source='TEDNativePlayback' AND w.state->>'last_played' IS NOT NULL)
 OR EXISTS(SELECT 1 FROM watch_history h LEFT JOIN native_watch_events e ON e.id=h.native_event_id
 WHERE h.user_id=$1 AND h.media_id=m.id AND (h.native_event_id IS NULL OR e.outcome='applied')
 AND (COALESCE((h.state->>'played')::boolean,false) OR COALESCE((h.state->>'position_ticks')::bigint,0)>0
 OR COALESCE((h.state->>'play_count')::int,0)>0 OR h.kind='playback_started')) AS viewed
 FROM catalog_media m LEFT JOIN watch_current w ON w.media_id=m.id AND w.user_id=$1
 WHERE ($2='movie' AND m.kind='movie' AND m.cinemeta_id=$3)
 OR ($2='series' AND m.kind='episode' AND split_part(m.cinemeta_id,':',1)=$3))
 SELECT COALESCE(bool_or(played),false),COALESCE(bool_or(played OR viewed),false) FROM viewing`, user, v.kind, v.imdb).Scan(&played, &viewed)
			if e != nil {
				return result, e
			}
			row.Watched = &played
			row.Viewed = &viewed
		}
		_, canLocalize := s.reader.(creditSummaryReader)
		localize := canLocalize && v.provider == "Tvdb" && v.item.PreferredMetadataLanguage != "en" && (v.status == "ready" || v.status == "missing")
		if v.status == "new" || localize {
			var status string
			var generation int64
			if e = tx.QueryRow(ctx, `SELECT identity_status,generation FROM metadata_credits WHERE reference_id=$1 FOR UPDATE`, v.id).Scan(&status, &generation); e != nil {
				return result, e
			}
			if status == "new" || localize && (status == "ready" || status == "missing") {
				if _, e = s.queue.InsertTx(ctx, tx, creditArgs{v.id, generation}, &river.InsertOpts{Queue: "native_metadata", MaxAttempts: 3}); e != nil {
					return result, e
				}
				if _, e = tx.Exec(ctx, `UPDATE metadata_credits SET identity_status='queued' WHERE reference_id=$1`, v.id); e != nil {
					return result, e
				}
			}
			row.IdentityStatus = "queued"
		}
		result.Items = append(result.Items, row)
	}
	if e = tx.Commit(ctx); e != nil {
		return result, e
	}
	return result, nil
}

type creditWorker struct {
	river.WorkerDefaults[creditArgs]
	s *Service
}

func (w *creditWorker) Work(ctx context.Context, job *river.Job[creditArgs]) error {
	var provider, kind, id, status, language string
	var generation int64
	e := w.s.pool.QueryRow(ctx, `SELECT provider,kind,provider_id,identity_status,generation,COALESCE(summary->>'PreferredMetadataLanguage','') FROM metadata_credits WHERE reference_id=$1`, job.Args.ID).Scan(&provider, &kind, &id, &status, &generation, &language)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if status == "ready" && (provider != "Tvdb" || language == "en") || generation != job.Args.Generation {
		return nil
	}
	var imdb string
	var summary Item
	var resolve error
	if reader, ok := w.s.reader.(creditSummaryReader); ok {
		imdb, summary, resolve = reader.ResolveCreditSummary(ctx, provider, kind, id)
	} else {
		imdb, resolve = w.s.reader.ResolveCredit(ctx, provider, kind, id)
	}
	if resolve == nil && !titleID.MatchString(imdb) {
		resolve = ErrPartial
	}
	state := "ready"
	if resolve != nil {
		state = "failed"
		if resolve == ErrMissing {
			state = "missing"
		}
		imdb = ""
	}
	patch := map[string]string{}
	if (resolve == nil || errors.Is(resolve, ErrMissing)) && summary.Name != "" {
		patch["Name"] = summary.Name
		patch["PreferredMetadataLanguage"] = summary.PreferredMetadataLanguage
	}
	raw, _ := json.Marshal(patch)
	// Retain a previously resolved identity if a summary refresh fails or no
	// external identifier is supplied. A conflicting association is an error.
	result, e := w.s.pool.Exec(ctx, `UPDATE metadata_credits SET cinemeta_id=CASE WHEN $2='' THEN cinemeta_id ELSE $2 END,identity_status=CASE WHEN cinemeta_id<>'' AND $2='' AND $3='missing' THEN 'ready' ELSE $3 END,summary=summary||$5::jsonb WHERE reference_id=$1 AND generation=$4 AND (cinemeta_id='' OR $2='' OR cinemeta_id=$2)`, job.Args.ID, imdb, state, job.Args.Generation, raw)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		return ErrAmbiguous
	}
	if resolve == ErrMissing {
		return river.JobCancel(resolve)
	}
	return resolve
}
