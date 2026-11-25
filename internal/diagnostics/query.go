package diagnostics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Query struct {
	UserID                                                                    int64
	DeviceID, Level, Component, SessionID, Platform, AppVersion, Text, Cursor string
	Since, Until                                                              time.Time
	Limit                                                                     int
	AfterSeq                                                                  int64
}
type Record struct {
	Entry
	UserID     int64     `json:"user_id"`
	Username   string    `json:"username"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Platform   string    `json:"platform"`
	AppVersion string    `json:"app_version"`
	ReceivedAt time.Time `json:"received_at"`
	BatchSeq   int64     `json:"batch_seq"`
}
type Page struct {
	Entries    []Record `json:"entries"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HeadSeq    int64    `json:"head_seq"`
}
type cursor struct {
	Batch  int64 `json:"b"`
	Offset int   `json:"o"`
	Head   int64 `json:"h"`
}

func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (s *Store) Query(ctx context.Context, q Query) (Page, error) {
	p := Page{Entries: []Record{}}
	if q.Limit < 1 || q.Limit > 500 || q.UserID < 0 || q.AfterSeq < 0 || len(q.Text) > 200 || len(q.Cursor) > 256 || q.Since.IsZero() || q.Until.IsZero() || !q.Since.Before(q.Until) {
		return p, ErrInvalid
	}
	if q.Level != "" && !contains([]string{"debug", "info", "warning", "error", "fatal"}, q.Level) {
		return p, ErrInvalid
	}
	var c cursor
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Batch < 0 || c.Head < 0 || c.Offset < -1 || c.Offset > 99 {
			return p, ErrInvalid
		}
	} else {
		if err := s.pool.QueryRow(ctx, "SELECT COALESCE(max(seq),0) FROM client_log_batches").Scan(&c.Head); err != nil {
			return p, err
		}
		c.Batch = c.Head
		c.Offset = -1
	}
	p.HeadSeq = c.Head
	rows, err := s.pool.Query(ctx, "SELECT b.seq,b.user_id,u.username,b.device_id,b.device_name,b.platform,b.app_version,b.received_at,b.payload FROM client_log_batches b JOIN users u ON u.id=b.user_id WHERE b.payload IS NOT NULL AND b.seq<=$1 AND b.seq<=$2 AND b.seq>$3 AND b.received_at>=$4 AND b.received_at<=$5 AND ($6::bigint=0 OR b.user_id=$6) AND ($7='' OR b.device_id=$7) AND ($8='' OR $8=ANY(b.levels)) AND ($9='' OR $9=ANY(b.components)) AND ($10='' OR $10=ANY(b.session_ids)) AND ($11='' OR b.platform=$11) AND ($12='' OR b.app_version=$12) AND ($13='' OR strpos(lower(b.search_text),lower($13))>0) AND b.received_at>now()-(SELECT retention_days FROM client_log_settings WHERE singleton)*interval '1 day' ORDER BY b.seq DESC LIMIT 101", c.Batch, c.Head, q.AfterSeq, q.Since, q.Until, q.UserID, q.DeviceID, q.Level, q.Component, q.SessionID, q.Platform, q.AppVersion, q.Text)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	scanned := 0
	var last int64
	for rows.Next() {
		if scanned == 100 {
			p.NextCursor = encodeCursor(cursor{last - 1, -1, c.Head})
			break
		}
		scanned++
		var r Record
		var payload []byte
		if err = rows.Scan(&r.BatchSeq, &r.UserID, &r.Username, &r.DeviceID, &r.DeviceName, &r.Platform, &r.AppVersion, &r.ReceivedAt, &payload); err != nil {
			return p, err
		}
		last = r.BatchSeq
		entries, err := decode(payload)
		if err != nil {
			return p, err
		}
		start := len(entries) - 1
		if r.BatchSeq == c.Batch && c.Offset >= 0 {
			start = c.Offset
			if start >= len(entries) {
				return p, ErrInvalid
			}
		}
		for i := start; i >= 0; i-- {
			e := entries[i]
			if q.Level != "" && q.Level != e.Level || q.Component != "" && q.Component != e.Component || q.SessionID != "" && q.SessionID != e.SessionID || q.Text != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(q.Text)) {
				continue
			}
			r.Entry = e
			p.Entries = append(p.Entries, r)
			if len(p.Entries) == q.Limit {
				next := cursor{r.BatchSeq, i - 1, c.Head}
				if i == 0 {
					next.Batch--
					next.Offset = -1
				}
				p.NextCursor = encodeCursor(next)
				return p, rows.Err()
			}
		}
	}
	return p, rows.Err()
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func (s *Store) Devices(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, "SELECT d.user_id,u.username,d.device_id,d.device_name,d.platform,d.app_version,d.received_at FROM (SELECT DISTINCT ON(user_id,device_id) user_id,device_id,device_name,platform,app_version,received_at FROM client_log_batches WHERE received_at>now()-(SELECT retention_days FROM client_log_settings WHERE singleton)*interval '1 day' ORDER BY user_id,device_id,seq DESC) d JOIN users u ON u.id=d.user_id ORDER BY d.received_at DESC LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var user int64
		var name, device, label, platform, version string
		var at time.Time
		if err = rows.Scan(&user, &name, &device, &label, &platform, &version, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"user_id": user, "username": name, "device_id": device, "device_name": label, "platform": platform, "app_version": version, "last_seen": at})
	}
	return out, rows.Err()
}
