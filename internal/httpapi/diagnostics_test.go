package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/diagnostics"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func diagnosticBatch(id string, n int) diagnostics.Batch {
	v := diagnostics.Batch{ID: id, DeviceName: "Living room", Platform: "android", AppVersion: "0.3.18", Entries: []diagnostics.Entry{}}
	for i := 0; i < n; i++ {
		eid := "11111111-1111-4111-8111-111111111111"
		if i > 0 {
			eid = "22222222-2222-4222-8222-222222222222"
		}
		v.Entries = append(v.Entries, diagnostics.Entry{ID: eid, At: time.Now().UTC(), Level: "warning", Component: "player", Message: "timeout https://private.invalid/file?secret=abc Authorization: Bearer super-secret", SessionID: "33333333-3333-4333-8333-333333333333"})
	}
	return v
}
func TestDiagnosticsGzipIdentityAdminReadAndRedaction(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	member, err := f.service.Create(ctx, identity(f.admin), "viewer", "Viewer", testPassword, "user")
	if err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	loginResult := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": member.Username, "password": testPassword, "device_id": "television", "device_name": "Living room"}, nil)
	requireStatus(t, loginResult, 200)
	var token accounts.NativeTokens
	if err = json.Unmarshal(loginResult.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	payload := diagnosticBatch("44444444-4444-4444-8444-444444444444", 2)
	raw, _ := json.Marshal(payload)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	r := httptest.NewRequest("POST", "https://ted.example/api/client/v1/diagnostics/batches", &compressed)
	r.Header.Set("Authorization", "Bearer "+token.AccessToken)
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set("X-Teddy-Compatibility-Version", "0.3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireStatus(t, w, 200)
	for _, path := range []string{"/api/client/v1/admin/logs", "/api/client/v1/admin/logs/devices", "/api/client/v1/admin/logs/settings"} {
		requireStatus(t, nativeCall(t, h, "GET", path, token.AccessToken, nil, nil), 403)
	}
	memberCookie := login(t, h, member.Username)
	requireStatus(t, call(t, h, "GET", "/api/admin/logs", nil, memberCookie), 403)
	requireStatus(t, call(t, h, "GET", "/api/admin/logs", nil, nil), 401)
	admin := nativeHTTPLogin(t, h)
	result := nativeCall(t, h, "GET", "/api/client/v1/admin/logs?level=warning&q=timeout", admin.AccessToken, nil, nil)
	requireStatus(t, result, 200)
	var page diagnostics.Page
	if err = json.Unmarshal(result.Body.Bytes(), &page); err != nil || len(page.Entries) != 2 {
		t.Fatalf("query %v %s", err, result.Body.String())
	}
	for _, e := range page.Entries {
		if e.UserID != member.ID || e.DeviceID != "television" || strings.Contains(e.Message, "private.invalid") || strings.Contains(e.Message, "super-secret") {
			t.Fatal("unsafe identity or diagnostic content")
		}
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/diagnostics/batches", token.AccessToken, payload, nil), 200)
	var count int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM client_log_batches").Scan(&count); err != nil || count != 1 {
		t.Fatal("lost-ack duplicate", count, err)
	}
	payload.Entries[0].Message = "different"
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/diagnostics/batches", token.AccessToken, payload, nil), 409)
}
func TestDiagnosticsPaginationRetentionAndScope(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	batch := diagnosticBatch("44444444-4444-4444-8444-444444444444", 2)
	for _, id := range []string{"one", "two"} {
		if _, err := f.api.Logs.Ingest(ctx, f.admin.ID, id, batch); err != nil {
			t.Fatal(err)
		}
	}
	q := diagnostics.Query{Limit: 1, Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), DeviceID: "one"}
	a, err := f.api.Logs.Query(ctx, q)
	if err != nil || len(a.Entries) != 1 || a.NextCursor == "" {
		t.Fatal(a, err)
	}
	q.Cursor = a.NextCursor
	b, err := f.api.Logs.Query(ctx, q)
	if err != nil || len(b.Entries) != 1 || b.Entries[0].ID == a.Entries[0].ID {
		t.Fatal("cursor skipped or repeated entry", b, err)
	}
	settings, err := f.api.Logs.Settings(ctx)
	if err != nil || settings.RetentionDays != 90 {
		t.Fatal(settings, err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE client_log_batches SET received_at=now()-interval '10 days' WHERE device_id='one'"); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	admin := nativeHTTPLogin(t, h)
	requireStatus(t, nativeCall(t, h, "PUT", "/api/client/v1/admin/logs/settings", admin.AccessToken, map[string]int{"retention_days": 0}, nil), 400)
	requireStatus(t, nativeCall(t, h, "PUT", "/api/client/v1/admin/logs/settings", admin.AccessToken, map[string]int{"retention_days": 7}, nil), 200)
	var count int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM client_log_batches").Scan(&count)
	if count != 1 {
		t.Fatal("retention did not prune expired batches", count)
	}
	browser := login(t, h, "admin")
	result := call(t, h, "GET", "/api/admin/logs?format=ndjson", nil, browser)
	requireStatus(t, result, 200)
	if !strings.HasPrefix(result.Header().Get("Content-Type"), "application/x-ndjson") || !strings.Contains(result.Body.String(), "timeout") {
		t.Fatal("invalid diagnostic export")
	}
}
func TestDiagnosticsRejectOversizeAndForgedIdentity(t *testing.T) {
	f := setup(t)
	h := f.api.Handler()
	token := nativeHTTPLogin(t, h)
	v := diagnosticBatch("44444444-4444-4444-8444-444444444444", 1)
	raw, _ := json.Marshal(v)
	var forged map[string]any
	_ = json.Unmarshal(raw, &forged)
	forged["user_id"] = 999
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/diagnostics/batches", token.AccessToken, forged, nil), 400)
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), diagnostics.MaxBatchBytes+1))
	_ = zw.Close()
	r := httptest.NewRequest("POST", "https://ted.example/api/client/v1/diagnostics/batches", &bomb)
	r.Header.Set("Authorization", "Bearer "+token.AccessToken)
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set("X-Teddy-Compatibility-Version", "0.3")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	requireStatus(t, w, 413)
}

