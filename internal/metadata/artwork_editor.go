package metadata

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/riverqueue/river"
)

type overrideArgs Args

func (overrideArgs) Kind() string { return "native_artwork_override" }

type overrideWorker struct {
	river.WorkerDefaults[overrideArgs]
	s *Service
}

func (w *overrideWorker) Work(ctx context.Context, job *river.Job[overrideArgs]) error {
	err := w.s.fetchSelectedImage(ctx, Args(job.Args), true)
	if err != nil && ctx.Err() == nil {
		status := "failed"
		if job.MaxAttempts > 0 && job.Attempt < job.MaxAttempts {
			status = "queued"
		}
		if _, e := w.s.pool.Exec(ctx, `UPDATE metadata_artwork SET status=$4,error_code=$3 WHERE id=$1 AND generation=$2 AND user_modified`, job.Args.ID, job.Args.Generation, code(err), status); e != nil {
			return e
		}
	}
	return err
}

type EditableArtwork struct {
	Asset
	EntityID  string `json:"entity_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Provider  string `json:"provider"`
	HasSource bool   `json:"has_source"`
}

func editableImageType(kind, imageType string) bool {
	switch kind {
	case "movie", "series":
		return imageType == "Primary" || imageType == "Logo" || imageType == "Banner" || imageType == "Backdrop"
	case "season":
		return imageType == "Primary"
	}
	return false
}

// One active slot per title image type and one poster per known season. Provider
// alternatives belong to the lazy picker, not the active artwork cache.
func (s *Service) TitleArtwork(ctx context.Context, kind, id string, retry bool) ([]EditableArtwork, error) {
	if (kind != "movie" && kind != "series") || !titleID.MatchString(id) {
		return nil, ErrInvalid
	}
	provider := "Tmdb"
	typ := "Movie"
	if kind == "series" {
		provider = "Tvdb"
		typ = "Series"
	}
	v, err := s.Ensure(ctx, provider, Request{Kind: kind, TitleID: id}, Item{Type: typ})
	if err != nil {
		return nil, err
	}
	if v.FetchedAt == nil {
		pending, err := s.ArtworkMetadataPending(ctx, v)
		if err != nil {
			return nil, err
		}
		if retry && !pending {
			if _, err = s.Refresh(ctx, v.Item.ID); err != nil {
				return nil, err
			}
		}
		return []EditableArtwork{}, nil
	}
	// Empty slots remain editable when the provider has no image of that type.
	for _, imageType := range []string{"Primary", "Logo", "Banner", "Backdrop"} {
		if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url) VALUES($1,$2,0,'') ON CONFLICT DO NOTHING`, v.Item.ID, imageType); err != nil {
			return nil, err
		}
	}
	if kind == "series" {
		if _, err = s.pool.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url)
 SELECT id,'Primary',0,'' FROM metadata_entities WHERE kind='season' AND request->>'title_id'=$1
 ON CONFLICT DO NOTHING`, id); err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.image_type,a.image_index,a.status,a.digest,a.error_code,a.generation,a.user_modified,e.id::text,COALESCE(e.item->>'Name',''),e.kind,e.provider,e.request,a.source_url<>''
 FROM metadata_artwork a JOIN metadata_entities e ON e.id=a.entity_id
 WHERE a.image_index=0 AND ((e.id=$1 AND a.image_type IN ('Primary','Logo','Banner','Backdrop'))
 OR ($3='series' AND e.request->>'title_id'=$2 AND e.kind='season' AND a.image_type='Primary'))
 ORDER BY CASE e.kind WHEN 'season' THEN 1 ELSE 0 END,
 CASE WHEN e.kind='season' AND COALESCE((e.request->>'season')::int,0)=0 THEN 1 ELSE 0 END,
 COALESCE((e.request->>'season')::int,0),
 CASE a.image_type WHEN 'Primary' THEN 0 WHEN 'Logo' THEN 1 WHEN 'Banner' THEN 2 ELSE 3 END`, v.Item.ID, id, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []EditableArtwork{}
	for rows.Next() {
		var a EditableArtwork
		var req Request
		if err = rows.Scan(&a.ID, &a.Type, &a.Index, &a.Status, &a.Tag, &a.Error, &a.Generation, &a.UserModified, &a.EntityID, &a.Name, &a.Kind, &a.Provider, &req, &a.HasSource); err != nil {
			return nil, err
		}
		if req.Kind == "season" {
			a.Name = fmt.Sprintf("Season %d", req.Season)
			if req.Season == 0 {
				a.Name = "Specials"
			}
		}
		a.URL = "/api/metadata/images/" + a.ID
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Service) ArtworkMetadataPending(ctx context.Context, v Snapshot) (bool, error) {
	if v.Status == "new" || v.Status == "queued" {
		return true, nil
	}
	if v.FetchedAt != nil {
		return false, nil
	}
	var pending bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM river_job WHERE kind='native_metadata_enrichment' AND args->>'id'=$1 AND (args->>'generation')::bigint=$2 AND state IN ('available','scheduled','retryable','running'))`, v.Item.ID, v.Generation).Scan(&pending)
	return pending, err
}

var ErrArtworkForbidden = errors.New("administrator required for artwork changes")

func lockArtworkEditor(ctx context.Context, tx pgx.Tx, user int64) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT role='admin' AND NOT disabled FROM users WHERE id=$1 FOR SHARE`, user).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrArtworkForbidden
	}
	return nil
}

type editableSlot struct {
	Entity, Type, Digest string
	Generation           int64
}

func lockEditableSlot(ctx context.Context, tx pgx.Tx, id string) (editableSlot, error) {
	var slot editableSlot
	var kind string
	var index int
	err := tx.QueryRow(ctx, `SELECT a.entity_id::text,a.image_type,a.digest,a.generation,e.kind,a.image_index
 FROM metadata_artwork a JOIN metadata_entities e ON e.id=a.entity_id WHERE a.id=$1 FOR UPDATE OF a`, id).
		Scan(&slot.Entity, &slot.Type, &slot.Digest, &slot.Generation, &kind, &index)
	if err == nil && (index != 0 || !editableImageType(kind, slot.Type)) {
		err = ErrInvalid
	}
	return slot, err
}

func (s *Service) UploadArtwork(ctx context.Context, user int64, id string, generation int64, raw []byte) error {
	if user <= 0 || !uuid.MatchString(id) || generation < 1 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockArtworkEditor(ctx, tx, user); err != nil {
		return err
	}
	slot, err := lockEditableSlot(ctx, tx, id)
	if err != nil {
		return err
	}
	if slot.Generation != generation {
		return ErrArtworkChanged
	}
	// Files are durable before their transactional reference is advertised.
	tag, mime, err := s.publishImage(ctx, id, raw)
	if err != nil {
		return err
	}
	actual := slot.Generation + 1
	if _, err = tx.Exec(ctx, `UPDATE metadata_artwork SET generation=$2,user_modified=true,source_url='',digest=$3,content_type=$4,bytes=$5,status='ready',error_code='' WHERE id=$1`, id, actual, tag, mime, len(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO metadata_artwork_edits(artwork_id,generation,user_id,previous_digest,digest) VALUES($1,$2,$3,$4,$5)`, id, actual, user, slot.Digest, tag); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
