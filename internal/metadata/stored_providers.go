package metadata

import (
	"context"
	"entertainmentdistrict/internal/settings"
	"sync"
	"time"
)

type StoredProviders struct {
	store    *settings.Store
	config   ProviderConfig
	mu       sync.Mutex
	revision int64
	current  *Providers
}

func NewStoredProviders(store *settings.Store, config ProviderConfig) *StoredProviders {
	return &StoredProviders{store: store, config: config}
}
func (s *StoredProviders) snapshot(ctx context.Context) (*Providers, error) {
	c, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || s.revision != c.Revision {
		cfg := s.config
		cfg.TMDBToken, cfg.TVDBKey, cfg.TVDBPIN = c.TMDBToken, c.TVDBKey, c.TVDBPIN
		s.current = NewProviders(cfg)
		s.revision = c.Revision
	}
	return s.current, nil
}
func (s *StoredProviders) Enabled(provider string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, err := s.snapshot(ctx)
	return err == nil && p.Enabled(provider)
}
func (s *StoredProviders) Available(ctx context.Context, provider string) (bool, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return false, err
	}
	return p.Enabled(provider), nil
}
func (s *StoredProviders) Fetch(ctx context.Context, provider string, r Request) (Document, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return Document{}, err
	}
	return p.Fetch(ctx, provider, r)
}
func (s *StoredProviders) ResolveCredit(ctx context.Context, provider, kind, id string) (string, error) {
	p, err := s.snapshot(ctx)
	if err != nil {
		return "", err
	}
	return p.ResolveCredit(ctx, provider, kind, id)
}
