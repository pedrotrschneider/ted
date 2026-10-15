package addons

import (
	"context"
	"encoding/json"
	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"errors"
	"github.com/jackc/pgx/v5"
	"slices"
	"strings"
)

// Preference identities omit configured URLs, headers and user IDs. Exact
// stream deduplication uses file identity, or an opaque digest of the private
// URL/headers when file identity is unavailable; labels alone prove nothing.
// An add-on's bingeGroup is only meaningful within that add-on and title.
func (d Descriptor) ProviderKey() string { return digest(d.Manifest.ID) }
func sharedKeys(d Descriptor, s Source, media catalog.MediaIdentity) (source, favorite, scope string) {
	identity := s.ReleaseKey()
	if identity == "" {
		headers, _ := json.Marshal(s.Headers)
		identity = digest(strings.Join([]string{s.URL, string(headers), s.Name, s.Description, s.Filename, s.VideoHash, s.BingeGroup}, "\x00"))
	}
	source = digest(d.ProviderKey() + "\x00" + identity)
	scope = media.Kind + ":" + media.ID
	match := "name:" + s.Name
	if s.BingeGroup != "" {
		match = "group:" + s.BingeGroup
		if media.Kind == "episode" {
			scope = "series:" + stringsBeforeColon(media.ID)
		}
	}
	favorite = digest(d.ProviderKey() + "\x00" + match)
	return
}

// Resolve saved favorite providers before fan-out, without exposing another
// account's configured URL or bypassing the current account's enabled list.
func (s *Service) preferredProviders(ctx context.Context, media catalog.MediaIdentity) (map[string]bool, error) {
	scope := media.Kind + ":" + media.ID
	series := scope
	if media.Kind == "episode" {
		series = "series:" + stringsBeforeColon(media.ID)
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ns.descriptor#>>'{manifest,id}'
 FROM native_favorite_sources f JOIN native_addon_sources ns ON ns.favorite_key=f.favorite_key AND ns.favorite_scope=f.scope
 WHERE (f.scope=$1 OR f.scope=$2)`, scope, series)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var provider string
		if err = rows.Scan(&provider); err != nil {
			return nil, err
		}
		if provider != "" {
			result[digest(provider)] = true
		}
	}
	return result, rows.Err()
}
func (s *Service) applyPreferences(ctx context.Context, tx pgx.Tx, page *SourcePage) error {
	scope := page.Media.Kind + ":" + page.Media.ID
	series := "series:" + stringsBeforeColon(page.Media.ID)
	if page.Media.Kind != "episode" {
		series = scope
	}
	rows, err := tx.Query(ctx, `SELECT favorite_key FROM native_favorite_sources WHERE scope=$1 OR scope=$2 ORDER BY updated_at DESC,favorite_key`, scope, series)
	if err != nil {
		return err
	}
	favorites := map[string]int{}
	rank := 0
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		rank++
		favorites[key] = rank
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT source_key FROM native_recent_sources WHERE scope=$1 OR scope=$2 ORDER BY last_used_at DESC,source_key DESC LIMIT 3`, scope, series)
	if err != nil {
		return err
	}
	recent := map[string]int{}
	rank = 0
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		rank++
		recent[key] = rank
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := range page.Sources {
		c := &page.Sources[i]
		c.Favorite = favorites[c.FavoriteKey] > 0
		c.RecentRank = recent[c.FavoriteKey]
	}
	slices.SortStableFunc(page.Sources, func(a, b Choice) int {
		if a.Favorite != b.Favorite {
			if a.Favorite {
				return -1
			}
			return 1
		}
		if a.Favorite {
			return favorites[a.FavoriteKey] - favorites[b.FavoriteKey]
		}
		ar, br := a.RecentRank, b.RecentRank
		if ar == 0 {
			ar = 4
		}
		if br == 0 {
			br = 4
		}
		return ar - br
	})
	for _, c := range page.Sources {
		if c.Favorite || c.RecentRank > 0 {
			page.Preferred = c.ID
			break
		}
	}
	return nil
}

type FavoriteResult struct {
	Favorite bool   `json:"favorite"`
	Key      string `json:"favorite_key"`
}

func (s *Service) SetFavorite(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, sourceID string, wanted bool) (FavoriteResult, error) {
	v, err := s.selected(ctx, g, sourceID)
	if err != nil {
		return FavoriteResult{}, err
	}
	_, descriptors, _, err := s.collection(ctx, g.User.ID, false)
	if err != nil {
		return FavoriteResult{}, err
	}
	installed := false
	for _, d := range descriptors {
		if d.Key() == v.addonKey {
			installed = true
		}
	}
	if !installed {
		return FavoriteResult{}, ErrSelection
	}
	_, key, scope := sharedKeys(v.descriptor, v.source, v.media)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FavoriteResult{}, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, g); err != nil {
		return FavoriteResult{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, v.link.ID); err != nil {
		return FavoriteResult{}, err
	}
	if err = fence(ctx, tx, v.link, g.User.ID); err != nil {
		return FavoriteResult{}, err
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return FavoriteResult{}, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT expires_at>now() FROM native_addon_sources WHERE id=$1 FOR SHARE`, sourceID).Scan(&valid); err != nil {
		return FavoriteResult{}, err
	}
	if !valid {
		return FavoriteResult{}, ErrSelection
	}
	if wanted {
		_, err = tx.Exec(ctx, `INSERT INTO native_favorite_sources(scope,favorite_key) VALUES($1,$2) ON CONFLICT(scope,favorite_key) DO NOTHING`, scope, key)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM native_favorite_sources WHERE scope=$1 AND favorite_key=$2`, scope, key)
	}
	if err != nil {
		return FavoriteResult{}, err
	}
	err = tx.Commit(ctx)
	return FavoriteResult{wanted, key}, err
}

var ErrPolicyConflict = errors.New("add-on settings changed; reload before saving")
