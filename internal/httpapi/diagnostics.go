package httpapi

import (
	"compress/gzip"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/diagnostics"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *API) diagnosticNativeRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/client/v1/diagnostics/batches", a.nativeProtected(a.uploadLogs))
	for _, path := range []string{"/api/client/v1/admin/logs", "/api/client/v1/admin/logs/devices", "/api/client/v1/admin/logs/settings"} {
		m.HandleFunc("GET "+path, a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
			if g.User.Role != "admin" {
				a.nativeFail(w, r, accounts.ErrForbidden)
				return
			}
			a.readLogs(w, r, g.User)
		}))
	}
	m.HandleFunc("PUT /api/client/v1/admin/logs/settings", a.nativeProtected(func(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
		if g.User.Role != "admin" {
			a.nativeFail(w, r, accounts.ErrForbidden)
			return
		}
		a.readLogs(w, r, g.User)
	}))
}
func (a *API) uploadLogs(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	r.Body = http.MaxBytesReader(w, r.Body, diagnostics.MaxBatchBytes)
	var reader io.Reader = r.Body
	if enc := r.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		if enc != "gzip" {
			respond(w, 415, map[string]string{"error": "diagnostics_invalid"})
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			respond(w, 400, map[string]string{"error": "diagnostics_invalid"})
			return
		}
		defer zr.Close()
		reader = zr
	}
	raw, err := io.ReadAll(io.LimitReader(reader, diagnostics.MaxBatchBytes+1))
	if err != nil || len(raw) > diagnostics.MaxBatchBytes {
		respond(w, 413, map[string]string{"error": "diagnostics_invalid"})
		return
	}
	var batch diagnostics.Batch
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&batch) != nil || decoder.Decode(new(any)) != io.EOF {
		respond(w, 400, map[string]string{"error": "diagnostics_invalid"})
		return
	}
	duplicate, err := a.Logs.Ingest(r.Context(), g.User.ID, g.DeviceID, batch)
	if err != nil {
		a.logFailure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"batch_id": batch.ID, "accepted": true, "duplicate": duplicate})
}
func (a *API) logFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, diagnostics.ErrInvalid):
		respond(w, 400, map[string]string{"error": "diagnostics_invalid"})
	case errors.Is(err, diagnostics.ErrConflict):
		respond(w, 409, map[string]string{"error": "diagnostics_conflict"})
	case errors.Is(err, diagnostics.ErrLimited):
		w.Header().Set("Retry-After", "60")
		respond(w, 429, map[string]string{"error": "diagnostics_limited"})
	default:
		a.fail(w, r, err)
	}
}
func (a *API) readLogs(w http.ResponseWriter, r *http.Request, _ accounts.User) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasSuffix(r.URL.Path, "/settings") {
		if r.Method == "PUT" {
			var v struct {
				Days int `json:"retention_days"`
			}
			if !decode(w, r, &v) {
				return
			}
			if err := a.Logs.SetRetention(r.Context(), v.Days); err != nil {
				a.logFailure(w, r, err)
				return
			}
		}
		v, err := a.Logs.Settings(r.Context())
		if err != nil {
			a.logFailure(w, r, err)
			return
		}
		respond(w, 200, v)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/devices") {
		v, err := a.Logs.Devices(r.Context())
		if err != nil {
			a.logFailure(w, r, err)
			return
		}
		respond(w, 200, map[string]any{"devices": v})
		return
	}
	v := r.URL.Query()
	q := diagnostics.Query{Limit: 200, Since: time.Now().UTC().Add(-24 * time.Hour), Until: time.Now().UTC().Add(time.Minute), DeviceID: v.Get("device_id"), Level: v.Get("level"), Component: v.Get("component"), SessionID: v.Get("session_id"), Platform: v.Get("platform"), AppVersion: v.Get("app_version"), Text: v.Get("q"), Cursor: v.Get("cursor")}
	var err error
	for _, item := range []struct {
		key    string
		target *int64
	}{{"user_id", &q.UserID}, {"after_seq", &q.AfterSeq}} {
		if v.Get(item.key) != "" {
			*item.target, err = strconv.ParseInt(v.Get(item.key), 10, 64)
			if err != nil {
				a.logFailure(w, r, diagnostics.ErrInvalid)
				return
			}
		}
	}
	if v.Get("limit") != "" {
		q.Limit, err = strconv.Atoi(v.Get("limit"))
		if err != nil {
			a.logFailure(w, r, diagnostics.ErrInvalid)
			return
		}
	}
	for _, item := range []struct {
		key    string
		target *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if v.Get(item.key) != "" {
			*item.target, err = time.Parse(time.RFC3339Nano, v.Get(item.key))
			if err != nil {
				a.logFailure(w, r, diagnostics.ErrInvalid)
				return
			}
		}
	}
	page, err := a.Logs.Query(r.Context(), q)
	if err != nil {
		a.logFailure(w, r, err)
		return
	}
	if v.Get("format") == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", "attachment; filename=teddy-logs.ndjson")
		w.Header().Set("X-Next-Cursor", page.NextCursor)
		w.Header().Set("X-Log-Head-Seq", strconv.FormatInt(page.HeadSeq, 10))
		enc := json.NewEncoder(w)
		for _, entry := range page.Entries {
			if enc.Encode(entry) != nil {
				return
			}
		}
		return
	}
	respond(w, 200, page)
}
