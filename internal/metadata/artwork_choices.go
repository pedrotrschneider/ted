package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/riverqueue/river"
)

type ArtworkChoice struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Preview  string `json:"preview"`
	Language string `json:"language"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}
type ArtworkChoices struct {
	Status     string          `json:"status"`
	Generation int64           `json:"generation"`
	Images     []ArtworkChoice `json:"images"`
	FetchedAt  *time.Time      `json:"fetched_at,omitempty"`
	Error      string          `json:"error_code"`
}
type artworkReader interface {
	Artwork(context.Context, string, Request) ([]ArtworkChoice, error)
}
type choiceArgs Args

func (choiceArgs) Kind() string { return "native_artwork_choices" }

type choiceWorker struct {
	river.WorkerDefaults[choiceArgs]
	s *Service
}

func validImageType(kind string) bool {
	switch kind {
	case "Primary", "Backdrop", "Banner", "Logo", "Thumb", "Art", "Disc", "Screenshot":
		return true
	}
	return false
}
func normalizeChoices(images []ArtworkChoice) ([]ArtworkChoice, error) {
	if len(images) > 5000 {
		return nil, ErrPartial
	}
	result := []ArtworkChoice{}
	seen := map[string]bool{}
	for _, img := range images {
		if !validImageType(img.Type) || imageURL(img.URL) == "" || imageURL(img.Preview) == "" || img.Width < 0 || img.Height < 0 {
			return nil, ErrPartial
		}
		img.ID = guid(img.Type + ":" + img.URL)
		if seen[img.ID] {
			continue
		}
		seen[img.ID] = true
		result = append(result, img)
	}
	return result, nil
}

func (s *Service) RequestArtworkChoices(ctx context.Context, id string, refresh bool) (ArtworkChoices, error) {
	if !uuid.MatchString(id) {
		return ArtworkChoices{}, ErrInvalid
	}
	entity, err := s.readEntity(ctx, id)
	if err != nil {
		return ArtworkChoices{}, err
	}
	if !editableImageType(entity.Request.Kind, "Primary") {
		return ArtworkChoices{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ArtworkChoices{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO metadata_artwork_choices(entity_id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
		return ArtworkChoices{}, err
	}
	v := ArtworkChoices{Images: []ArtworkChoice{}}
	if err = tx.QueryRow(ctx, `SELECT status,generation,images,fetched_at,error_code FROM metadata_artwork_choices WHERE entity_id=$1 FOR UPDATE`, id).Scan(&v.Status, &v.Generation, &v.Images, &v.FetchedAt, &v.Error); err != nil {
		return v, err
	}
	v.Images = editableChoices(entity.Request.Kind, v.Images)
	if v.Status != "queued" && (v.Status == "new" || refresh || v.Status == "ready" && v.FetchedAt != nil && time.Since(*v.FetchedAt) > 24*time.Hour) {
		v.Generation++
		v.Status = "queued"
		v.Error = ""
		if _, err = s.queue.InsertTx(ctx, tx, choiceArgs{ID: id, Generation: v.Generation}, &river.InsertOpts{Queue: "native_metadata", MaxAttempts: 3}); err != nil {
			return v, err
		}
		if _, err = tx.Exec(ctx, `UPDATE metadata_artwork_choices SET generation=$2,status='queued',error_code='' WHERE entity_id=$1`, id, v.Generation); err != nil {
			return v, err
		}
	}
	return v, tx.Commit(ctx)
}

func editableChoices(kind string, images []ArtworkChoice) []ArtworkChoice {
	result := []ArtworkChoice{}
	for _, img := range images {
		if editableImageType(kind, img.Type) {
			result = append(result, img)
		}
	}
	return result
}

func (w *choiceWorker) Work(ctx context.Context, job *river.Job[choiceArgs]) error {
	v, err := w.s.readEntity(ctx, job.Args.ID)
	if err != nil {
		return err
	}
	if !editableImageType(v.Request.Kind, "Primary") {
		return river.JobCancel(ErrInvalid)
	}
	reader, ok := w.s.reader.(artworkReader)
	if !ok {
		err = ErrUnconfigured
	} else {
		req := v.Request
		req.ProviderID = v.ProviderID
		var images []ArtworkChoice
		images, err = reader.Artwork(ctx, v.Provider, req)
		if err == nil {
			images, err = normalizeChoices(images)
		}
		if err == nil {
			raw, e := json.Marshal(editableChoices(req.Kind, images))
			if e != nil {
				return e
			}
			_, e = w.s.pool.Exec(ctx, `UPDATE metadata_artwork_choices SET images=$3,status='ready',fetched_at=now(),error_code='' WHERE entity_id=$1 AND generation=$2`, v.ID, job.Args.Generation, raw)
			return e
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	permanent := errors.Is(err, ErrInvalid) || errors.Is(err, ErrMissing) || errors.Is(err, ErrUnconfigured)
	var rejected unauthorized
	permanent = permanent || errors.As(err, &rejected)
	status := "failed"
	if !permanent && job.MaxAttempts > 0 && job.Attempt < job.MaxAttempts {
		status = "queued"
	}
	if _, e := w.s.pool.Exec(ctx, `UPDATE metadata_artwork_choices SET status=$4,error_code=$3 WHERE entity_id=$1 AND generation=$2`, v.ID, job.Args.Generation, code(err), status); e != nil {
		return e
	}
	if permanent {
		return river.JobCancel(err)
	}
	return err
}

var ErrArtworkChanged = errors.New("artwork selection changed")

// Accept only a server-retrieved choice for this entity and image type. The
// browser never supplies a fetch URL, and an expired picker must be refreshed.
func (s *Service) SelectArtwork(ctx context.Context, user int64, id string, generation int64, choice string) error {
	if user <= 0 || !uuid.MatchString(id) || !uuid.MatchString(choice) || generation < 1 || s.root == nil {
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
	if generation != slot.Generation {
		return ErrArtworkChanged
	}
	var choices []ArtworkChoice
	var fetched *time.Time
	if err = tx.QueryRow(ctx, `SELECT images,fetched_at FROM metadata_artwork_choices WHERE entity_id=$1 FOR SHARE`, slot.Entity).Scan(&choices, &fetched); err != nil {
		return err
	}
	if fetched == nil || time.Since(*fetched) > 24*time.Hour {
		return ErrArtworkChanged
	}
	source := ""
	for _, img := range choices {
		if img.ID == choice && img.Type == slot.Type && imageURL(img.URL) != "" {
			source = img.URL
			break
		}
	}
	if source == "" {
		return ErrInvalid
	}
	actual := slot.Generation + 1
	if _, err = tx.Exec(ctx, `UPDATE metadata_artwork SET generation=$2,user_modified=true,source_url=$3,status='queued',error_code='' WHERE id=$1`, id, actual, source); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO metadata_artwork_edits(artwork_id,generation,user_id,previous_digest) VALUES($1,$2,$3,$4)`, id, actual, user, slot.Digest); err != nil {
		return err
	}
	if _, err = s.queue.InsertTx(ctx, tx, overrideArgs{ID: id, Generation: actual}, &river.InsertOpts{Queue: "native_artwork", MaxAttempts: 3}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
