package addons

import (
	"context"
	"fmt"
	"sync"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
)

const warmLifetime = time.Hour

// A pending selection owns this cache entry. It is not a playback session and
// never emits viewing, recents, local downloads or torrent additions.
type warmEntry struct {
	source  [32]byte
	expires time.Time
	cancel  context.CancelFunc
	p       prepared
	ready   bool
}
type warmCache struct {
	mu      sync.Mutex
	entries map[string]*warmEntry
}

func warmKey(g accounts.NativeGrant, id string) string {
	return fmt.Sprintf("%d/%s/%s/%s", g.User.ID, g.FamilyID, g.DeviceID, id)
}
func (s *Service) DiscardPreparation(g accounts.NativeGrant, id string) {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	key := warmKey(g, id)
	if entry := s.warm.entries[key]; entry != nil {
		entry.cancel()
		entry.p.startup.close()
		delete(s.warm.entries, key)
	}
}
func (s *Service) takeWarm(g accounts.NativeGrant, id string, source Source) (prepared, bool) {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	key := warmKey(g, id)
	entry := s.warm.entries[key]
	if entry == nil || !entry.ready {
		return prepared{}, false
	}
	delete(s.warm.entries, key)
	entry.cancel()
	if time.Now().After(entry.expires) || entry.source != startupKey(prepared{Source: source}) {
		entry.p.startup.close()
		return prepared{}, false
	}
	return entry.p, true
}
func (s *Service) PrepareSource(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, id string) error {
	ctx, cancel := context.WithTimeout(ctx, SessionPreparationTimeout)
	defer cancel()
	v, err := s.selectedForPlayback(ctx, g, media, id)
	if err != nil {
		return err
	}
	// Direct resolver URLs may have provider-side effects. Background reads are
	// restricted to identified torrents already available in our RD account.
	if v.source.InfoHash == "" || s.RD == nil {
		return ErrUnsupported
	}
	entry := &warmEntry{source: startupKey(prepared{Source: v.source}), expires: time.Now().Add(warmLifetime), cancel: cancel}
	key := warmKey(g, id)
	s.warm.mu.Lock()
	if s.warm.entries == nil {
		s.warm.entries = make(map[string]*warmEntry)
	}
	for k, e := range s.warm.entries {
		if time.Now().After(e.expires) {
			e.cancel()
			e.p.startup.close()
			delete(s.warm.entries, k)
		}
	}
	if old := s.warm.entries[key]; old != nil {
		old.cancel()
		old.p.startup.close()
		delete(s.warm.entries, key)
	}
	if len(s.warm.entries) >= startupSessions {
		s.warm.mu.Unlock()
		return ErrUnavailable
	}
	pending := 0
	for _, e := range s.warm.entries {
		if !e.ready {
			pending++
		}
	}
	if pending >= 4 {
		s.warm.mu.Unlock()
		return ErrUnavailable
	}
	s.warm.entries[key] = entry
	s.warm.mu.Unlock()
	success := false
	defer func() {
		if !success {
			s.warm.mu.Lock()
			if s.warm.entries[key] == entry {
				delete(s.warm.entries, key)
			}
			s.warm.mu.Unlock()
		}
	}()
	rd, err := s.RD.Snapshot(ctx)
	if err != nil {
		return err
	}
	scope := rd.CredentialScope()
	want, resolved, err := s.RD.ResolveAvailableSelection(ctx, torrentSelection(v.source))
	if err != nil {
		return err
	}
	source := v.source
	source.URL = resolved.URL
	source.Headers = map[string]string{}
	source.RDFile = &want
	source.RDScope = scope
	source.VideoSize = resolved.Bytes
	p, err := s.preparePlayback(ctx, source)
	defer func() {
		if !success {
			p.startup.close()
		}
	}()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if err = auth.AuthorizeMutation(ctx, tx, g); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM server_addon_accounts WHERE id=$1 FOR SHARE`, v.link.ID); err != nil {
		return err
	}
	if err = fence(ctx, tx, v.link, g.User.ID); err != nil {
		return err
	}
	if err = policyFence(ctx, tx, v.descriptor.PolicyKey()); err != nil {
		return err
	}
	if err = s.RD.ValidateScope(ctx, tx, scope); err != nil {
		return err
	}
	// Keep preparation usable for its lifetime even near picker expiry. Only
	// extend a still-valid authenticated selection; never shorten its lifetime.
	result, err := tx.Exec(ctx, `UPDATE native_addon_sources SET expires_at=GREATEST(expires_at,now()+interval '1 hour') WHERE id=$1 AND user_id=$2 AND expires_at>now()`, id, g.User.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrSelection
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	if s.warm.entries[key] != entry || ctx.Err() != nil {
		return context.Canceled
	}
	entry.p = p
	entry.ready = true
	success = true
	return nil
}
