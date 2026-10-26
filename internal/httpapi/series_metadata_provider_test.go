package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"entertainmentdistrict/internal/cinemeta"
	"entertainmentdistrict/internal/metadata"
)

type providerAPIReader struct{ calls atomic.Int32 }

func (*providerAPIReader) Enabled(string) bool { return true }
func (r *providerAPIReader) Fetch(_ context.Context, p string, req metadata.Request) (metadata.Document, error) {
	r.calls.Add(1)
	return metadata.Document{ProviderID: "100", Item: metadata.Item{Name: p + " series"}}, nil
}
func (*providerAPIReader) ResolveCredit(context.Context, string, string, string) (string, error) {
	return "", metadata.ErrMissing
}

func TestSeriesProviderControlIsServerWideAdminOnlyAndFencesStaleConfirmations(t *testing.T) {
	f := setup(t)
	installMetadata(t, f)
	f.api.series = seriesStub{}
	r := &providerAPIReader{}
	var err error
	f.api.Metadata, err = metadata.New(f.pool, r, t.TempDir(), f.api.logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.api.Metadata.Close)
	h := f.api.Handler()
	admin := nativeHTTPLogin(t, h)
	if _, err = f.pool.Exec(context.Background(), `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	memberLogin := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "member", "password": testPassword, "device_id": "provider-member", "device_name": "Provider test"}, nil)
	requireStatus(t, memberLogin, 200)
	var member struct {
		AccessToken string `json:"access_token"`
	}
	if err = json.Unmarshal(memberLogin.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	path := "/api/client/v1/titles/series/tt1245698/metadata-provider"
	requireStatus(t, nativeCall(t, h, "GET", path, "", nil, nil), 401)
	memberChoice := nativeCall(t, h, "GET", path, member.AccessToken, nil, nil)
	requireStatus(t, memberChoice, 200)
	if !strings.Contains(memberChoice.Body.String(), `"can_change":false`) {
		t.Fatal(memberChoice.Body.String())
	}
	body := map[string]string{"provider": "Tmdb", "expected_provider": "Tvdb"}
	requireStatus(t, nativeCall(t, h, "POST", path, member.AccessToken, body, nil), 403)
	if r.calls.Load() != 0 {
		t.Fatal("unauthorized request reached provider")
	}
	requireStatus(t, nativeCall(t, h, "POST", path, admin.AccessToken, map[string]string{"provider": "Other", "expected_provider": "Tvdb"}, nil), 400)
	if r.calls.Load() != 0 {
		t.Fatal("invalid choice reached provider")
	}
	requireStatus(t, nativeCall(t, h, "POST", path, admin.AccessToken, body, nil), 202)
	if r.calls.Load() != 0 {
		t.Fatal("provider change fetched metadata synchronously")
	}
	f.api.series = failingNativeSeries{err: cinemeta.ErrIncomplete}
	current := nativeCall(t, h, "GET", path, member.AccessToken, nil, nil)
	requireStatus(t, current, 200)
	if !strings.Contains(current.Body.String(), `"provider":"Tmdb"`) {
		t.Fatal("choice was account-local", current.Body.String())
	}
	requireStatus(t, nativeCall(t, h, "POST", path, admin.AccessToken, body, nil), 409)
	webAdmin := login(t, h, "admin")
	requireStatus(t, call(t, h, "POST", "/api/admin/series/tt1245698/metadata-provider", map[string]string{"provider": "Tvdb", "expected_provider": "Tmdb"}, webAdmin), 202)
	current = nativeCall(t, h, "GET", path, admin.AccessToken, nil, nil)
	requireStatus(t, current, 200)
	if !strings.Contains(current.Body.String(), `"provider":"Tvdb"`) {
		t.Fatal("web/native policies diverged", current.Body.String())
	}
	// An older/unreviewed request cannot silently retain or remove manual edits.
	if _, err = f.pool.Exec(context.Background(), `UPDATE metadata_entities SET item=item||'{"LockedFields":["Name"],"Name":"Manual"}'::jsonb WHERE kind='series' AND request->>'title_id'='tt1245698'`); err != nil {
		t.Fatal(err)
	}
	rejected := nativeCall(t, h, "POST", path, admin.AccessToken, body, nil)
	requireStatus(t, rejected, 409)
	if !strings.Contains(rejected.Body.String(), `"code":"metadata_overrides_review_required"`) {
		t.Fatal(rejected.Body.String())
	}
	current = nativeCall(t, h, "GET", path, admin.AccessToken, nil, nil)
	requireStatus(t, current, 200)
	var review metadata.SeriesProvider
	if err = json.Unmarshal(current.Body.Bytes(), &review); err != nil || review.Overrides.Metadata != 1 || len(review.Overrides.Version) != 64 {
		t.Fatal(review, err)
	}
	body["override_action"] = "remove"
	body["overrides_version"] = review.Overrides.Version
	requireStatus(t, nativeCall(t, h, "POST", path, admin.AccessToken, body, nil), 202)
	current = nativeCall(t, h, "GET", path, admin.AccessToken, nil, nil)
	requireStatus(t, current, 200)
	if err = json.Unmarshal(current.Body.Bytes(), &review); err != nil || review.Overrides.Metadata != 0 {
		t.Fatal(review, err)
	}
	if r.calls.Load() != 0 {
		t.Fatal("switch fetched metadata eagerly")
	}

	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/titles/movie/tt1245698/metadata-provider", admin.AccessToken, body, nil), 404)
}
