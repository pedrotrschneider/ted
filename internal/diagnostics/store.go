// Package diagnostics stores bounded, redacted client diagnostics independently
// of viewing state. A batch and its retry receipt commit in one transaction.
package diagnostics

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"entertainmentdistrict/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxBatchBytes = 256 << 10
const MaxStoredBytes int64 = 1 << 30
const MaxDeviceBytes int64 = 64 << 20
const lockID int64 = 84051051

var ErrInvalid = errors.New("invalid diagnostic batch")
var ErrConflict = errors.New("diagnostic batch identity reused")
var ErrLimited = errors.New("diagnostic upload limited")
var uuid = regexp.MustCompile("^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[1-5][a-fA-F0-9]{3}-[89abAB][a-fA-F0-9]{3}-[a-fA-F0-9]{12}$")
var code = regexp.MustCompile("^[a-zA-Z0-9_.+-]{1,64}$")

type Entry struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	SessionID string    `json:"session_id,omitempty"`
}
type Batch struct {
	ID         string  `json:"batch_id"`
	DeviceName string  `json:"device_name"`
	Platform   string  `json:"platform"`
	AppVersion string  `json:"app_version"`
	Entries    []Entry `json:"entries"`
}
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Settings struct {
	RetentionDays  int   `json:"retention_days"`
	StoredBytes    int64 `json:"stored_bytes"`
	MaxStoredBytes int64 `json:"max_stored_bytes"`
	MaxDeviceBytes int64 `json:"max_device_bytes"`
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	err := s.pool.QueryRow(ctx, "SELECT retention_days,(SELECT COALESCE(sum(stored_bytes),0) FROM client_log_batches) FROM client_log_settings WHERE singleton").Scan(&v.RetentionDays, &v.StoredBytes)
	v.MaxStoredBytes = MaxStoredBytes
	v.MaxDeviceBytes = MaxDeviceBytes
	return v, err
}
func (s *Store) SetRetention(ctx context.Context, days int) error {
	if days < 1 || days > 3650 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE client_log_settings SET retention_days=$1 WHERE singleton", days); err != nil {
		return err
	}
	if err = s.prune(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func cleanBatch(v Batch) (Batch, error) {
	v.Entries = append([]Entry(nil), v.Entries...)
	if !uuid.MatchString(v.ID) || len(v.Entries) < 1 || len(v.Entries) > 100 || len(v.DeviceName) > 128 || !utf8.ValidString(v.DeviceName) || !code.MatchString(v.AppVersion) {
		return v, ErrInvalid
	}
	switch v.Platform {
	case "android", "macos", "windows", "linux", "ios":
	default:
		return v, ErrInvalid
	}
	v.DeviceName = Redact(v.DeviceName)
	seen := map[string]bool{}
	for i := range v.Entries {
		e := &v.Entries[i]
		if !uuid.MatchString(e.ID) || seen[e.ID] || !code.MatchString(e.Component) || len(e.Message) == 0 || len(e.Message) > 8192 || !utf8.ValidString(e.Message) || e.At.Year() < 2000 || e.At.Year() > 2100 || e.SessionID != "" && !uuid.MatchString(e.SessionID) {
			return v, ErrInvalid
		}
		seen[e.ID] = true
		switch e.Level {
		case "debug", "info", "warning", "error", "fatal":
		default:
			return v, ErrInvalid
		}
		e.Message = Redact(e.Message)
		e.At = e.At.UTC()
	}
	return v, nil
}
func (s *Store) Ingest(ctx context.Context, userID int64, deviceID string, v Batch) (bool, error) {
	if userID <= 0 || deviceID == "" || len(deviceID) > 128 {
		return false, ErrInvalid
	}
	v, err := cleanBatch(v)
	if err != nil {
		return false, err
	}
	plain, err := json.Marshal(v.Entries)
	if err != nil || len(plain) > MaxBatchBytes {
		return false, ErrInvalid
	}
	// Include labels in the retry fingerprint. No private pre-redaction data is stored.
	canonical, _ := json.Marshal(v)
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err = zw.Write(plain); err != nil {
		return false, err
	}
	if err = zw.Close(); err != nil {
		return false, err
	}
	first, last := v.Entries[0].At, v.Entries[0].At
	levels, components, sessions := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var text strings.Builder
	for _, e := range v.Entries {
		if e.At.Before(first) {
			first = e.At
		}
		if e.At.After(last) {
			last = e.At
		}
		levels[e.Level] = true
		components[e.Component] = true
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		text.WriteString(e.Message)
		text.WriteByte('\n')
	}
	stored := int64(compressed.Len() + text.Len())
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return false, err
	}
	var old string
	err = tx.QueryRow(ctx, "SELECT payload_hash FROM client_log_batches WHERE user_id=$1 AND device_id=$2 AND batch_id=$3", userID, deviceID, v.ID).Scan(&old)
	if err == nil {
		if old != hash {
			return false, ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err = s.prune(ctx, tx); err != nil {
		return false, err
	}
	var recent, total int
	if err = tx.QueryRow(ctx, "SELECT count(*) FILTER(WHERE user_id=$1 AND device_id=$2 AND received_at>now()-interval '1 minute'),count(*) FROM client_log_batches", userID, deviceID).Scan(&recent, &total); err != nil {
		return false, err
	}
	if recent >= 12 || total >= 1000000 {
		return false, ErrLimited
	}
	_, err = tx.Exec(ctx, "INSERT INTO client_log_batches(batch_id,user_id,device_id,device_name,platform,app_version,first_at,last_at,levels,components,session_ids,search_text,payload,payload_hash,entry_count,stored_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)", v.ID, userID, deviceID, v.DeviceName, v.Platform, v.AppVersion, first, last, keys(levels), keys(components), keys(sessions), text.String(), compressed.Bytes(), hash, len(v.Entries), stored)
	if err != nil {
		return false, err
	}
	if err = s.prune(ctx, tx); err != nil {
		return false, err
	}
	// Evict bodies, retaining small receipts so a lost acknowledgment cannot
	// reinsert an already accepted batch after storage pressure.
	_, err = tx.Exec(ctx, "WITH ranked AS (SELECT seq,sum(stored_bytes) OVER(ORDER BY seq DESC) AS total,sum(stored_bytes) OVER(PARTITION BY user_id,device_id ORDER BY seq DESC) AS device_total FROM client_log_batches WHERE payload IS NOT NULL) UPDATE client_log_batches b SET payload=NULL,search_text='',levels='{}',components='{}',session_ids='{}',stored_bytes=0 FROM ranked r WHERE b.seq=r.seq AND (r.total>$1 OR r.device_total>$2)", MaxStoredBytes, MaxDeviceBytes)
	if err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}
func keys(m map[string]bool) []string {
	v := make([]string, 0, len(m))
	for k := range m {
		v = append(v, k)
	}
	sort.Strings(v)
	return v
}
func (s *Store) prune(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "DELETE FROM client_log_batches WHERE received_at < now()-(SELECT retention_days FROM client_log_settings WHERE singleton)*interval '1 day'")
	return err
}
func (s *Store) Cleanup(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return err
	}
	if err = s.prune(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func decode(payload []byte) ([]Entry, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, MaxBatchBytes+1))
	if err != nil || len(raw) > MaxBatchBytes {
		return nil, ErrInvalid
	}
	var v []Entry
	err = json.Unmarshal(raw, &v)
	return v, err
}

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var opaquePattern = regexp.MustCompile(`\bted_[ar]_[A-Za-z0-9_-]{43}\b`)
var quotedSecret = regexp.MustCompile(`(?i)(["']?(?:authorization|proxy-authorization|cookie|set-cookie|api[-_]?key|access[-_]?token|refresh[-_]?token|password|passwd|secret|token|pin)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*')`)
var bareSecret = regexp.MustCompile(`(?i)(\b(?:authorization|proxy-authorization|cookie|set-cookie|api[-_]?key|access[-_]?token|refresh[-_]?token|password|passwd|secret|token|pin)["']?\s*[:=]\s*)(?:(?:Bearer|Basic)\s+)?[^\s,;"'}]+`)
var bearerPattern = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[^\s,;"'}]+`)

var credentialHeader = regexp.MustCompile(`(?im)\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]*`)

func Redact(s string) string {
	s = urlPattern.ReplaceAllString(s, "[url]")
	s = opaquePattern.ReplaceAllString(s, "[credential]")
	s = credentialHeader.ReplaceAllString(s, "[credential header]")
	s = quotedSecret.ReplaceAllString(s, "${1}[redacted]")
	s = bareSecret.ReplaceAllString(s, "${1}[redacted]")
	s = bearerPattern.ReplaceAllString(s, "[credential]")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
