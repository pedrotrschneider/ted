package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

var ErrCachePending = errors.New("RD inventory is being built")

type Inventory struct {
	*Client
	pool  *pgxpool.Pool
	queue *river.Client[pgx.Tx]
}
type InventoryStatus struct {
	Configured bool       `json:"configured"`
	Ready      bool       `json:"ready"`
	Total      int        `json:"total"`
	Pending    int        `json:"files_pending"`
	Phase      string     `json:"phase"`
	CheckedAt  *time.Time `json:"checked_at"`
	FullAt     *time.Time `json:"full_rebuilt_at"`
	Error      string     `json:"error_code"`
}
type inventoryArgs struct {
	Full   bool `json:"full"`
	Manual bool `json:"manual"`
}

func (inventoryArgs) Kind() string { return "rd_inventory_sync" }

type inventoryFileArgs struct {
	Scope      string `json:"scope"`
	ID         string `json:"id"`
	Generation int64  `json:"generation"`
}

func (inventoryFileArgs) Kind() string { return "rd_inventory_file" }

type inventoryWorker struct {
	river.WorkerDefaults[inventoryArgs]
	s *Inventory
}
type inventoryFileWorker struct {
	river.WorkerDefaults[inventoryFileArgs]
	s *Inventory
}

func inventoryOpts(queue string) *river.InsertOpts {
	return &river.InsertOpts{Queue: queue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByQueue: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}

func NewInventory(pool *pgxpool.Pool, c *Client, logger *slog.Logger) (*Inventory, error) {
	s := &Inventory{Client: c, pool: pool}
	var schema string
	if e := pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); e != nil {
		return nil, e
	}
	q, e := river.NewClient(riverpgxv5.New(pool), &river.Config{Schema: schema, Logger: logger})
	if e != nil {
		return nil, e
	}
	s.queue = q
	return s, nil
}
func (s *Inventory) RegisterWorkers(workers *river.Workers) {
	river.AddWorker(workers, &inventoryWorker{s: s})
	river.AddWorker(workers, &inventoryFileWorker{s: s})
}
func (s *Inventory) PeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) { return inventoryArgs{}, inventoryOpts("rd_inventory") }, &river.PeriodicJobOpts{RunOnStart: true})
}
func (s *Inventory) UseQueue(q *river.Client[pgx.Tx]) { s.queue = q }
func (s *Inventory) Status(ctx context.Context) (InventoryStatus, error) {
	v := InventoryStatus{Phase: "new"}
	c, e := s.Snapshot(ctx)
	if e != nil {
		return v, e
	}
	v.Configured = c.Configured()
	if !v.Configured {
		v.Phase = "unconfigured"
		return v, nil
	}
	e = s.pool.QueryRow(ctx, `SELECT ready,total,phase,checked_at,full_rebuilt_at,error_code,(SELECT count(*) FROM rd_inventory_torrents t WHERE t.scope=s.scope AND t.detail_generation<t.generation) FROM rd_inventory_state s WHERE scope=$1`, c.CredentialScope()).Scan(&v.Ready, &v.Total, &v.Phase, &v.CheckedAt, &v.FullAt, &v.Error, &v.Pending)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return v, e
}
func (s *Inventory) Request(ctx context.Context, who accounts.Identity, full bool) error {
	c, e := s.Snapshot(ctx)
	if e != nil {
		return e
	}
	if !c.Configured() {
		return ErrNotConfigured
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer database.Rollback(tx)
	if e = accounts.AuthorizeMutation(ctx, tx, who, true); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO rd_inventory_state(scope,phase) VALUES($1,'queued') ON CONFLICT(scope) DO UPDATE SET phase='queued'`, c.CredentialScope()); e != nil {
		return e
	}
	if _, e = s.queue.InsertTx(ctx, tx, inventoryArgs{Full: full, Manual: true}, inventoryOpts("rd_inventory")); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Inventory) List(ctx context.Context, page int) (Page, error) {
	if page < 1 || page > 10000 {
		return Page{}, ErrInvalid
	}
	c, e := s.Snapshot(ctx)
	if e != nil {
		return Page{}, e
	}
	if !c.Configured() {
		return Page{}, ErrNotConfigured
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return Page{}, e
	}
	defer database.Rollback(tx)
	var ready bool
	var total int
	e = tx.QueryRow(ctx, `SELECT ready,total FROM rd_inventory_state WHERE scope=$1`, c.CredentialScope()).Scan(&ready, &total)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && !ready {
		return Page{}, ErrCachePending
	}
	if e != nil {
		return Page{}, e
	}
	if (page-1)*PageSize > total {
		return Page{}, ErrInvalid
	}
	result := Page{Page: page, Total: &total, Torrents: []Torrent{}}
	rows, e := tx.Query(ctx, `SELECT summary FROM rd_inventory_torrents WHERE scope=$1 ORDER BY ordinal LIMIT $2 OFFSET $3`, c.CredentialScope(), PageSize, (page-1)*PageSize)
	if e != nil {
		return Page{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var t Torrent
		if e = rows.Scan(&raw); e != nil {
			return Page{}, e
		}
		if json.Unmarshal(raw, &t) != nil {
			return Page{}, ErrIncomplete
		}
		result.Torrents = append(result.Torrents, t)
	}
	if e = rows.Err(); e != nil {
		return Page{}, e
	}
	if len(result.Torrents) != min(PageSize, max(0, total-(page-1)*PageSize)) {
		return Page{}, ErrIncomplete
	}
	if page*PageSize < total {
		next := page + 1
		result.NextPage = &next
	}
	return result, tx.Commit(ctx)
}
func (s *Inventory) Torrent(ctx context.Context, id string) (Detail, error) {
	if !idPattern.MatchString(id) {
		return Detail{}, ErrInvalid
	}
	c, e := s.Snapshot(ctx)
	if e != nil {
		return Detail{}, e
	}
	if !c.Configured() {
		return Detail{}, ErrNotConfigured
	}
	var raw []byte
	e = s.pool.QueryRow(ctx, `SELECT detail FROM rd_inventory_torrents WHERE scope=$1 AND id=$2 AND detail_generation=generation`, c.CredentialScope(), id).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && len(raw) == 0 {
		return Detail{}, ErrCachePending
	}
	if e != nil {
		return Detail{}, e
	}
	var d Detail
	if json.Unmarshal(raw, &d) != nil {
		return d, ErrIncomplete
	}
	return d, nil
}

func (w *inventoryWorker) Work(ctx context.Context, j *river.Job[inventoryArgs]) error {
	return w.s.reconcile(ctx, j.Args.Full)
}
func (s *Inventory) reconcile(ctx context.Context, force bool) (err error) {
	c, e := s.Snapshot(ctx)
	if e != nil {
		return e
	}
	if !c.Configured() {
		return nil
	}
	scope := c.CredentialScope()
	conn, e := s.pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,84051200))`, scope).Scan(&locked); e != nil {
		return e
	}
	if !locked {
		return river.JobSnooze(time.Second)
	}
	defer func() {
		op, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.Exec(op, `SELECT pg_advisory_unlock(hashtextextended($1,84051200))`, scope); e != nil {
			_ = conn.Conn().Close(op)
		}
	}()
	defer func() {
		if err != nil {
			op, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = s.pool.Exec(op, `UPDATE rd_inventory_state SET phase='failed',error_code=$2 WHERE scope=$1`, scope, rdErrorCode(err))
		}
	}()
	_, e = s.pool.Exec(ctx, `INSERT INTO rd_inventory_state(scope,phase) VALUES($1,'syncing') ON CONFLICT(scope) DO UPDATE SET phase='syncing',error_code=''`, scope)
	if e != nil {
		return e
	}
	var ready bool
	var fullAt *time.Time
	if e = s.pool.QueryRow(ctx, `SELECT ready,full_scan_at FROM rd_inventory_state WHERE scope=$1`, scope).Scan(&ready, &fullAt); e != nil {
		return e
	}
	full := force || !ready || fullAt == nil || time.Since(*fullAt) >= 24*time.Hour
	old := []Torrent{}
	rows, e := s.pool.Query(ctx, `SELECT summary FROM rd_inventory_torrents WHERE scope=$1 ORDER BY ordinal`, scope)
	if e != nil {
		return e
	}
	for rows.Next() {
		var raw []byte
		var v Torrent
		if e = rows.Scan(&raw); e != nil {
			break
		}
		if json.Unmarshal(raw, &v) != nil {
			e = ErrIncomplete
			break
		}
		old = append(old, v)
	}
	rows.Close()
	if e != nil {
		return e
	}
	if e = rows.Err(); e != nil {
		return e
	}
	fresh, complete, e := c.scanInventory(ctx, old, full)
	if e != nil {
		return e
	}
	account, e := c.AccountID(ctx)
	if e != nil {
		return e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer database.Rollback(tx)
	if e = s.credentialFence(ctx, tx, scope); e != nil {
		return e
	}
	var generation int64
	if e = tx.QueryRow(ctx, `UPDATE rd_inventory_state SET generation=generation+1,account_id=$2,ready=true,total=$3,checked_at=now(),full_scan_at=CASE WHEN $4 THEN now() ELSE full_scan_at END,phase='files',error_code='' WHERE scope=$1 RETURNING generation`, scope, account, len(fresh), complete).Scan(&generation); e != nil {
		return e
	}
	ids := make([]string, 0, len(fresh))
	for i, t := range fresh {
		ids = append(ids, t.ID)
		data, _ := json.Marshal(t)
		_, e = tx.Exec(ctx, `INSERT INTO rd_inventory_torrents(scope,id,ordinal,hash,summary,generation) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(scope,id) DO UPDATE SET ordinal=EXCLUDED.ordinal,hash=EXCLUDED.hash,summary=EXCLUDED.summary,generation=CASE WHEN $7 OR rd_inventory_torrents.summary IS DISTINCT FROM EXCLUDED.summary THEN EXCLUDED.generation ELSE rd_inventory_torrents.generation END`, scope, t.ID, i, t.Hash, data, generation, full)
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `DELETE FROM rd_inventory_torrents WHERE scope=$1 AND NOT(id=ANY($2::text[]))`, scope, ids); e != nil {
		return e
	}
	pending, e := tx.Query(ctx, `SELECT id,generation FROM rd_inventory_torrents WHERE scope=$1 AND detail_generation<generation`, scope)
	if e != nil {
		return e
	}
	tasks := []inventoryFileArgs{}
	for pending.Next() {
		a := inventoryFileArgs{Scope: scope}
		if e = pending.Scan(&a.ID, &a.Generation); e != nil {
			break
		}
		tasks = append(tasks, a)
	}
	pending.Close()
	if e != nil {
		return e
	}
	if e = pending.Err(); e != nil {
		return e
	}
	for _, a := range tasks {
		if _, e = s.queue.InsertTx(ctx, tx, a, inventoryOpts("rd_inventory_files")); e != nil {
			return e
		}
	}
	if len(tasks) == 0 {
		if _, e = tx.Exec(ctx, `UPDATE rd_inventory_state SET phase='ready',full_rebuilt_at=CASE WHEN $2 THEN now() ELSE full_rebuilt_at END WHERE scope=$1`, scope, complete); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Inventory) credentialFence(ctx context.Context, tx pgx.Tx, scope string) error {
	if s.Client.store == nil {
		if s.Client.CredentialScope() != scope {
			return ErrMismatch
		}
		return nil
	}
	var key string
	if e := tx.QueryRow(ctx, `SELECT rd_key FROM provider_settings WHERE id FOR SHARE`).Scan(&key); e != nil {
		return e
	}
	if New(key).CredentialScope() != scope {
		return ErrMismatch
	}
	return nil
}
func (s *Inventory) ValidateScope(ctx context.Context, tx pgx.Tx, scope string) error {
	return s.credentialFence(ctx, tx, scope)
}
func (w *inventoryFileWorker) Work(ctx context.Context, j *river.Job[inventoryFileArgs]) error {
	// Limit this background worker to at most ~120 requests/minute, leaving RD
	// capacity for playback/mapping. Waiting obeys worker cancellation.
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	s, a := w.s, j.Args
	c, e := s.Snapshot(ctx)
	if e != nil {
		return e
	}
	if c.CredentialScope() != a.Scope || !c.Configured() {
		return nil
	}
	var valid bool
	if e = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rd_inventory_torrents WHERE scope=$1 AND id=$2 AND generation=$3 AND detail_generation<generation)`, a.Scope, a.ID, a.Generation).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return nil
	}
	d, e := c.Torrent(ctx, a.ID)
	if e != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE rd_inventory_state SET error_code=$2 WHERE scope=$1`, a.Scope, rdErrorCode(e))
		return e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer database.Rollback(tx)
	if e = s.credentialFence(ctx, tx, a.Scope); e != nil {
		return e
	}
	data, _ := json.Marshal(d)
	// Listing bytes describe the torrent; detail bytes may describe only its
	// selected files. Keep the listing snapshot intact for incremental comparisons.
	if _, e = tx.Exec(ctx, `UPDATE rd_inventory_torrents SET detail=$4,detail_generation=generation,observed_at=now() WHERE scope=$1 AND id=$2 AND generation=$3 AND hash=$5`, a.Scope, a.ID, a.Generation, data, d.Hash); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE rd_inventory_state s SET phase='ready',error_code='',full_rebuilt_at=CASE WHEN full_scan_at IS NOT NULL AND (full_rebuilt_at IS NULL OR full_scan_at>full_rebuilt_at) THEN now() ELSE full_rebuilt_at END WHERE scope=$1 AND phase='files' AND NOT EXISTS(SELECT 1 FROM rd_inventory_torrents t WHERE t.scope=s.scope AND detail_generation<t.generation)`, a.Scope); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func rdErrorCode(e error) string {
	switch {
	case errors.Is(e, ErrCredentials):
		return "rd_credentials"
	case errors.Is(e, ErrLimited):
		return "rd_limited"
	case errors.Is(e, ErrIncomplete):
		return "rd_incomplete"
	case errors.Is(e, ErrInfringing):
		return "rd_infringing"
	case errors.Is(e, ErrMismatch):
		return "rd_account_changed"
	default:
		return "rd_unavailable"
	}
}
