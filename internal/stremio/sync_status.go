package stremio

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type OutgoingStatus struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Played   bool   `json:"played"`
	Phase    string `json:"phase"`
	Error    string `json:"error"`
}
type SyncStatus struct {
	Outgoing    []OutgoingStatus `json:"outgoing"`
	LastSuccess *string          `json:"last_success"`
	NextPoll    string           `json:"next_poll"`
	Error       string           `json:"error"`
	Pending     bool             `json:"pending"`
	Issues      int              `json:"issues"`
}

func syncStatus(ctx context.Context, tx pgx.Tx, link string) (*SyncStatus, error) {
	var v SyncStatus
	err := tx.QueryRow(ctx, `SELECT p.last_success::text,p.next_poll::text,p.error_code,EXISTS(SELECT 1 FROM stremio_poll_deliveries WHERE link_id=l.id AND NOT complete),(SELECT count(*) FROM stremio_sync_baselines WHERE link_id=l.id AND issue!='') FROM stremio_sync_links s JOIN stremio_links l ON l.id=s.link_id JOIN stremio_poll_state p ON p.account_id=l.account_id WHERE l.id=$1`, link).Scan(&v.LastSuccess, &v.NextPoll, &v.Error, &v.Pending, &v.Issues)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if v.Error == "" && v.Pending {
		var failed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM river_job job JOIN stremio_poll_deliveries d ON d.snapshot_id::text=job.args->>'snapshot' AND d.link_id=$1 AND NOT d.complete WHERE job.kind='stremio_poll_delivery' AND job.args->>'link'=$1::text AND job.state IN ('retryable','discarded'))`, link).Scan(&failed); err != nil {
			return nil, err
		}
		if failed {
			v.Error = "delivery"
		}
	}
	v.Outgoing = []OutgoingStatus{}
	rows, err := tx.Query(ctx, `SELECT o.identity,COALESCE(c.metadata->>'name',o.identity),o.played,o.phase,o.error_code FROM stremio_outgoing o JOIN catalog_media c ON c.id=o.media_id WHERE o.link_id=$1 AND o.phase IN ('pending','prepared','conflict') AND NOT EXISTS(SELECT 1 FROM stremio_outgoing newer WHERE newer.account_id=o.account_id AND newer.identity=o.identity AND newer.id>o.id) ORDER BY (o.phase='conflict') DESC,o.id LIMIT 20`, link)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry OutgoingStatus
		if err = rows.Scan(&entry.Identity, &entry.Name, &entry.Played, &entry.Phase, &entry.Error); err != nil {
			return nil, err
		}
		v.Outgoing = append(v.Outgoing, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &v, nil
}