func TestDiagnosticsConcurrentReceiptAndEvictionRecovery(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	held, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	if _, err = held.Exec(ctx, "SELECT pg_advisory_xact_lock(84051051)"); err != nil {
		t.Fatal(err)
	}
	batch := diagnosticBatch("44444444-4444-4444-8444-444444444444", 1)
	type result struct {
		duplicate bool
		err       error
	}
	done := make(chan result, 2)
	for range 2 {
		go func() {
			duplicate, err := f.api.Logs.Ingest(ctx, f.admin.ID, "television", batch)
			done <- result{duplicate, err}
		}()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case value := <-done:
			t.Fatalf("ingest escaped held durable lock: %v", value.err)
		case <-ctx.Done():
			t.Fatal("workers did not reach held lock")
		case <-ticker.C:
		}
	}
	if err = held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := <-done, <-done
	if a.err != nil || b.err != nil || a.duplicate == b.duplicate {
		t.Fatalf("concurrent retry results: %+v %+v", a, b)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE client_log_batches SET stored_bytes=$1", diagnostics.MaxDeviceBytes); err != nil {
		t.Fatal(err)
	}
	next := diagnosticBatch("55555555-5555-4555-8555-555555555555", 1)
	if _, err = f.api.Logs.Ingest(ctx, f.admin.ID, "television", next); err != nil {
		t.Fatal(err)
	}
	var evicted bool
	if err = f.pool.QueryRow(ctx, "SELECT payload IS NULL FROM client_log_batches WHERE batch_id=$1", batch.ID).Scan(&evicted); err != nil || !evicted {
		t.Fatal("storage pressure did not evict old body", err)
	}
	duplicate, err := f.api.Logs.Ingest(ctx, f.admin.ID, "television", batch)
	if err != nil || !duplicate {
		t.Fatal("lost acknowledgment resurrected evicted log body", err)
	}
}
