package httpapi

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/addons"
	"strings"
	"testing"
)

func TestAdminAddonPolicyHTTPAuthorizationRevisionAndRedaction(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wire := `[{"transportUrl":"https://example.com/private-config/manifest.json","manifest":{"id":"fixture","name":"Fixture","resources":["stream"],"types":["movie"]}}]`
	if _, err := f.pool.Exec(ctx, `INSERT INTO server_addon_accounts(account_id,email,auth_key) VALUES('policy','private@example.com','private-key')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO server_addon_collections(provider_id,account_updated_at,payload,observed_at) SELECT id,updated_at,$1,now() FROM server_addon_accounts WHERE account_id='policy'`, wire); err != nil {
		t.Fatal(err)
	}
	h := f.api.Handler()
	admin := login(t, h, "admin")
	requireStatus(t, call(t, h, "GET", "/api/admin/addons", nil, nil), 401)
	r := call(t, h, "GET", "/api/admin/addons", nil, admin)
	requireStatus(t, r, 200)
	var page struct{ Addons []addons.Policy }
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Addons) != 1 {
		t.Fatal(err, r.Body.String())
	}
	p := page.Addons[0]
	if !p.Enabled || p.Revision != 0 || strings.Contains(r.Body.String(), "private") {
		t.Fatal("private collection disclosed or initial state wrong")
	}
	requireStatus(t, call(t, h, "PUT", "/api/admin/addons", map[string]any{"id": p.ID, "enabled": false, "revision": 0}, admin), 200)
	requireStatus(t, call(t, h, "PUT", "/api/admin/addons", map[string]any{"id": p.ID, "enabled": true, "revision": 0}, admin), 409)
	requireStatus(t, call(t, h, "PUT", "/api/admin/addons", map[string]any{"id": p.ID, "revision": 1}, admin), 400)
	if _, err := f.pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash,role) SELECT 'member','Member',password_hash,'user' FROM users WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	member := login(t, h, "member")
	requireStatus(t, call(t, h, "GET", "/api/admin/addons", nil, member), 403)
	requireStatus(t, call(t, h, "PUT", "/api/admin/addons", map[string]any{"id": p.ID, "enabled": true, "revision": 1}, member), 403)
}
