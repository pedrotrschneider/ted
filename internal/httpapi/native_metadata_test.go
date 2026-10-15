package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/metadata"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"strings"
	"testing"
)

type metadataReader struct{}

func (metadataReader) Enabled(string) bool { return true }
func (metadataReader) Fetch(context.Context, string, metadata.Request) (metadata.Document, error) {
	return metadata.Document{}, metadata.ErrProvider
}
func (metadataReader) ResolveCredit(context.Context, string, string, string) (string, error) {
	return "", metadata.ErrProvider
}
func installMetadata(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	var schema string
	if err := f.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(f.pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	f.api.Metadata, err = metadata.New(f.pool, metadataReader{}, t.TempDir(), f.api.logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.api.Metadata.Close)
}
func TestNativeMetadataAsyncAuthenticationAndAdminRefresh(t *testing.T) {
	f := setup(t)
	installMetadata(t, f)
	f.api.movies = &movieStub{}
	h := f.api.Handler()
	pair := nativeHTTPLogin(t, h)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/metadata/configuration", "", nil, nil), 401)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/metadata/configuration", pair.AccessToken, nil, nil), 200)
	detail := nativeCall(t, h, "GET", "/api/client/v1/titles/movie/tt1345836", pair.AccessToken, nil, nil)
	requireStatus(t, detail, 200)
	var v nativeTitleDetail
	if err := json.Unmarshal(detail.Body.Bytes(), &v); err != nil || v.Enrichment == nil || v.Enrichment.Status != "queued" || v.Enrichment.Item.Name == "" {
		t.Fatal("detail waited for enrichment or lost fallback", detail.Body.String(), err)
	}
	id := v.Enrichment.Item.ID
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/metadata/items/"+id, pair.AccessToken, nil, nil), 200)
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/metadata/people/"+id, pair.AccessToken, nil, nil), 400)
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	r := nativeCall(t, h, "POST", "/api/client/v1/auth/login", "", map[string]string{"username": "member", "password": testPassword, "device_id": "member-device", "device_name": "Member test device"}, nil)
	requireStatus(t, r, 200)
	var member struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, nativeCall(t, h, "POST", "/api/client/v1/metadata/items/"+id+"/refresh", member.AccessToken, map[string]any{}, nil), 403)
	refresh := nativeCall(t, h, "POST", "/api/client/v1/metadata/items/"+id+"/refresh", pair.AccessToken, map[string]any{}, nil)
	requireStatus(t, refresh, 200)
	if !strings.Contains(refresh.Body.String(), `"generation":2`) {
		t.Fatal("refresh did not persist a newer generation", refresh.Body.String())
	}
	requireStatus(t, nativeCall(t, h, "GET", "/api/client/v1/metadata/items/00000000-0000-0000-0000-000000000001", pair.AccessToken, nil, nil), 404)
}
