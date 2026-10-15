package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/stremio"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type Resolver interface {
	ResolveFile(context.Context, realdebrid.FileRequest) (realdebrid.Resolved, error)
	CredentialScope() string
}
type Service struct {
	mappingBatchSize int
	mappingMovies    catalog.Movies
	mappingSeries    catalog.SeriesReader
	pool             *pgxpool.Pool
	locks            *pgxpool.Pool
	linkSlots        chan struct{}
	rd               Resolver
	publicationMode  string
	mediaHTTP        *http.Client
	queue            *river.Client[pgx.Tx]
	inventory        *realdebrid.Inventory
	probe            func(context.Context, Media) (json.RawMessage, error)
	logger           *slog.Logger
	storage          *os.Root
	freeBytes        func() (int64, error)
	localProbe       func(context.Context, string, int64) error
}
type Media struct {
	ID, MappingID, Name, IMDB, Year string
	realdebrid.FileRequest
	Probe json.RawMessage
}
type State struct {
	PublicationMode string `json:"publication_mode"`
	Mapped          bool   `json:"mapped"`
	Validated       bool   `json:"validated"`
	Queued          bool   `json:"queued"`
	Adding          bool   `json:"adding"`
	Phase           string `json:"phase"`
	Error           string `json:"error"`
	Published       bool   `json:"published"`
	JobID           *int64 `json:"job_id"`
}
type prepareArgs struct {
	MovieID string `json:"movie_id"`
}

func (prepareArgs) Kind() string { return "prepare_stream" }

type worker struct {
	river.WorkerDefaults[prepareArgs]
	s *Service
}

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

