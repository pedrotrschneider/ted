package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type Service struct {
	pool      *pgxpool.Pool
	reader    Reader
	queue     *river.Client[pgx.Tx]
	root      *os.Root
	logger    *slog.Logger
	imageHTTP *http.Client
}
type Args struct {
	ID         string `json:"id"`
	Generation int64  `json:"generation"`
}

func (Args) Kind() string { return "native_metadata_enrichment" }

type assetArgs Args

func (assetArgs) Kind() string { return "native_metadata_artwork" }

type creditArgs struct {
	ID         string `json:"id"`
	Generation int64  `json:"generation"`
}

const workerConcurrency = 5

func (creditArgs) Kind() string { return "native_metadata_credit_identity" }

type entity struct {
	ID, Kind, Identity, Provider, ProviderID, Parent, Status string
	Generation, AppliedGeneration                            int64
	Item                                                     Item
	Request                                                  Request
}

func New(pool *pgxpool.Pool, reader Reader, imageDir string, logger *slog.Logger) (*Service, error) {
	s := &Service{pool: pool, reader: reader, logger: logger, imageHTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if logger == nil {
		s.logger = slog.Default()
	}
	if imageDir != "" {
		if e := os.MkdirAll(imageDir, 0750); e != nil {
			return nil, e
		}
		r, e := os.OpenRoot(imageDir)
		if e != nil {
			return nil, e
		}
		s.root = r
	}
	var schema string
	op, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := pool.QueryRow(op, "SELECT current_schema()").Scan(&schema); e != nil {
		s.Close()
		return nil, e
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &worker{s: s})
	river.AddWorker(workers, &imageWorker{s: s})
	river.AddWorker(workers, &creditWorker{s: s})
	river.AddWorker(workers, &choiceWorker{s: s})
	river.AddWorker(workers, &overrideWorker{s: s})
	river.AddWorker(workers, &seriesLayoutWorker{s: s})
	q, e := river.NewClient(riverpgxv5.New(pool), &river.Config{Schema: schema, Workers: workers, Queues: map[string]river.QueueConfig{"native_metadata": {MaxWorkers: workerConcurrency}, "native_artwork": {MaxWorkers: workerConcurrency}}, JobTimeout: 2 * time.Minute, RescueStuckJobsAfter: 4 * time.Minute, Logger: s.logger})
	if e != nil {
		s.Close()
		return nil, e
	}
	s.queue = q
	return s, nil
}
func (s *Service) Start(ctx context.Context) error { return s.queue.Start(ctx) }
func (s *Service) Stop(ctx context.Context) error  { return s.queue.Stop(ctx) }
func (s *Service) Close() {
	if s.root != nil {
		_ = s.root.Close()
	}
}
func (s *Service) Configured(ctx context.Context, provider string) (bool, error) {
	if stored, ok := s.reader.(interface {
		Available(context.Context, string) (bool, error)
	}); ok {
		return stored.Available(ctx, provider)
	}
	return s.reader.Enabled(provider), nil
}

func identity(provider string, r Request) (string, error) {
	switch r.Kind {
	case "movie", "series":
		if titleID.MatchString(r.TitleID) {
			return r.Kind + ":" + r.TitleID, nil
		}
	case "season":
		if titleID.MatchString(r.TitleID) && r.Season >= 0 && r.Season <= 9999 {
			return fmt.Sprintf("season:%s:%d", r.TitleID, r.Season), nil
		}
	case "episode":
		if r.VideoID != "" {
			if cinemeta.ValidVideoID(r.VideoID) && strings.HasPrefix(r.VideoID, r.TitleID+":") {
				return "episode:" + r.VideoID, nil
			}
			return "", ErrInvalid
		}
		if titleID.MatchString(r.TitleID) && r.Season >= 0 && r.Season <= 9999 && r.Number >= 0 && r.Number <= 99999 {
			return fmt.Sprintf("episode:%s:%d:%d", r.TitleID, r.Season, r.Number), nil
		}
	case "person":
		if (provider == "Tmdb" || provider == "Tvdb") && positiveID.MatchString(r.ProviderID) {
			return "person:" + provider + ":" + r.ProviderID, nil
		}
	}
	return "", ErrInvalid
}
func seed(r Request) Item {
	types := map[string]string{"movie": "Movie", "series": "Series", "season": "Season", "episode": "Episode", "person": "Person"}
	v := Item{Type: types[r.Kind], ProviderIDs: map[string]string{}}
	if r.TitleID != "" {
		v.ProviderIDs["Imdb"] = r.TitleID
	}
	if r.Kind == "season" {
		v.IndexNumber = &r.Season
	}
	if r.Kind == "episode" {
		v.IndexNumber = &r.Number
		v.ParentIndexNumber = &r.Season
		v.Name = r.EpisodeName
		v.PremiereDate = date(r.EpisodeDate)
	}
	return v
}
func CinemetaSeed(m cinemeta.Movie) Item {
	v := seed(Request{Kind: m.Type, TitleID: m.ID})
	v.Name = m.Name
	v.Overview = m.Description
	v.Genres = m.Genres
	v.CommunityRating = m.IMDbRating
	if n, e := strconv.Atoi(m.Year); e == nil {
		v.ProductionYear = &n
	}
	return v
}
func (s *Service) Ensure(ctx context.Context, provider string, r Request, fallback Item) (Snapshot, error) {
	key, e := identity(provider, r)
	if e != nil {
		return Snapshot{}, e
	}
	if provider != "Tmdb" && provider != "Tvdb" {
		return Snapshot{}, ErrInvalid
	}
	if r.Kind == "movie" && provider != "Tmdb" {
		return Snapshot{}, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Snapshot{}, e
	}
	defer tx.Rollback(ctx)
	if r.Kind == "series" || r.Kind == "season" || r.Kind == "episode" {
		provider, e = lockSeriesProvider(ctx, tx, r.TitleID)
		if e != nil {
			return Snapshot{}, e
		}
	}
	enabled, e := s.Configured(ctx, provider)
	if e != nil {
		return Snapshot{}, e
	}
	parent := ""
	if r.Kind == "season" || r.Kind == "episode" {
		pr := Request{Kind: "series", TitleID: r.TitleID}
		parent, e = s.insert(ctx, tx, provider, pr, seed(pr), "")
		if e != nil {
			return Snapshot{}, e
		}
		if e = s.queueEntity(ctx, tx, parent, false, enabled); e != nil {
			return Snapshot{}, e
		}
	}
	id, e := s.insert(ctx, tx, provider, r, fallback, parent)
	if e != nil {
		return Snapshot{}, e
	}
	if provider == "Tvdb" && (r.Kind == "series" || r.Kind == "episode") {
		if e = s.repairTVDBCast(ctx, tx, id, enabled); e != nil {
			return Snapshot{}, e
		}
	}
	if r.Kind == "movie" || r.Kind == "series" {
		if e = s.repairOriginalLanguage(ctx, tx, id, enabled); e != nil {
			return Snapshot{}, e
		}
	}
	if e = s.queueEntity(ctx, tx, id, false, enabled); e != nil {
		return Snapshot{}, e
	}
	if (r.Kind == "season" || r.Kind == "episode") && !r.SkipCast {
		var requested bool
		var skipped bool
		var status, previous string
		if e = tx.QueryRow(ctx, `SELECT COALESCE((request->>'scoped_cast_requested')::boolean,false),COALESCE((request->>'skip_cast')::boolean,false),status,COALESCE(request->>'scoped_cast_signature','') FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&requested, &skipped, &status, &previous); e != nil {
			return Snapshot{}, e
		}
		_, signature, err := castScope(ctx, tx, r)
		if err != nil {
			return Snapshot{}, err
		}
		if skipped || !requested || previous != signature {
			if _, e = tx.Exec(ctx, `UPDATE metadata_entities SET request=(request-'skip_cast')||jsonb_build_object('scoped_cast_requested',true,'scoped_cast_signature',$2::text) WHERE id=$1`, id, signature); e != nil {
				return Snapshot{}, e
			}
			if skipped || status == "ready" || status == "partial" || status == "failed" || status == "missing" || requested && previous != "" && previous != signature {
				if e = s.queueEntity(ctx, tx, id, true, enabled); e != nil {
					return Snapshot{}, e
				}
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return Snapshot{}, e
	}
	return s.Get(ctx, guid(key))
}

// Older cached TVDB guest stars were classified as Unknown. Revisit only a
// touched title, once, under the persistent lock; retain credits on failure
// and use the normal worker's locked-field/artwork preservation rules.
func (s *Service) repairTVDBCast(ctx context.Context, tx pgx.Tx, id string, enabled bool) error {
	if !enabled {
		return nil
	}
	var state string
	var request Request
	var item Item
	if err := tx.QueryRow(ctx, `SELECT status,request,item FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&state, &request, &item); err != nil {
		return err
	}
	if request.TVDBCastTypesChecked || (state != "ready" && state != "partial") || item.LockData != nil && *item.LockData {
		return nil
	}
	for _, field := range item.LockedFields {
		if field == "People" || field == "Cast" {
			return nil
		}
	}
	unknown := false
	for _, person := range item.People {
		unknown = unknown || person.Type == "Unknown"
	}
	if !unknown {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE metadata_entities SET request=request||'{"tvdb_cast_types_checked":true}'::jsonb WHERE id=$1`, id); err != nil {
		return err
	}
	return s.queueEntity(ctx, tx, id, true, enabled)
}
func (s *Service) insert(ctx context.Context, tx pgx.Tx, provider string, r Request, fallback Item, parent string) (string, error) {
	key, e := identity(provider, r)
	if e != nil {
		return "", e
	}
	id := guid(key)
	fallback.ID = id
	if fallback.Type == "" {
		fallback = seed(r)
		fallback.ID = id
	}
	fallback.ParentID = parent
	if r.Kind == "episode" || r.Kind == "season" {
		fallback.SeriesID = guid("series:" + r.TitleID)
	}
	if r.Kind == "episode" {
		fallback.SeasonID = guid(fmt.Sprintf("season:%s:%d", r.TitleID, r.Season))
	}
	req, _ := json.Marshal(r)
	raw, _ := json.Marshal(fallback)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	sources := map[string]string{}
	source := "Cinemeta"
	if r.Kind == "person" {
		source = provider
	}
	if r.Kind == "season" {
		source = "TED"
	}
	for field := range fields {
		sources[field] = source
	}
	for _, field := range []string{"Id", "Type", "ParentId", "SeriesId", "SeasonId", "IndexNumber", "ParentIndexNumber"} {
		if _, ok := fields[field]; ok {
			sources[field] = "TED"
		}
	}
	provenance, _ := json.Marshal(sources)
	_, e = tx.Exec(ctx, `INSERT INTO metadata_entities(id,kind,identity,parent_id,provider,provider_id,item,request,field_sources)
 VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9) ON CONFLICT(kind,identity) DO UPDATE SET item=metadata_entities.item||EXCLUDED.item,field_sources=metadata_entities.field_sources||EXCLUDED.field_sources WHERE metadata_entities.applied_generation=0 AND COALESCE(metadata_entities.item->>'Name','')=''`, id, r.Kind, key, parent, provider, r.ProviderID, raw, req, provenance)
	return id, e
}
func (s *Service) queueEntity(ctx context.Context, tx pgx.Tx, id string, force, enabled bool) error {
	var state string
	var generation int64
	if e := tx.QueryRow(ctx, `SELECT status,generation FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&state, &generation); e != nil {
		return e
	}
	if !force && state != "new" && state != "not_configured" {
		return nil
	}
	if !enabled {
		if force {
			generation++
		}
		_, e := tx.Exec(ctx, `UPDATE metadata_entities SET status='not_configured',generation=$2,error_code='provider_not_configured' WHERE id=$1`, id, generation)
		return e
	}
	generation++
	if _, e := s.queue.InsertTx(ctx, tx, Args{id, generation}, &river.InsertOpts{Queue: "native_metadata", MaxAttempts: 3}); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `UPDATE metadata_entities SET generation=$2,status='queued',error_code='' WHERE id=$1`, id, generation)
	return e
}
func (s *Service) Get(ctx context.Context, id string) (Snapshot, error) {
	if !uuid.MatchString(id) {
		return Snapshot{}, ErrInvalid
	}
	v := Snapshot{Images: []Asset{}}
	var request Request
	e := s.pool.QueryRow(ctx, `SELECT item,status,provider,generation,fetched_at,error_code,field_sources,request FROM metadata_entities WHERE id=$1`, id).Scan(&v.Item, &v.Status, &v.Provider, &v.Generation, &v.FetchedAt, &v.Error, &v.FieldSources, &request)
	if e != nil {
		return v, e
	}
	v.normalizeTMDBCast()
	v.BiographyMatch = request.BiographyMatch
	v.ScopedCast = request.ScopedCastChecked
	v.OriginalLanguage = request.OriginalLanguage
	if v.OriginalLanguage == "" && (request.Kind == "season" || request.Kind == "episode") {
		// Audio follows the show's origin, not the language of translated descriptions.
		if e = s.pool.QueryRow(ctx, `SELECT COALESCE(request->>'original_language','') FROM metadata_entities WHERE kind='series' AND identity=$1`, "series:"+request.TitleID).Scan(&v.OriginalLanguage); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return v, e
		}
	}
	v.Item.ImageTags = map[string]string{}
	v.Item.BackdropImageTags = []string{}
	rows, e := s.pool.Query(ctx, `SELECT id::text,image_type,image_index,status,digest,error_code,generation,user_modified FROM metadata_artwork WHERE entity_id=$1 AND image_index=0 ORDER BY image_type`, id)
	if e != nil {
		return v, e
	}
	for rows.Next() {
		var a Asset
		if e = rows.Scan(&a.ID, &a.Type, &a.Index, &a.Status, &a.Tag, &a.Error, &a.Generation, &a.UserModified); e != nil {
			break
		}
		a.URL = "/api/client/v1/metadata/images/" + a.ID
		v.Images = append(v.Images, a)
		if a.Tag != "" {
			if a.Type == "Backdrop" {
				v.Item.BackdropImageTags = append(v.Item.BackdropImageTags, a.Tag)
			} else {
				v.Item.ImageTags[a.Type] = a.Tag
			}
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return v, e
	}
	if len(v.Item.People) > 0 {
		ids := []string{}
		for _, p := range v.Item.People {
			ids = append(ids, p.ID)
		}
		people, e := s.pool.Query(ctx, `SELECT entity_id::text,digest FROM metadata_artwork WHERE entity_id=ANY($1::uuid[]) AND image_type='Primary' AND image_index=0 AND digest<>''`, ids)
		if e != nil {
			return v, e
		}
		tags := map[string]string{}
		for people.Next() {
			var id, tag string
			if e = people.Scan(&id, &tag); e != nil {
				break
			}
			tags[id] = tag
		}
		if e == nil {
			e = people.Err()
		}
		people.Close()
		if e != nil {
			return v, e
		}
		for i := range v.Item.People {
			v.Item.People[i].PrimaryImageTag = tags[v.Item.People[i].ID]
		}
	}
	return v, nil
}

