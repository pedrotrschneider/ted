package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"sort"
)

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// Named variants can share a manifest ID (e.g. Torrentio and Torrentio RD).
// Credentials/configured URLs do not belong in shared administrator settings.
func (d Descriptor) PolicyKey() string {
	return digest(d.ProviderKey() + "\x00" + label(d.Manifest.Name, 256))
}

func disabledProviders(ctx context.Context, q queryer) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT id FROM stremio_addon_policies WHERE NOT enabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		values[id] = true
	}
	return values, rows.Err()
}

// Policy writes serialize with publication/session/favorite decisions. Network
// requests already in flight may finish, but cannot publish disabled choices.
func policyLock(ctx context.Context, tx pgx.Tx, exclusive bool) error {
	sql := `SELECT pg_advisory_xact_lock_shared(hashtextextended('stremio-addon-policies',0))`
	if exclusive {
		sql = `SELECT pg_advisory_xact_lock(hashtextextended('stremio-addon-policies',0))`
	}
	_, err := tx.Exec(ctx, sql)
	return err
}
func policyFence(ctx context.Context, tx pgx.Tx, id string) error {
	if err := policyLock(ctx, tx, false); err != nil {
		return err
	}
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM stremio_addon_policies WHERE id=$1),true)`, id).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return ErrSelection
	}
	return nil
}

type Policy struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision"`
}

func policyList(ctx context.Context, q queryer) ([]Policy, error) {
	values := map[string]Policy{}
	rows, err := q.Query(ctx, `SELECT c.payload FROM server_addon_collections c JOIN server_addon_accounts a ON a.id=c.provider_id WHERE a.active AND a.updated_at=c.account_updated_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var ds []Descriptor
		if json.Unmarshal(raw, &ds) != nil {
			rows.Close()
			return nil, ErrIncomplete
		}
		for _, d := range ds {
			key := d.PolicyKey()
			name := label(d.Manifest.Name, 256)
			old, ok := values[key]
			if !ok || name < old.Name {
				values[key] = Policy{key, name, true, 0}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT id,name,enabled,revision FROM stremio_addon_policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Policy
		if err = rows.Scan(&p.ID, &p.Name, &p.Enabled, &p.Revision); err != nil {
			return nil, err
		}
		if _, installed := values[p.ID]; installed {
			values[p.ID] = p
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := make([]Policy, 0, len(values))
	for _, p := range values {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
func (s *Service) Policies(ctx context.Context) ([]Policy, error) { return policyList(ctx, s.pool) }
func (s *Service) UpdatePolicy(ctx context.Context, actor accounts.Identity, id string, enabled bool, revision int64) (Policy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Policy{}, err
	}
	defer database.Rollback(tx)
	if err = accounts.AuthorizeMutation(ctx, tx, actor, true); err != nil {
		return Policy{}, err
	}
	if err = policyLock(ctx, tx, true); err != nil {
		return Policy{}, err
	}
	all, err := policyList(ctx, tx)
	if err != nil {
		return Policy{}, err
	}
	var current Policy
	found := false
	for _, p := range all {
		if p.ID == id {
			current = p
			found = true
			break
		}
	}
	if !found || revision < 0 {
		return Policy{}, ErrUnsupported
	}
	if current.Revision != revision {
		return Policy{}, ErrPolicyConflict
	}
	current.Enabled = enabled
	current.Revision++
	_, err = tx.Exec(ctx, `INSERT INTO stremio_addon_policies(id,name,enabled,revision) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET enabled=EXCLUDED.enabled,revision=EXCLUDED.revision`, id, current.Name, enabled, current.Revision)
	if err != nil {
		return Policy{}, err
	}
	return current, tx.Commit(ctx)
}