func New(ctx context.Context, pool *pgxpool.Pool, rd Resolver, logger *slog.Logger, mappingWorkers int) (*Service, error) {
	s := &Service{publicationMode: PublicationNative, mappingBatchSize: 200, mappingMovies: cinemeta.New(), mappingSeries: cinemeta.New(), pool: pool, rd: rd, mediaHTTP: MediaClient(), logger: logger, linkSlots: make(chan struct{}, 2)}
	if mappingWorkers < 1 || mappingWorkers > 4 {
		return nil, ErrInvalid
	}
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return nil, err
	}
	// Serialize River schema upgrades using a dedicated session lock, independently
	// of worker sessions; some upstream migrations need separate transactions.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(84051004)"); err != nil {
		return nil, err
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.Exec(unlock, "SELECT pg_advisory_unlock(84051004)")
		if err != nil {
			_ = conn.Conn().Close(unlock)
		}
	}()
	migrations, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema, Logger: logger})
	if err != nil {
		return nil, err
	}
	if _, err = migrations.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, err
	}
	workers := river.NewWorkers()
	periodic := []*river.PeriodicJob{stremio.PeriodicJob(), catalog.MetadataPeriodicJob(), river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
		return orphanArgs{}, cleanupOptions()
	}, &river.PeriodicJobOpts{RunOnStart: true})}
	queues := map[string]river.QueueConfig{"catalog_metadata": {MaxWorkers: 1}, "default": {MaxWorkers: 1}, "stremio": {MaxWorkers: 2}, "mapping_control": {MaxWorkers: 1}, "mapping_validation": {MaxWorkers: mappingWorkers}}
	if client, ok := rd.(*realdebrid.Client); ok {
		s.inventory, err = realdebrid.NewInventory(pool, client, logger)
		if err != nil {
			return nil, err
		}
		s.inventory.RegisterWorkers(workers)
		periodic = append(periodic, s.inventory.PeriodicJob())
		queues["rd_inventory"] = river.QueueConfig{MaxWorkers: 1}
		queues["rd_inventory_files"] = river.QueueConfig{MaxWorkers: 1}
	}
	stremio.RegisterWorkers(workers, pool)
	catalog.RegisterMetadataWorkers(workers, pool)
	river.AddWorker(workers, &worker{s: s})
	river.AddWorker(workers, &downloadWorker{s: s})
	river.AddWorker(workers, &mappingWorker{s: s})
	river.AddWorker(workers, &mappingFileWorker{s: s})
	river.AddWorker(workers, &orphanWorker{s: s})
	river.AddWorker(workers, &removalWorker{s: s})
	s.queue, err = river.NewClient(riverpgxv5.New(pool), &river.Config{Schema: schema, Workers: workers, PeriodicJobs: periodic, Queues: queues, JobTimeout: 3 * time.Minute, RescueStuckJobsAfter: 5 * time.Minute, SoftStopTimeout: 5 * time.Second, Logger: logger})
	if err != nil {
		return nil, err
	}
	if s.inventory != nil {
		s.inventory.UseQueue(s.queue)
	}
	if err = s.configureLifecycle(ctx); err != nil {
		return nil, err
	}
	s.probe = s.ffprobe
	return s, err
}
func (s *Service) Start(ctx context.Context) error    { return s.queue.Start(ctx) }
func (s *Service) RDInventory() *realdebrid.Inventory { return s.inventory }
func (s *Service) Stop(ctx context.Context) error     { return s.queue.StopAndCancel(ctx) }
func (s *Service) Status(ctx context.Context, imdb string) (State, error) {
	state := State{PublicationMode: s.publicationMode}
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(s.phase,'draft'),COALESCE(s.error_code,''),COALESCE(s.published,false),s.job_id,
        pending.id IS NOT NULL,
        pending.id IS NOT NULL OR EXISTS(SELECT 1 FROM media_mappings WHERE movie_id=c.id),
        COALESCE(candidate.probe IS NOT NULL,false),
        COALESCE(pending.queued,false)
        FROM (SELECT 1) seed
        LEFT JOIN catalog_media c ON c.cinemeta_id=$1
        LEFT JOIN streaming_items s ON s.movie_id=c.id
        LEFT JOIN LATERAL (
          SELECT o.id,(f.batch_number>o.batch_number OR o.phase='queued') AS queued FROM mapping_operations o JOIN mapping_submission_files f ON f.submission_id=o.submission_id
          WHERE f.episode_id=$1 AND f.batch_number>=o.batch_number AND o.phase IN ('queued','validating','publishing')
          UNION ALL
          SELECT o.id,o.phase='queued' AS queued FROM mapping_operations o, jsonb_array_elements(o.request->'files') f
          WHERE o.submission_id IS NULL AND o.phase IN ('queued','validating','publishing') AND f->>'episode_id'=$1
        ) pending ON true
        LEFT JOIN mapping_candidates candidate ON candidate.operation_id=pending.id AND candidate.media_id=c.id`, imdb).Scan(&state.Phase, &state.Error, &state.Published, &state.JobID, &state.Adding, &state.Mapped, &state.Validated, &state.Queued)
	return state, err
}
func (s *Service) Enqueue(ctx context.Context, who accounts.Identity, imdb string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	if _, err = s.enqueueTx(ctx, tx, imdb); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// enqueueTx shares locking and retry semantics between single and grouped preparation.
func (s *Service) enqueueTx(ctx context.Context, tx pgx.Tx, imdb string) (bool, error) {
	var movie, mapping string
	err := tx.QueryRow(ctx, `SELECT c.id::text,m.id::text FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id WHERE c.cinemeta_id=$1 AND NOT EXISTS(SELECT 1 FROM library_removal_items i JOIN library_removals r ON r.id=i.removal_id WHERE i.item_id=c.id AND NOT r.retired) FOR UPDATE OF c`, imdb).Scan(&movie, &mapping)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotReady
	}
	if err != nil {
		return false, err
	}
	var phase string
	err = tx.QueryRow(ctx, `SELECT phase FROM streaming_items WHERE movie_id=$1 FOR UPDATE`, movie).Scan(&phase)
	if err == nil && phase != "failed" && phase != "cancelled" {
		return false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	job, err := s.queue.InsertTx(ctx, tx, prepareArgs{movie}, &river.InsertOpts{MaxAttempts: 3})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO streaming_items(movie_id,mapping_id,phase,job_id) VALUES($1,$2,'queued',$3) ON CONFLICT(movie_id) DO UPDATE SET phase='queued',job_id=$3,error_code='',updated_at=now()`, movie, mapping, job.Job.ID)
	if err != nil {
		return false, err
	}
	return true, nil
}
func (s *Service) Cancel(ctx context.Context, who accounts.Identity, imdb string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, who, true); err != nil {
		return err
	}
	var job int64
	var phase string
	err = tx.QueryRow(ctx, `SELECT s.job_id,s.phase FROM streaming_items s JOIN catalog_media c ON c.id=s.movie_id WHERE c.cinemeta_id=$1 FOR UPDATE OF s`, imdb).Scan(&job, &phase)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotReady
	}
	if err != nil {
		return err
	}
	if phase == "ready" {
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE streaming_items SET phase='cancelled',error_code='',updated_at=now() WHERE job_id=$1`, job); err != nil {
		return err
	}
	if _, err = s.queue.JobCancelTx(ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) media(ctx context.Context, id string, published bool) (Media, error) {
	var m Media
	if !uuidPattern.MatchString(id) {
		return m, ErrInvalid
	}
	err := s.pool.QueryRow(ctx, `SELECT c.id::text,m.id::text,COALESCE(c.metadata->>'name',c.cinemeta_id),c.cinemeta_id,COALESCE(c.metadata->>'year',''),m.rd_account_id,m.torrent_id,m.info_hash,m.file_path,m.file_bytes,s.probe FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id JOIN streaming_items s ON s.movie_id=c.id WHERE c.id=$1 AND (NOT $2 OR s.published)`, id, published).Scan(&m.ID, &m.MappingID, &m.Name, &m.IMDB, &m.Year, &m.AccountID, &m.TorrentID, &m.Hash, &m.Path, &m.Bytes, &m.Probe)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotReady
	}
	return m, err
}
func (s *Service) link(ctx context.Context, m Media, invalid string) (string, error) {
	if s.rd == nil {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	rd := s.rd
	if stored, ok := rd.(interface {
		Snapshot(context.Context) (*realdebrid.Client, error)
	}); ok {
		snapshot, err := stored.Snapshot(ctx)
		if err != nil {
			return "", err
		}
		rd = snapshot
	}
	// Bound pool consumption, leaving connections for durable recovery writes.
	// Mapping row locks below remain the cross-process correctness boundary.
	select {
	case s.linkSlots <- struct{}{}:
		defer func() { <-s.linkSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM mapping_versions WHERE id=$1 FOR NO KEY UPDATE`, m.MappingID); err != nil {
		return "", err
	}
	var link string
	err = tx.QueryRow(ctx, `SELECT download_url FROM streaming_links WHERE mapping_id=$1 AND credential_scope=$2 AND expires_at>now()`, m.MappingID, rd.CredentialScope()).Scan(&link)
	if err == nil && link != invalid {
		return link, tx.Commit(ctx)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	want := m.FileRequest
	if recovery, ok := rd.(interface {
		PrepareFile(context.Context, realdebrid.FileRequest, string, func(string) error) (realdebrid.FileRequest, error)
	}); ok {
		var owned string
		err = tx.QueryRow(ctx, `SELECT torrent_id FROM streaming_recovery WHERE mapping_id=$1 AND rd_account_id=$2`, m.MappingID, m.AccountID).Scan(&owned)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		want, err = recovery.PrepareFile(ctx, want, owned, func(id string) error {
			// Separate durable commit: the mapping row lock still serializes workers,
			// but rollback after a remote failure must not erase ownership provenance.
			_, e := s.pool.Exec(ctx, `INSERT INTO streaming_recovery(mapping_id,torrent_id,rd_account_id) VALUES($1,$2,$3) ON CONFLICT(mapping_id) DO UPDATE SET torrent_id=$2,rd_account_id=$3`, m.MappingID, id, m.AccountID)
			return e
		})
		if err != nil {
			return "", err
		}
	}
	resolved, err := rd.ResolveFile(ctx, want)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO streaming_links(mapping_id,credential_scope,download_url,expires_at) VALUES($1,$2,$3,now()+interval '30 minutes') ON CONFLICT(mapping_id) DO UPDATE SET credential_scope=$2,download_url=$3,expires_at=excluded.expires_at`, m.MappingID, rd.CredentialScope(), resolved.URL)
	if err != nil {
		return "", err
	}
	return resolved.URL, tx.Commit(ctx)
}

func (w *worker) Work(ctx context.Context, job *river.Job[prepareArgs]) error {
	s := w.s
	tag, err := s.pool.Exec(ctx, `UPDATE streaming_items SET phase='preparing',error_code='',updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase!='cancelled' AND NOT published`, job.Args.MovieID, job.ID)
	if err != nil {
		return ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		return river.JobCancel(ErrInvalid)
	}
	started := time.Now()
	stage := "resolve"
	m, err := s.media(ctx, job.Args.MovieID, false)
	if err == nil {
		_, err = s.link(ctx, m, "")
	}
	if err == nil {
		_, err = s.pool.Exec(ctx, `UPDATE streaming_items SET phase='probing',updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase!='cancelled'`, m.ID, job.ID)
	}
	resolveDuration := time.Since(started)
	var probeDuration time.Duration
	var probe json.RawMessage
	if err == nil {
		stage = "probe"
		probeStarted := time.Now()
		probe, err = s.probe(ctx, m)
		probeDuration = time.Since(probeStarted)
	}
	if err != nil {
		code := "source_unavailable"
		if stage == "probe" {
			code = "probe_unavailable"
		}
		if errors.Is(err, ErrProbeInvalid) {
			code = "probe_invalid"
		}
		if errors.Is(err, realdebrid.ErrPending) {
			code = "source_pending"
		}
		if errors.Is(err, realdebrid.ErrMismatch) {
			code = "source_mismatch"
		}
		s.logger.Warn("stream preparation attempt failed", "media_id", job.Args.MovieID, "job_id", job.ID, "attempt", job.Attempt, "stage", stage, "code", code, "resolve_ms", resolveDuration.Milliseconds(), "probe_ms", probeDuration.Milliseconds())
		record, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		phase := "failed"
		if job.Attempt < job.MaxAttempts {
			phase = "queued"
		}
		_, _ = s.pool.Exec(record, `UPDATE streaming_items SET phase=$4,error_code=$3,updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase!='cancelled' AND NOT published`, job.Args.MovieID, job.ID, code, phase)
		return errors.New(code) // River diagnostics receive only bounded, credential-free codes.
	}
	s.logger.Info("stream preparation metadata validated", "media_id", job.Args.MovieID, "job_id", job.ID, "attempt", job.Attempt, "resolve_ms", resolveDuration.Milliseconds(), "probe_ms", probeDuration.Milliseconds())
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer database.Rollback(tx)
	tag, err = tx.Exec(ctx, `UPDATE streaming_items SET phase='ready',probe=$3,published=true,error_code='',updated_at=now() WHERE movie_id=$1 AND job_id=$2 AND phase!='cancelled'`, m.ID, job.ID, probe)
	if err != nil {
		return ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		return river.JobCancel(ErrInvalid)
	}
	if _, err = river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return ErrUnavailable
	}
	return tx.Commit(ctx)
}