// Lookup returns an already retained summary without initiating enrichment.
func (s *Service) Lookup(ctx context.Context, r Request) (Snapshot, error) {
	key, e := identity("Tvdb", r)
	if e != nil {
		return Snapshot{}, e
	}
	return s.Get(ctx, guid(key))
}
func (s *Service) RequestPerson(ctx context.Context, id string) (Snapshot, error) {
	if !uuid.MatchString(id) {
		return Snapshot{}, ErrInvalid
	}
	v, e := s.readEntity(ctx, id)
	if e != nil {
		return Snapshot{}, e
	}
	if v.Kind != "person" {
		return Snapshot{}, ErrInvalid
	}
	enabled, e := s.Configured(ctx, v.Provider)
	if e != nil {
		return Snapshot{}, e
	}
	fallbackEnabled := false
	if v.Provider == "Tvdb" {
		fallbackEnabled, e = s.Configured(ctx, "Tmdb")
		if e != nil {
			return Snapshot{}, e
		}
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Snapshot{}, e
	}
	defer tx.Rollback(ctx)
	var kind, status string
	var item Item
	var request Request
	if e = tx.QueryRow(ctx, `SELECT kind,status,item,request FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &status, &item, &request); e != nil {
		return Snapshot{}, e
	}
	if kind != "person" {
		return Snapshot{}, ErrInvalid
	}
	// Re-evaluate under the persistent row lock: concurrent visits must queue
	// only one attempt for cached records created before biography fallback.
	biographyLocked := item.LockData != nil && *item.LockData
	for _, field := range item.LockedFields {
		biographyLocked = biographyLocked || field == "Overview"
	}
	nameFallbackPending := item.ProviderIDs["Tmdb"] == "" && item.ProviderIDs["Imdb"] == "" && !request.TMDBBiographyNameChecked
	force := fallbackEnabled && status == "ready" && strings.TrimSpace(item.Overview) == "" && (!request.TMDBBiographyChecked || nameFallbackPending) && !biographyLocked
	var filmographyRevision int64
	if e = tx.QueryRow(ctx, `SELECT COALESCE(sum(revision),0)::bigint FROM metadata_series_providers`).Scan(&filmographyRevision); e != nil {
		return Snapshot{}, e
	}
	if filmographyRevision > request.FilmographyRevision && (status == "ready" || status == "partial" || status == "failed") {
		force = true
		if _, e = tx.Exec(ctx, `UPDATE metadata_entities SET request=request||jsonb_build_object('filmography_revision',$2::bigint,'filmography_providers',true) WHERE id=$1`, id, filmographyRevision); e != nil {
			return Snapshot{}, e
		}
	}
	// Upgrade old filmographies once, retaining cached data during the job.
	if enabled && v.Provider == "Tvdb" && !request.TVDBAppearanceChecked && (status == "ready" || status == "partial") && !(item.LockData != nil && *item.LockData) {
		force = true
		if _, e = tx.Exec(ctx, `UPDATE metadata_entities SET request=request||'{"tvdb_appearance_checked":true}'::jsonb WHERE id=$1`, id); e != nil {
			return Snapshot{}, e
		}
	}
	if e = s.queueEntity(ctx, tx, id, force, enabled); e != nil {
		return Snapshot{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Snapshot{}, e
	}
	return s.Get(ctx, id)
}
func (s *Service) Refresh(ctx context.Context, id string) (Snapshot, error) {
	if !uuid.MatchString(id) {
		return Snapshot{}, ErrInvalid
	}
	v, e := s.readEntity(ctx, id)
	if e != nil {
		return Snapshot{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Snapshot{}, e
	}
	defer tx.Rollback(ctx)
	if v.Kind != "person" {
		if _, e = lockSeriesProvider(ctx, tx, v.Request.TitleID); e != nil {
			return Snapshot{}, e
		}
	}
	var kind, imdb string
	var provider string
	if e = tx.QueryRow(ctx, `SELECT kind,COALESCE(request->>'title_id',''),provider FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &imdb, &provider); e != nil {
		return Snapshot{}, e
	}
	enabled, e := s.Configured(ctx, provider)
	if e != nil {
		return Snapshot{}, e
	}
	ids := []string{id}
	if kind == "series" {
		rows, e := tx.Query(ctx, `SELECT id::text FROM metadata_entities WHERE request->>'title_id'=$1 AND id<>$2 AND kind IN ('season','episode') AND (status<>'new' OR applied_generation>0) ORDER BY id`, imdb, id)
		if e != nil {
			return Snapshot{}, e
		}
		for rows.Next() {
			var child string
			if e = rows.Scan(&child); e != nil {
				break
			}
			ids = append(ids, child)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return Snapshot{}, e
		}
	}
	for _, target := range ids {
		if e = s.queueEntity(ctx, tx, target, true, enabled); e != nil {
			return Snapshot{}, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return Snapshot{}, e
	}
	return s.Get(ctx, id)
}
func (s *Service) readEntity(ctx context.Context, id string) (entity, error) {
	var v entity
	e := s.pool.QueryRow(ctx, `SELECT id::text,kind,identity,provider,provider_id,COALESCE(parent_id::text,''),status,generation,applied_generation,item,request FROM metadata_entities WHERE id=$1`, id).Scan(&v.ID, &v.Kind, &v.Identity, &v.Provider, &v.ProviderID, &v.Parent, &v.Status, &v.Generation, &v.AppliedGeneration, &v.Item, &v.Request)
	return v, e
}
func (s *Service) failure(ctx context.Context, id string, generation int64, err error) error {
	state := "failed"
	if errors.Is(err, ErrMissing) {
		state = "missing"
	}
	_, e := s.pool.Exec(ctx, `UPDATE metadata_entities SET status=$3,error_code=$4 WHERE id=$1 AND generation=$2`, id, generation, state, code(err))
	return e
}
func (s *Service) apply(ctx context.Context, v entity, d Document, partial error) error {
	if d.Item.Name == "" || !positiveID.MatchString(d.ProviderID) {
		return ErrPartial
	}
	canonical := map[string]cinemeta.Episode{}
	var show cinemeta.Series
	if len(d.Children) > 0 && v.Request.TitleID != "" {
		var err error
		show, err = catalog.New(s.pool).NativeSeries(ctx, v.Request.TitleID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, ep := range show.Episodes {
			canonical[fmt.Sprintf("%d:%d", ep.Season, ep.Number)] = ep
		}
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var generation int64
	if v.Kind != "person" && v.Request.TitleID != "" {
		if _, e = lockSeriesProvider(ctx, tx, v.Request.TitleID); e != nil {
			return e
		}
	}
	var old Item
	if e = tx.QueryRow(ctx, `SELECT generation,item FROM metadata_entities WHERE id=$1 FOR UPDATE`, v.ID).Scan(&generation, &old); e != nil {
		return e
	}
	if generation != v.Generation {
		return nil
	}
	if v.Kind == "series" {
		aliases, _ := json.Marshal(map[string]string{v.Provider: d.ProviderID})
		if _, e = tx.Exec(ctx, `UPDATE metadata_series_providers SET provider_ids=provider_ids||$2::jsonb WHERE title_id=$1 AND provider=$3`, v.Request.TitleID, aliases, v.Provider); e != nil {
			return e
		}
	}
	if d.Structure != nil {
		v.ProviderID = d.ProviderID
		if e = s.applySeriesStructure(ctx, tx, v, d.Structure, d.StructureCanonical, d.Children); e != nil {
			return e
		}
	}
	d.Item.ID = v.ID
	d.Item.Type = old.Type
	d.Item.ParentID = old.ParentID
	d.Item.IndexNumber = old.IndexNumber
	d.Item.ParentIndexNumber = old.ParentIndexNumber
	d.Item.SeriesID = old.SeriesID
	d.Item.SeasonID = old.SeasonID
	if d.People != nil {
		d.Item.People = []Person{}
		for _, p := range d.People {
			if !positiveID.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" || len(p.Name) > 1024 || len(p.Role) > 4096 {
				return ErrPartial
			}
			req := Request{Kind: "person", ProviderID: p.ID}
			personSeed := Item{Type: "Person", Name: p.Name, ProviderIDs: map[string]string{p.Provider: p.ID}}
			id, e := s.insert(ctx, tx, p.Provider, req, personSeed, "")
			if e != nil {
				return e
			}
			d.Item.People = append(d.Item.People, Person{Name: p.Name, ID: id, Role: p.Role, Type: p.Type})
			if p.Image != "" {
				if e = s.putImage(ctx, tx, id, Image{Type: "Primary", URL: p.Image}); e != nil {
					return e
				}
			}
		}
	}
	for _, a := range d.Images {
		if e = s.putImage(ctx, tx, v.ID, a); e != nil {
			return e
		}
	}
	if d.Credits != nil {
		refs := []string{}
		for _, c := range d.Credits {
			if !positiveID.MatchString(c.ID) || (c.Kind != "movie" && c.Kind != "series") || c.Item.Name == "" || (c.Provider != "Tmdb" && c.Provider != "Tvdb") {
				return ErrPartial
			}
			ref := guid(v.ID + ":" + c.Provider + ":" + c.Kind + ":" + c.ID)
			refs = append(refs, ref)
			c.Item.ID = ref
			raw, _ := json.Marshal(c.Item)
			roles, _ := json.Marshal(c.Roles)
			if c.Roles == nil {
				roles = []byte("[]")
			}
			if c.TitleID != "" && !titleID.MatchString(c.TitleID) {
				return ErrPartial
			}
			if _, e = tx.Exec(ctx, `INSERT INTO metadata_credits(person_id,provider,kind,provider_id,reference_id,summary,cinemeta_id,identity_status,roles,generation) VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $7='' THEN 'new' ELSE 'ready' END,$8,$9) ON CONFLICT(person_id,provider,kind,provider_id) DO UPDATE SET summary=EXCLUDED.summary||CASE WHEN metadata_credits.summary->>'PreferredMetadataLanguage'='en' THEN jsonb_build_object('Name',metadata_credits.summary->>'Name','PreferredMetadataLanguage','en') ELSE '{}'::jsonb END,roles=EXCLUDED.roles,generation=EXCLUDED.generation,identity_status=CASE WHEN metadata_credits.cinemeta_id<>'' THEN 'ready' ELSE 'new' END`, v.ID, c.Provider, c.Kind, c.ID, ref, raw, c.TitleID, roles, v.Generation); e != nil {
				return e
			}
			for _, image := range []Image{{Type: "Primary", URL: c.Poster}, {Type: "Thumb", URL: c.CharacterImage}} {
				if image.URL == "" {
					continue
				}
				if imageURL(image.URL) == "" {
					return ErrPartial
				}
				if _, e = tx.Exec(ctx, `INSERT INTO metadata_artwork(credit_id,image_type,image_index,source_url) VALUES($1,$2,0,$3) ON CONFLICT(credit_id,image_type,image_index) DO UPDATE SET source_url=EXCLUDED.source_url,generation=metadata_artwork.generation+1,status='new',error_code='' WHERE NOT metadata_artwork.user_modified AND metadata_artwork.source_url<>EXCLUDED.source_url`, ref, image.Type, image.URL); e != nil {
					return e
				}
			}
		}
		if _, e = tx.Exec(ctx, `DELETE FROM metadata_credits WHERE person_id=$1 AND NOT(reference_id=ANY($2::uuid[]))`, v.ID, refs); e != nil {
			return e
		}
	}
	// Season requests supply thumbnails only for verified canonical episode tuples.
	if len(d.Children) > 0 {
		for _, child := range d.Children {
			r := child.Request
			fallback := seed(r)
			if r.Kind == "episode" {
				ep, ok := canonical[fmt.Sprintf("%d:%d", r.Season, r.Number)]
				if !ok {
					partial = errors.Join(partial, ErrAmbiguous)
					continue
				}
				if r.VideoID != "" && r.VideoID != ep.ID || v.Provider == "Tmdb" && ep.TMDBID > 0 && r.ProviderID != fmt.Sprint(ep.TMDBID) || v.Provider == "Tvdb" && ep.TVDBID > 0 && r.ProviderID != fmt.Sprint(ep.TVDBID) {
					partial = errors.Join(partial, ErrAmbiguous)
					continue
				}
				r.VideoID = ep.ID
				r.EpisodeName = ep.Name
				r.EpisodeDate = ep.ReleaseDate
				fallback = seed(r)
			}
			id, e := s.insert(ctx, tx, v.Provider, r, fallback, v.ID)
			if e != nil {
				return e
			}
			if child.Image != "" {
				if e = s.putImage(ctx, tx, id, Image{Type: "Primary", URL: child.Image}); e != nil {
					return e
				}
			}
		}
	}
	raw, _ := json.Marshal(d.Item)
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	if d.People != nil {
		values["People"] = d.Item.People
	}
	if old.LockData != nil && *old.LockData {
		values = map[string]any{}
	}
	for _, locked := range old.LockedFields {
		if locked == "Cast" {
			locked = "People"
		}
		if locked == "Runtime" {
			locked = "RunTimeTicks"
		}
		delete(values, locked)
	}
	// Only fetched values replace the retained document. Missing optional fields do not erase it.
	delete(values, "LockData")
	delete(values, "LockedFields")
	raw, _ = json.Marshal(values)
	sources := map[string]string{}
	for field := range values {
		sources[field] = v.Provider
		if source := d.FieldSources[field]; source != "" {
			sources[field] = source
		}
	}
	for _, field := range []string{"Id", "Type", "ParentId", "SeriesId", "SeasonId", "IndexNumber", "ParentIndexNumber"} {
		if _, ok := values[field]; ok {
			sources[field] = "TED"
		}
	}
	provenance, _ := json.Marshal(sources)
	requestValues := map[string]any{}
	if d.FilmographyRevision > 0 {
		requestValues["filmography_revision"] = d.FilmographyRevision
	}
	if d.ScopedCastChecked {
		requestValues["scoped_cast_checked"] = true
	}
	if d.OriginalLanguageChecked || (partial == nil && (v.Kind == "movie" || v.Kind == "series")) {
		requestValues["original_language_checked"] = true
		requestValues["original_language"] = d.OriginalLanguage
	}
	if d.TVDBAppearanceChecked {
		requestValues["tvdb_appearance_checked"] = true
	}
	if d.TMDBBiographyChecked {
		requestValues["tmdb_biography_checked"] = true
	}
	if d.TMDBBiographyNameChecked {
		requestValues["tmdb_biography_name_checked"] = true
	}
	if d.TVDBCastTypesChecked {
		requestValues["tvdb_cast_types_checked"] = true
	}
	if _, replaced := values["Overview"]; replaced {
		// Retain provenance on failure/locked edits; replace it only together
		// with the biography it describes. A primary/verified source clears it.
		requestValues["biography_match"] = d.BiographyMatch
	}
	requestPatch, _ := json.Marshal(requestValues)
	status, errCode := "ready", ""
	if partial != nil {
		status = "partial"
		errCode = code(partial)
	}
	_, e = tx.Exec(ctx, `UPDATE metadata_entities SET item=item||$2::jsonb,provider_id=$3,status=$4,applied_generation=$5,fetched_at=now(),error_code=$6,field_sources=field_sources||$7::jsonb,
 request=request||$8::jsonb WHERE id=$1`, v.ID, raw, d.ProviderID, status, v.Generation, errCode, provenance, requestPatch)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) putImage(ctx context.Context, tx pgx.Tx, id string, a Image) error {
	if imageURL(a.URL) == "" || a.Index < 0 || a.Index > 100 {
		return ErrPartial
	}
	_, e := tx.Exec(ctx, `INSERT INTO metadata_artwork(entity_id,image_type,image_index,source_url) VALUES($1,$2,$3,$4)
 ON CONFLICT(entity_id,image_type,image_index) DO UPDATE SET source_url=EXCLUDED.source_url,generation=metadata_artwork.generation+1,status='new',error_code=''
 WHERE NOT metadata_artwork.user_modified`, id, a.Type, a.Index, a.URL)
	return e
}

// Backfill touched pre-language caches once. The retained DTO/artwork remains
// usable while River performs enrichment; unknown provider language is final
// until explicit refresh. Persistent row locks coalesce concurrent visits.
func (s *Service) repairOriginalLanguage(ctx context.Context, tx pgx.Tx, id string, enabled bool) error {
	if !enabled {
		return nil
	}
	var state string
	var r Request
	if err := tx.QueryRow(ctx, `SELECT status,request FROM metadata_entities WHERE id=$1 FOR UPDATE`, id).Scan(&state, &r); err != nil {
		return err
	}
	if r.OriginalLanguageChecked || (state != "ready" && state != "partial") {
		return nil
	}
	return s.queueEntity(ctx, tx, id, true, enabled)
}
