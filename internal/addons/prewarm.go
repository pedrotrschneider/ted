package addons

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/catalog"
	"entertainmentdistrict/internal/database"
	"entertainmentdistrict/internal/realdebrid"
)

const warmLifetime = time.Hour

// A pending selection owns this cache entry. It is not a playback session and
// never emits viewing, recents or local downloads. Explicit playback-next
// requests may separately own a durable exact-file RD job.
type warmEntry struct {
	operation string
	source    [32]byte
	expires   time.Time
	cancel    context.CancelFunc
	p         prepared
	ready     bool
}
type warmCache struct {
	mu      sync.Mutex
	entries map[string]*warmEntry
}

func warmKey(g accounts.NativeGrant, id string) string {
	return fmt.Sprintf("%d/%s/%s/%s", g.User.ID, g.FamilyID, g.DeviceID, id)
}
func (s *Service) DiscardPreparation(g accounts.NativeGrant, id string) {
	s.DiscardPreparationOperation(g, id, "")
}
func (s *Service) DiscardPreparationOperation(g accounts.NativeGrant, id, operation string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.cancelNextFile(ctx, g, id, operation); err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "next file cancellation failed", "operation", "next_episode_cancel")
	}
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	key := warmKey(g, id)
	if entry := s.warm.entries[key]; entry != nil && (operation == "" || entry.operation == operation) {
		entry.cancel()
		entry.p.startup.close()
		delete(s.warm.entries, key)
	}
}
func (s *Service) takeWarm(g accounts.NativeGrant, id string, source Source) (prepared, bool) {
	return s.warmForSession(g, id, source, false)
}
func (s *Service) warmForSession(g accounts.NativeGrant, id string, source Source, keep bool) (prepared, bool) {
	s.warm.mu.Lock()
	defer s.warm.mu.Unlock()
	key := warmKey(g, id)
	entry := s.warm.entries[key]
	if entry == nil || !entry.ready {
		return prepared{}, false
	}
	if !keep {
		delete(s.warm.entries, key)
		entry.cancel()
	}
	if time.Now().After(entry.expires) || entry.source != startupKey(prepared{Source: source}) {
		delete(s.warm.entries, key)
		entry.cancel()
		entry.p.startup.close()
		return prepared{}, false
	}
	return entry.p, true
}
func (s *Service) PrepareSource(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, id string) error {
	return s.PrepareSourceOperation(ctx, auth, g, media, id, "", false)
}
func (s *Service) PrepareSourceOperation(ctx context.Context, auth *accounts.NativeAuth, g accounts.NativeGrant, media catalog.MediaIdentity, id, operation string, allowAdd bool) error {
	ctx, cancel := context.WithTimeout(ctx, SessionPreparationTimeout)
	defer cancel()
	v, err := s.selectedForPlayback(ctx, g, media, id)
	if err != nil {
		return err
	}
	// Direct resolver URLs may have provider-side effects. Background reads are
	// restricted to identified torrents. Browsing remains ready-only; the
	// playback-next workflow may separately queue its requested exact file.
	if v.source.InfoHash == "" || s.RD == nil {
		return ErrUnsupported
	}
	entry := &warmEntry{operation: operation, source: startupKey(prepared{Source: v.source}), expires: time.Now().Add(warmLifetime), cancel: cancel}
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
	if allowAdd && errors.Is(err, realdebrid.ErrPending) {
		if err = s.requestNextFile(ctx, auth, g, v, id, operation); err != nil {
			return err
		}
		return realdebrid.ErrPending
	}
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
