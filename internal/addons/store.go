package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
	"entertainmentdistrict/internal/stremio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SourceLookupTimeout includes collection refresh, bounded parallel lookups and
// authenticated publication. Native HTTP/client deadlines include further margin.
const SourceLookupTimeout = 25 * time.Second
const addonLookupTimeout = 12 * time.Second

type AccountRemote interface {
	AddonCollection(context.Context, string) ([]json.RawMessage, error)
}
type Service struct {
	RD      *realdebrid.Inventory
	pool    *pgxpool.Pool
	remote  AccountRemote
	http    *http.Client
	media   *http.Client
	logger  *slog.Logger
	startup startupCache
	warm    warmCache
}

func New(pool *pgxpool.Pool, remote AccountRemote, client *http.Client, logger *slog.Logger) *Service {
	media := client
	if client == nil {
		client = PublicClient()
		media = mediaClient()
	}
	return &Service{pool: pool, remote: remote, http: client, media: media, logger: logger}
}

func (s *Service) mediaHTTP() *http.Client {
	if s.media != nil {
		return s.media
	}
	return s.http
}
func mediaIdentity(kind, id string) catalog.MediaIdentity {
	return catalog.MediaIdentity{Kind: kind, ID: id}
}

type link struct {
	ID, Account, Key string
	Updated          time.Time
}

func (l link) String() string   { return "Stremio link [credentials redacted]" }
func (l link) GoString() string { return l.String() }
func (s *Service) linked(ctx context.Context, user int64) (link, error) {
	var l link
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,a.account_id,a.auth_key,a.updated_at FROM server_addon_accounts a JOIN users u ON u.id=$1 WHERE a.active AND NOT u.disabled AND a.auth_key!=''`, user).Scan(&l.ID, &l.Account, &l.Key, &l.Updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotLinked
	}
	return l, err
}
func fence(ctx context.Context, tx pgx.Tx, l link, user int64) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM server_addon_accounts a JOIN users u ON u.id=$2 WHERE a.id=$1 AND a.active AND NOT u.disabled AND a.account_id=$3 AND a.updated_at=$4 AND a.auth_key!='')`, l.ID, user, l.Account, l.Updated).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrChanged
	}
	return nil
}

