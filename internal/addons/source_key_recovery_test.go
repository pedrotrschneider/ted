package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestOldDownloadKeyRequiresRetainedHashFileAndAccountMediaScope(t *testing.T) {
	s, auth, g, remote, _ := fixture(t)
	ctx := context.Background()
	hash := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"streams": []any{map[string]any{"infoHash": hash, "behaviorHints": map[string]any{"bingeGroup": "torrentio|" + hash, "filename": "episode.mkv", "videoSize": 1000}}}})
	}))
	defer server.Close()
	raw := descriptorRaw(server.URL)
	d, e := parseDescriptor(raw)
	if e != nil {
		t.Fatal(e)
	}
	remote.collection = []json.RawMessage{raw}
	s.http = server.Client()
	for _, v := range []struct{ key, group, kind, id string }{{strings.Repeat("b", 64), "torrentio|" + hash, "movie", "tt1234567"}, {strings.Repeat("c", 64), "torrentio|" + strings.Repeat("d", 40), "movie", "tt1234567"}, {strings.Repeat("e", 64), "torrentio|" + hash, "episode", "tt1234567:1:1"}} {
		old, _ := json.Marshal(Source{URL: "https://example.com/old-private-link", Filename: "episode.mkv", VideoSize: 1000, BingeGroup: v.group})
		if _, e = s.pool.Exec(ctx, `INSERT INTO native_addon_sources(user_id,provider_id,account_id,account_updated_at,media_kind,media_identity,addon_key,descriptor,source,shared_source_key) SELECT $1,a.id,a.account_id,a.updated_at,$2,$3,$4,$5,$6,$7 FROM server_addon_accounts a WHERE a.active`, g.User.ID, v.kind, v.id, d.Key(), raw, old, v.key); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.SelectedSources(ctx, auth, g, mediaIdentity("movie", "tt1234567"), "")
	if e != nil || len(page.Sources) != 1 {
		t.Fatal(e)
	}
	keys := page.Sources[0].PreviousKeys
	if !slices.Contains(keys, strings.Repeat("b", 64)) || slices.Contains(keys, strings.Repeat("c", 64)) || slices.Contains(keys, strings.Repeat("e", 64)) {
		t.Fatal("selection recovery changed a release or media scope", keys)
	}
	public, _ := json.Marshal(page)
	if strings.Contains(string(public), hash) || strings.Contains(string(public), "old-private-link") {
		t.Fatal("private source provenance escaped")
	}
}
