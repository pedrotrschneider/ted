// Package stremio contains the account datastore protocol. It never uses add-ons
// for watch state and never exposes credentials or raw provider errors to the UI.
package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid Stremio request")
	ErrCredentials = errors.New("Stremio credentials rejected")
	ErrUnavailable = errors.New("Stremio unavailable")
	ErrNotFound    = errors.New("Stremio title record is absent")
	ErrIncomplete  = errors.New("incomplete Stremio data")
	ErrChanged     = errors.New("Stremio changed during read")
	ErrAmbiguous   = errors.New("Stremio episode identity needs review")
)

type Session struct {
	ID    string
	Email string
	Key   string `json:"-"`
}

func (s Session) String() string   { return "Stremio session [credentials redacted]" }
func (s Session) GoString() string { return s.String() }

type Client struct {
	base string
	http *http.Client
}

func New() *Client {
	return &Client{base: "https://api.strem.io/api", http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/"+path, bytes.NewReader(b))
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ErrCredentials
	}
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil {
		return ErrUnavailable
	}
	if len(data) > 16<<20 {
		return ErrIncomplete
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ErrIncomplete
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		if path == "login" {
			return ErrCredentials
		}
		// The installed-collection endpoint returns a missing session as HTTP
		// 200/code 1, verified against the live service with a dummy key.
		if path == "addonCollectionGet" {
			var rejection struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(envelope.Error, &rejection) == nil && rejection.Code == 1 {
				return ErrCredentials
			}
		}
		return ErrUnavailable
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" || json.Unmarshal(envelope.Result, out) != nil {
		return ErrIncomplete
	}
	return nil
}
func (c *Client) Login(ctx context.Context, email, password string) (Session, error) {
	var result struct {
		Key  string `json:"authKey"`
		User struct {
			ID    string `json:"_id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if len(email) > 320 || !strings.Contains(email, "@") || len(password) == 0 || len(password) > 4096 {
		return Session{}, ErrInvalid
	}
	err := c.post(ctx, "login", map[string]any{"email": email, "password": password, "facebook": false}, &result)
	if err != nil {
		return Session{}, err
	}
	if result.User.ID == "" || len(result.User.ID) > 128 || result.User.Email == "" || len(result.Key) < 16 || len(result.Key) > 4096 {
		return Session{}, ErrIncomplete
	}
	return Session{result.User.ID, result.User.Email, result.Key}, nil
}

type Record struct {
	ID       string                     `json:"_id"`
	Type     string                     `json:"type"`
	Name     string                     `json:"name"`
	Modified int64                      `json:"-"`
	AddedAt  *time.Time                 `json:"-"`
	State    map[string]json.RawMessage `json:"state"`
	Raw      json.RawMessage            `json:"-"`
}

func parseRecord(raw json.RawMessage) (Record, error) {
	var r Record
	// Full records use RFC3339 dates, while datastoreMeta uses epoch
	// milliseconds. Normalize only for comparison; preserve the original JSON.
	wire := struct {
		*Record
		Modified string          `json:"_mtime"`
		Created  json.RawMessage `json:"_ctime"`
	}{Record: &r}
	if json.Unmarshal(raw, &wire) != nil {
		return r, ErrIncomplete
	}
	modified, err := time.Parse(time.RFC3339Nano, wire.Modified)
	if err != nil {
		return r, ErrIncomplete
	}
	r.Modified = modified.UnixMilli()
	// Older/imported records may omit creation time. Do not reject watched
	// flags or substitute lastWatched/_mtime for their account-addition date.
	var created string
	if json.Unmarshal(wire.Created, &created) == nil {
		if at, e := time.Parse(time.RFC3339Nano, created); e == nil && at.Year() >= 1970 && at.Year() <= 2100 && !at.After(modified) {
			r.AddedAt = &at
		}
	}
	if r.ID == "" || len(r.ID) > 512 || r.Type == "" || r.Modified <= 0 || r.State == nil {
		return r, ErrIncomplete
	}
	r.Raw = append(json.RawMessage(nil), raw...)
	return r, nil
}
func (c *Client) meta(ctx context.Context, key string) (map[string]int64, error) {
	var raw []json.RawMessage
	if err := c.post(ctx, "datastoreMeta", map[string]any{"authKey": key, "collection": "libraryItem"}, &raw); err != nil {
		return nil, err
	}
	if raw == nil || len(raw) > 20000 {
		return nil, ErrIncomplete
	}
	out := map[string]int64{}
	for _, v := range raw {
		var pair []json.RawMessage
		var id string
		var modified int64
		if json.Unmarshal(v, &pair) != nil || len(pair) != 2 || json.Unmarshal(pair[0], &id) != nil || json.Unmarshal(pair[1], &modified) != nil || id == "" || modified <= 0 {
			return nil, ErrIncomplete
		}
		if _, ok := out[id]; ok {
			return nil, ErrIncomplete
		}
		out[id] = modified
	}
	return out, nil
}

// Snapshot verifies IDs and versions against metadata both before and after the
// full read. Failure or concurrent change never becomes a smaller valid baseline.
func (c *Client) Snapshot(ctx context.Context, key string) ([]Record, error) {
	before, err := c.meta(ctx, key)
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err = c.post(ctx, "datastoreGet", map[string]any{"authKey": key, "collection": "libraryItem", "all": true, "ids": []string{}}, &raw); err != nil {
		return nil, err
	}
	if raw == nil || len(raw) > 20000 {
		return nil, ErrIncomplete
	}
	records := make([]Record, 0, len(raw))
	seen := map[string]int64{}
	for _, v := range raw {
		r, e := parseRecord(v)
		if e != nil {
			return nil, e
		}
		if _, ok := seen[r.ID]; ok {
			return nil, ErrIncomplete
		}
		seen[r.ID] = r.Modified
		records = append(records, r)
	}
	if !reflect.DeepEqual(before, seen) {
		return nil, ErrChanged
	}
	after, err := c.meta(ctx, key)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, ErrChanged
	}
	return records, nil
}
func MovieWatched(r Record) (bool, error) {
	if r.Type != "movie" {
		return false, ErrInvalid
	}
	var count uint32
	raw, ok := r.State["timesWatched"]
	if !ok || string(raw) == "null" || json.Unmarshal(raw, &count) != nil {
		return false, ErrIncomplete
	}
	return count > 0, nil
}

// SnapshotSince avoids full library reads when the accepted record versions are
// unchanged. A changed metadata set still goes through complete snapshot checks.
func (c *Client) SnapshotSince(ctx context.Context, key string, previous []Record) ([]Record, error) {
	current, err := c.meta(ctx, key)
	if err != nil {
		return nil, err
	}
	versions := map[string]int64{}
	for _, r := range previous {
		versions[r.ID] = r.Modified
	}
	if reflect.DeepEqual(current, versions) {
		return previous, nil
	}
	return c.Snapshot(ctx, key)
}