type Issue struct {
	Addon   string `json:"addon"`
	AddonID string `json:"addon_id,omitempty"`
	Code    string `json:"code"`
	Count   int    `json:"count,omitempty"`
}
type Addon struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Movie     bool   `json:"movie"`
	Episode   bool   `json:"episode"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
}
type Collection struct {
	Addons     []Addon   `json:"addons"`
	Cached     bool      `json:"cached"`
	ObservedAt time.Time `json:"observed_at"`
	Issues     []Issue   `json:"issues"`
}

func parseCollection(raw []json.RawMessage) ([]Descriptor, error) {
	if raw == nil || len(raw) > 64 {
		return nil, ErrIncomplete
	}
	values := make([]Descriptor, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		d, err := parseDescriptor(r)
		if err != nil || seen[d.Key()] {
			return nil, ErrIncomplete
		}
		seen[d.Key()] = true
		values = append(values, d)
	}
	return values, nil
}
func (s *Service) collection(ctx context.Context, user int64, force bool) (link, []Descriptor, Collection, error) {
	view := Collection{Addons: []Addon{}, Issues: []Issue{}}
	l, err := s.linked(ctx, user)
	if err != nil {
		return l, nil, view, err
	}
	var cached []byte
	var observed time.Time
	var cachedError string
	err = s.pool.QueryRow(ctx, `SELECT payload,observed_at,error_code FROM server_addon_collections WHERE provider_id=$1 AND account_updated_at=$2`, l.ID, l.Updated).Scan(&cached, &observed, &cachedError)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return l, nil, view, err
	}
	var descriptors []Descriptor
	if len(cached) > 0 {
		var wire []json.RawMessage
		if json.Unmarshal(cached, &wire) != nil {
			return l, nil, view, ErrIncomplete
		}
		descriptors, err = parseCollection(wire)
		if err != nil {
			return l, nil, view, err
		}
	}
	if cachedError != "" {
		view.Cached = true
		view.Issues = append(view.Issues, Issue{Code: "addon_collection_cached"})
	}
	if force || len(cached) == 0 || cachedError == "addon_credentials_rejected" || time.Since(observed) > 5*time.Minute {
		started := time.Now().UTC()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		wire, e := s.remote.AddonCollection(bounded, l.Key)
		cancel()
		var fresh []Descriptor
		if e == nil {
			fresh, e = parseCollection(wire)
		}
		if e != nil {
			if ctx.Err() != nil {
				return l, nil, view, ctx.Err()
			}
			// Credentials rejected must not continue using an old private collection.
			if errors.Is(e, stremio.ErrCredentials) {
				_, saveErr := s.pool.Exec(ctx, `UPDATE server_addon_collections SET error_code='addon_credentials_rejected' WHERE provider_id=$1 AND account_updated_at=$2 AND observed_at<=$3`, l.ID, l.Updated, started)
				if saveErr != nil {
					return l, nil, view, saveErr
				}
				return l, nil, view, ErrCredentials
			}
			if cachedError == "addon_credentials_rejected" {
				return l, nil, view, ErrCredentials
			}
			if len(cached) == 0 {
				return l, nil, view, ErrUnavailable
			}
			view.Cached = true
			if cachedError == "" {
				view.Issues = append(view.Issues, Issue{Code: "addon_collection_cached"})
			}
			_, err = s.pool.Exec(ctx, `UPDATE server_addon_collections SET error_code='addon_collection_unavailable' WHERE provider_id=$1 AND account_updated_at=$2 AND observed_at<=$3`, l.ID, l.Updated, started)
			if err != nil {
				return l, nil, view, err
			}
		} else {
			view.Cached = false
			view.Issues = []Issue{}
			descriptors = fresh
			data, e := json.Marshal(wire)
			if e != nil {
				return l, nil, view, e
			}
			tx, e := s.pool.Begin(ctx)
			if e != nil {
				return l, nil, view, e
			}
			defer database.Rollback(tx)
			// Lock the account after network work, serializing collection publication
			// with unlink/relink and credential changes without blocking remote reads.
			if _, e = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, l.ID); e != nil {
				return l, nil, view, e
			}
			if e = fence(ctx, tx, l, user); e != nil {
				return l, nil, view, e
			}
			_, e = tx.Exec(ctx, `INSERT INTO server_addon_collections(provider_id,account_updated_at,payload,observed_at) VALUES($1,$2,$3,$4) ON CONFLICT(provider_id) DO UPDATE SET account_updated_at=EXCLUDED.account_updated_at,payload=EXCLUDED.payload,observed_at=EXCLUDED.observed_at,error_code='' WHERE server_addon_collections.observed_at<=EXCLUDED.observed_at`, l.ID, l.Updated, data, started)
			if e != nil {
				return l, nil, view, e
			}
			if e = tx.Commit(ctx); e != nil {
				return l, nil, view, e
			}
			// Read the winning publication. An older remote request may finish
			// after a newer one and must not return its stale collection.
			if e = s.pool.QueryRow(ctx, `SELECT payload,observed_at FROM server_addon_collections WHERE provider_id=$1 AND account_updated_at=$2`, l.ID, l.Updated).Scan(&cached, &observed); e != nil {
				return l, nil, view, e
			}
			var winning []json.RawMessage
			if json.Unmarshal(cached, &winning) != nil {
				return l, nil, view, ErrIncomplete
			}
			descriptors, e = parseCollection(winning)
			if e != nil {
				return l, nil, view, e
			}
		}
	}
	// A cached/failed collection read is still fenced at its response boundary.
	current, e := s.linked(ctx, user)
	if e != nil || current.ID != l.ID || current.Account != l.Account || !current.Updated.Equal(l.Updated) {
		return l, nil, view, ErrChanged
	}
	disabled, e := disabledProviders(ctx, s.pool)
	if e != nil {
		return l, nil, view, e
	}
	view.ObservedAt = observed
	for _, d := range descriptors {
		_, e := d.StreamURL(mediaIdentity("movie", "tt1234567"))
		movie, episode := d.Supports(mediaIdentity("movie", "tt1234567")), d.Supports(mediaIdentity("episode", "tt1234567:1:1"))
		view.Addons = append(view.Addons, Addon{ID: d.Key(), Name: label(d.Manifest.Name, 256), Movie: movie, Episode: episode, Supported: e == nil && (movie || episode), Enabled: !disabled[d.PolicyKey()]})
	}
	return l, descriptors, view, nil
}
func (s *Service) Collection(ctx context.Context, user int64, force bool) (Collection, error) {
	_, _, view, err := s.collection(ctx, user, force)
	return view, err
}

type Choice struct {
	PreviousKeys []string `json:"previous_source_keys,omitempty"`
	RDDirect     bool     `json:"rd_direct_play"`
	InRDAccount  bool     `json:"in_rd_account"`
	ID           string   `json:"id"`
	Addon        string   `json:"addon"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	AddonID      string   `json:"addon_id"`
	BingeKey     string   `json:"binge_key,omitempty"`
	SourceKey    string   `json:"source_key"`
	FavoriteKey  string   `json:"favorite_key"`
	Favorite     bool     `json:"favorite"`
	RecentRank   int      `json:"recent_rank"`
}

