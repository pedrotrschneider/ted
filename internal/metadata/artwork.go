package metadata

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"regexp"

	"github.com/riverqueue/river"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type imageWorker struct {
	river.WorkerDefaults[assetArgs]
	s *Service
}

func (w *imageWorker) Work(ctx context.Context, job *river.Job[assetArgs]) error {
	e := w.s.fetchImage(ctx, Args(job.Args))
	if e != nil && ctx.Err() == nil {
		_, update := w.s.pool.Exec(ctx, `UPDATE metadata_artwork SET status='failed',error_code=$3 WHERE id=$1 AND generation=$2 AND NOT user_modified`, job.Args.ID, job.Args.Generation, code(e))
		if update != nil {
			return update
		}
	}
	return e
}
func (s *Service) RequestImage(ctx context.Context, id string, retry bool) (Asset, error) {
	if !uuid.MatchString(id) {
		return Asset{}, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Asset{}, e
	}
	defer tx.Rollback(ctx)
	var a Asset
	var generation int64
	var modified bool
	e = tx.QueryRow(ctx, `SELECT id::text,image_type,image_index,status,digest,error_code,generation,user_modified FROM metadata_artwork WHERE id=$1 FOR UPDATE`, id).Scan(&a.ID, &a.Type, &a.Index, &a.Status, &a.Tag, &a.Error, &generation, &modified)
	if e != nil {
		return a, e
	}
	a.URL = "/api/client/v1/metadata/images/" + id
	a.Generation, a.UserModified = generation, modified
	if !modified && (a.Status == "new" || retry && a.Status == "failed") {
		if s.root == nil {
			a.Error = "artwork_storage_not_configured"
		} else {
			if _, e = s.queue.InsertTx(ctx, tx, assetArgs{ID: id, Generation: generation}, &river.InsertOpts{Queue: "native_artwork", MaxAttempts: 3}); e != nil {
				return a, e
			}
			if _, e = tx.Exec(ctx, `UPDATE metadata_artwork SET status='queued',error_code='' WHERE id=$1`, id); e != nil {
				return a, e
			}
			a.Status = "queued"
			a.Error = ""
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return a, e
	}
	return a, nil
}
func (s *Service) ImageFile(ctx context.Context, id string) (*os.File, string, string, error) {
	var digest, mime string
	var size int64
	if !uuid.MatchString(id) {
		return nil, "", "", ErrInvalid
	}
	e := s.pool.QueryRow(ctx, `SELECT digest,content_type,bytes FROM metadata_artwork WHERE id=$1`, id).Scan(&digest, &mime, &size)
	if e != nil {
		return nil, "", "", e
	}
	if !digestPattern.MatchString(digest) || s.root == nil {
		return nil, "", "", ErrMissing
	}
	f, e := s.root.Open(digest)
	if e != nil {
		return nil, "", "", ErrMissing
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != size {
		f.Close()
		return nil, "", "", ErrImage
	}
	return f, mime, digest, nil
}
func (s *Service) fetchImage(ctx context.Context, args Args) error {
	return s.fetchSelectedImage(ctx, args, false)
}
func (s *Service) fetchSelectedImage(ctx context.Context, args Args, override bool) error {
	if s.root == nil {
		return ErrUnconfigured
	}
	var source, status string
	var generation int64
	var modified bool
	e := s.pool.QueryRow(ctx, `SELECT source_url,generation,user_modified,status FROM metadata_artwork WHERE id=$1`, args.ID).Scan(&source, &generation, &modified, &status)
	if e != nil {
		return e
	}
	if generation != args.Generation || modified != override || status == "ready" {
		return nil
	}
	if imageURL(source) == "" {
		return ErrImage
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, e := s.imageHTTP.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return ErrProvider
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024+1))
	if e != nil || len(raw) > 20*1024*1024 || resp.ContentLength >= 0 && int64(len(raw)) != resp.ContentLength {
		return ErrImage
	}
	tag, mime, e := s.publishImage(ctx, args.ID, raw)
	if e != nil {
		return e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	changed, e := tx.Exec(ctx, `UPDATE metadata_artwork SET digest=$3,content_type=$4,bytes=$5,status='ready',error_code='' WHERE id=$1 AND generation=$2 AND user_modified=$6`, args.ID, args.Generation, tag, mime, len(raw), override)
	if e != nil {
		return e
	}
	if override && changed.RowsAffected() == 1 {
		if _, e = tx.Exec(ctx, `UPDATE metadata_artwork_edits SET digest=$3 WHERE artwork_id=$1 AND generation=$2`, args.ID, args.Generation, tag); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

const MaxArtworkBytes = 20 * 1024 * 1024

// Validate both the header and decoded pixels before publishing immutable bytes.
// The existing reference stays usable until the new file and DB update succeed.
func (s *Service) publishImage(ctx context.Context, id string, raw []byte) (string, string, error) {
	if s.root == nil {
		return "", "", ErrUnconfigured
	}
	if len(raw) == 0 || len(raw) > MaxArtworkBytes {
		return "", "", ErrImage
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return "", "", ErrImage
	}
	if _, _, e = image.Decode(bytes.NewReader(raw)); e != nil {
		return "", "", ErrImage
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	digest := sha256.Sum256(raw)
	tag := hex.EncodeToString(digest[:])
	mime := "image/" + format
	// Immutable content-addressed files are published before their database reference.
	// A crash leaves an unreferenced owned file, never an advertised partial image.
	temp := id + ".partial-" + rand.Text()
	f, e := s.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", "", ErrImage
	}
	defer s.root.Remove(temp)
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return "", "", ErrImage
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return "", "", ErrImage
	}
	if e = f.Close(); e != nil {
		return "", "", ErrImage
	}
	if e = s.root.Rename(temp, tag); e != nil {
		return "", "", ErrImage
	}
	directory, e := s.root.Open(".")
	if e != nil {
		return "", "", ErrImage
	}
	e = directory.Sync()
	_ = directory.Close()
	if e != nil {
		return "", "", ErrImage
	}
	return tag, mime, nil
}