// Group hints can contain private provider configuration. Return only an
// opaque, add-on-scoped equality key; URLs/credentials never leave the adapter.
func bingeKey(addon, group string) string {
	if group == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(addon + "\x00" + group))
	return hex.EncodeToString(digest[:])
}

type SourcePage struct {
	Media      catalog.MediaIdentity `json:"media"`
	Sources    []Choice              `json:"sources"`
	Issues     []Issue               `json:"issues"`
	AutoSelect string                `json:"auto_select,omitempty"`
	Preferred  string                `json:"preferred_source,omitempty"`
	Addons     []Addon               `json:"addons"`
}

func (s *Service) Sources(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, previous string) (SourcePage, error) {
	return s.sources(ctx, auth, g, media, previous, false, "")
}

// SelectedSources permits an explicit Stremio choice without changing a saved
// mapping. The default discovery workflow still never falls through a mapping.
func (s *Service) SelectedSources(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, previous string) (SourcePage, error) {
	return s.sources(ctx, auth, g, media, previous, true, "")
}

func (s *Service) SelectedSourcesPrioritizing(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, previous, preferred string) (SourcePage, error) {
	return s.sources(ctx, auth, g, media, previous, true, preferred)
}

func (s *Service) sources(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, previous string, selected bool, preferred string) (SourcePage, error) {
	page := SourcePage{Media: media, Sources: []Choice{}, Issues: []Issue{}}
	if !media.Valid() {
		return page, ErrUnsupported
	}
	var mapped bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_media c JOIN media_mappings m ON m.movie_id=c.id WHERE c.kind=$1 AND c.cinemeta_id=$2)`, media.Kind, media.ID).Scan(&mapped); err != nil {
		return page, err
	}
	if mapped && !selected {
		return page, ErrSelection
	}
	// Reserve five seconds for authenticated persistence after network work.
	// Collection refresh must not consume most of a provider's lookup budget.
	network, stopNetwork := context.WithTimeout(ctx, SourceLookupTimeout-5*time.Second)
	defer stopNetwork()
	l, descriptors, collection, err := s.collection(network, g.User.ID, false)
	if err != nil {
		return page, err
	}
	page.Issues = append(page.Issues, collection.Issues...)
	page.Addons = collection.Addons
	disabled, err := disabledProviders(ctx, s.pool)
	if err != nil {
		return page, err
	}
	type response struct {
		d           Descriptor
		sources     []Source
		unsupported int
		err         error
	}
	results := make([]response, len(descriptors))
	var workers sync.WaitGroup
	lookup, cancel := context.WithCancel(network)
	defer cancel()
	priority, err := s.preferredProviders(ctx, media)
	if err != nil {
		return page, err
	}
	order := []int{}
	for i, d := range descriptors {
		results[i].d = d
		if !d.Supports(media) || disabled[d.PolicyKey()] {
			continue
		}
		order = append(order, i)
	}
	rank := func(d Descriptor) int {
		if preferred != "" && d.Key() == preferred {
			return 0
		}
		if priority[d.ProviderKey()] {
			return 1
		}
		return 2
	}
	slices.SortStableFunc(order, func(a, b int) int { return rank(descriptors[a]) - rank(descriptors[b]) })
	work := make(chan int, len(order))
	for _, i := range order {
		work <- i
	}
	close(work)
	// Queue before launching the fixed workers. Racing one goroutine per
	// add-on for a semaphore could give a continuation/favorite the last slot
	// after slow unrelated add-ons consumed its lookup deadline.
	for worker := 0; worker < min(4, len(order)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range work {
				d := descriptors[i]
				if lookup.Err() != nil {
					results[i].err = ErrUnavailable
					continue
				}
				off, e := disabledProviders(lookup, s.pool)
				if e != nil {
					results[i].err = e
					continue
				}
				if off[d.PolicyKey()] {
					continue
				}
				bounded, stop := context.WithTimeout(lookup, addonLookupTimeout)
				results[i].sources, results[i].unsupported, results[i].err = s.fetchStreams(bounded, d, media.Kind, media.ID)
				stop()
			}
		}()
	}
	workers.Wait()
	if ctx.Err() != nil {
		return page, ctx.Err()
	}
	availability := map[string]bool{}
	rdScope := ""
	if s.RD != nil {
		selections := []realdebrid.TorrentSelection{}
		keys := []string{}
		for _, r := range results {
			for _, source := range r.sources {
				if source.InfoHash != "" {
					selections = append(selections, torrentSelection(source))
					keys = append(keys, source.ReleaseKey())
				}
			}
		}
		flags, scope, e := s.RD.ReadyFor(ctx, selections)
		if e != nil {
			return page, e
		}
		rdScope = scope
		for i, key := range keys {
			availability[key] = flags[i]
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return page, err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, g); err != nil {
		return page, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, l.ID); err != nil {
		return page, err
	}
	if err = fence(ctx, tx, l, g.User.ID); err != nil {
		return page, err
	}
	// Recover already persisted download selections when moving a recognized
	// direct-link release to RD identity. Only retained hash + exact filename/size
	// evidence in this account/media/add-on scope can authorize an old opaque key.
	previousKeys := map[string][]string{}
	oldRows, e := tx.Query(ctx, `SELECT addon_key,shared_source_key,source FROM native_addon_sources WHERE user_id=$1 AND provider_id=$2 AND account_updated_at=$3 AND media_kind=$4 AND media_identity=$5 AND NOT(source ? 'info_hash')`, g.User.ID, l.ID, l.Updated, media.Kind, media.ID)
	if e != nil {
		return page, e
	}
	for oldRows.Next() {
		var addon, key string
		var raw []byte
		if e = oldRows.Scan(&addon, &key, &raw); e != nil {
			break
		}
		var old Source
		if json.Unmarshal(raw, &old) != nil {
			e = ErrIncomplete
			break
		}
		old.InfoHash = providerBingeHash(old.BingeGroup)
		if key == "" || old.InfoHash == "" || old.Filename == "" {
			continue
		}
		match := addon + ":" + old.ReleaseKey()
		if !slices.Contains(previousKeys[match], key) {
			previousKeys[match] = append(previousKeys[match], key)
		}
	}
	oldRows.Close()
	if e != nil {
		return page, e
	}
	if e = oldRows.Err(); e != nil {
		return page, e
	}
	var previousKey, previousGroup, previousIdentity string
	if previous != "" {
		if !nativeUUID.MatchString(previous) {
			return page, ErrSelection
		}
		err = tx.QueryRow(ctx, `SELECT ns.addon_key,ns.source->>'binge_group',c.cinemeta_id FROM native_playback_sessions p JOIN native_addon_sources ns ON ns.id=p.addon_source_id JOIN catalog_media c ON c.id=p.media_id WHERE p.id=$1 AND p.user_id=$2 AND p.device_id=$3 AND ns.provider_id=$4 AND ns.account_updated_at=$5`, previous, g.User.ID, g.DeviceID, l.ID, l.Updated).Scan(&previousKey, &previousGroup, &previousIdentity)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return page, err
		}
		if media.Kind != "episode" || media.ID == previousIdentity || stringsBeforeColon(media.ID) != stringsBeforeColon(previousIdentity) {
			previousGroup = ""
		}
	}
	if err = policyLock(ctx, tx, false); err != nil {
		return page, err
	}
	disabled, err = disabledProviders(ctx, tx)
	if err != nil {
		return page, err
	}
	for i := range page.Addons {
		for _, d := range descriptors {
			if page.Addons[i].ID == d.Key() {
				page.Addons[i].Enabled = !disabled[d.PolicyKey()]
			}
		}
	}
	matches := []string{}
	previousMatches := 0
	seen := map[string]bool{}
	for _, r := range results {
		if disabled[r.d.PolicyKey()] {
			continue
		}
		addonName := label(r.d.Manifest.Name, 256)
		if r.err != nil {
			page.Issues = append(page.Issues, Issue{Addon: addonName, AddonID: r.d.Key(), Code: "addon_unavailable"})
			continue
		}
		if r.unsupported > 0 {
			page.Issues = append(page.Issues, Issue{Addon: addonName, AddonID: r.d.Key(), Code: "unsupported_sources", Count: r.unsupported})
		}
		descriptor, _ := json.Marshal(r.d)
		for _, source := range r.sources {
			if len(page.Sources) >= 200 {
				page.Issues = append(page.Issues, Issue{Addon: addonName, AddonID: r.d.Key(), Code: "source_limit"})
				break
			}
			if previousGroup != "" && previousKey == r.d.Key() && previousGroup == source.BingeGroup {
				previousMatches++
			}
			key, favorite, scope := sharedKeys(r.d, source, media)
			if seen[key] {
				continue
			}
			seen[key] = true
			data, _ := json.Marshal(source)
			var id string
			err = tx.QueryRow(ctx, `INSERT INTO native_addon_sources(user_id,provider_id,account_id,account_updated_at,media_kind,media_identity,addon_key,descriptor,source,shared_source_key,favorite_key,favorite_scope,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now()+interval '13 hours') RETURNING id::text`, g.User.ID, l.ID, l.Account, l.Updated, media.Kind, media.ID, r.d.Key(), descriptor, data, key, favorite, scope).Scan(&id)
			if err != nil {
				return page, err
			}
			name := source.Name
			if name == "" {
				name = addonName
			}
			inAccount := availability[source.ReleaseKey()]
			page.Sources = append(page.Sources, Choice{ID: id, Addon: addonName, Name: name, Description: source.Description, AddonID: r.d.Key(), BingeKey: bingeKey(r.d.Key(), source.BingeGroup), SourceKey: key, FavoriteKey: favorite, RDDirect: source.InfoHash != "", InRDAccount: inAccount, PreviousKeys: previousKeys[r.d.Key()+":"+source.ReleaseKey()]})
			if previousGroup != "" && previousKey == r.d.Key() && previousGroup == source.BingeGroup {
				matches = append(matches, id)
			}
		}
	}
	if err = s.applyPreferences(ctx, tx, &page); err != nil {
		return page, err
	}
	if len(matches) == 1 && previousMatches == 1 {
		page.AutoSelect = matches[0]
	}
	if rdScope != "" {
		if err = s.RD.ValidateScope(ctx, tx, rdScope); err != nil {
			return page, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return page, err
	}
	return page, nil
}
