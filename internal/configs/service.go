package configs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/yogasw/wick/internal/entity"

	"gorm.io/gorm"
)

// Service exposes typed, cached access to runtime variables. The cache
// is loaded once at startup by Bootstrap() and refreshed on every
// Set() / Regenerate() call, so reads never touch the DB.
type Service struct {
	repo  *repo
	mu    sync.RWMutex
	cache map[ownerKey]entity.Config
	// meta tracks every declared row — app-level + module-declared —
	// keyed by (owner, key). It lets Missing() answer without re-
	// walking the registries.
	meta map[ownerKey]entity.Config
	// declOrder preserves the per-owner declaration order so ListOwned
	// returns rows in the same sequence as the module's Config struct.
	declOrder map[string][]string
}

// ownerKey is the composite cache key. Owner scopes the variable
// (app, tool, job); Key is the field name within that scope.
type ownerKey struct{ Owner, Key string }

func NewService(db *gorm.DB) *Service {
	return &Service{
		repo:      newRepo(db),
		cache:     make(map[ownerKey]entity.Config),
		meta:      make(map[ownerKey]entity.Config),
		declOrder: make(map[string][]string),
	}
}

// Bootstrap reconciles the given extras plus the app-level defaults
// with the DB and seeds the in-memory cache. Pass module-declared
// Configs collected from every registered tool/job — wick assigns
// Owner before handing them off. Call once at startup, before
// anything else reads config.
func (s *Service) Bootstrap(ctx context.Context, extras ...entity.Config) error {
	all := append([]entity.Config{}, appDefaults()...)
	all = append(all, extras...)
	for _, row := range all {
		if err := s.reconcile(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcile(ctx context.Context, row entity.Config) error {
	existing, err := s.repo.FindByOwnerKey(ctx, row.Owner, row.Key)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load config %s/%s: %w", row.Owner, row.Key, err)
	}
	// Fill Value from generator if the row is missing and a generator
	// exists for this key (app-level secrets). Otherwise keep whatever
	// the caller seeded in row.Value.
	value := row.Value
	if existing == nil {
		if gen, ok := generators[row.Key]; ok && row.Owner == "" {
			value = gen()
		}
		row.Value = value
	}
	if err := s.repo.UpsertMeta(ctx, &row); err != nil {
		return fmt.Errorf("upsert config %s/%s: %w", row.Owner, row.Key, err)
	}
	fresh, err := s.repo.FindByOwnerKey(ctx, row.Owner, row.Key)
	if err != nil {
		return fmt.Errorf("reload config %s/%s: %w", row.Owner, row.Key, err)
	}
	// UpsertMeta skips the value column on existing rows — restore the
	// seed so auto-generated secrets land in the cache even when the
	// row already existed (Bootstrap only runs the generator when
	// nothing was stored yet).
	if existing == nil {
		fresh.Value = value
		_ = s.repo.SetValue(ctx, row.Owner, row.Key, value)
	}
	k := ownerKey{Owner: row.Owner, Key: row.Key}
	s.mu.Lock()
	s.cache[k] = *fresh
	s.meta[k] = row
	// Track declaration order — append only on first encounter so
	// re-reconciliation (e.g. Set) does not duplicate the key.
	found := false
	for _, existing := range s.declOrder[row.Owner] {
		if existing == row.Key {
			found = true
			break
		}
	}
	if !found {
		s.declOrder[row.Owner] = append(s.declOrder[row.Owner], row.Key)
	}
	s.mu.Unlock()
	return nil
}

// Get returns the cached value for the app-level key (Owner=="").
// Callers should prefer typed accessors (AppURL, SessionSecret) so
// renames are compiler-enforced.
func (s *Service) Get(key string) string {
	return s.GetOwned("", key)
}

// GetOwned returns the cached value for (owner, key), or empty string
// if missing. Tool/job handlers use this via Ctx helpers; cross-owner
// reads are allowed but should be rare and intentional.
func (s *Service) GetOwned(owner, key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[ownerKey{Owner: owner, Key: key}].Value
}

// ListOwned returns every config scoped to owner in declaration order.
func (s *Service) ListOwned(owner string) []entity.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := s.declOrder[owner]
	out := make([]entity.Config, 0, len(keys))
	for _, key := range keys {
		if v, ok := s.cache[ownerKey{Owner: owner, Key: key}]; ok {
			out = append(out, v)
		}
	}
	return out
}

// List returns every app-level variable. Kept for backward
// compatibility with the admin settings page.
func (s *Service) List() []entity.Config {
	return s.ListOwned("")
}

// Missing returns the keys of every Required row in owner that has
// no value stored. Tool handlers call this via Ctx.Missing() to
// render a "setup required" banner before doing any work.
func (s *Service) Missing(owner string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k, m := range s.meta {
		if k.Owner != owner || !m.Required {
			continue
		}
		if s.cache[k].Value == "" {
			out = append(out, k.Key)
		}
	}
	return out
}

// Set persists a new value for the app-level key and refreshes the
// cache. Returns an error if the key is unknown.
func (s *Service) Set(ctx context.Context, key, value string) error {
	s.mu.RLock()
	_, ok := s.meta[ownerKey{Owner: "", Key: key}]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	return s.setOwned(ctx, "", key, value)
}

// SetOwned persists a new value for (owner, key) and refreshes the
// cache. Returns an error if that pair was never declared.
func (s *Service) SetOwned(ctx context.Context, owner, key, value string) error {
	s.mu.RLock()
	_, ok := s.meta[ownerKey{Owner: owner, Key: key}]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown config %s/%s", owner, key)
	}
	return s.setOwned(ctx, owner, key, value)
}

func (s *Service) setOwned(ctx context.Context, owner, key, value string) error {
	if err := s.repo.SetValue(ctx, owner, key, value); err != nil {
		return err
	}
	fresh, err := s.repo.FindByOwnerKey(ctx, owner, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[ownerKey{Owner: owner, Key: key}] = *fresh
	s.mu.Unlock()
	return nil
}

// Regenerate replaces an app-level key's value by running its
// registered generator. Fails if the key is unknown, has no
// generator, or is not flagged CanRegenerate.
func (s *Service) Regenerate(ctx context.Context, key string) error {
	s.mu.RLock()
	m, ok := s.meta[ownerKey{Owner: "", Key: key}]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	gen, hasGen := generators[key]
	if !m.CanRegenerate || !hasGen {
		return fmt.Errorf("key %s is not regeneratable", key)
	}
	return s.Set(ctx, key, gen())
}

// ── typed accessors ──────────────────────────────────────────
// Add one per app-level key so callers don't pass string keys around.

func (s *Service) AppName() string            { return s.Get(KeyAppName) }
func (s *Service) AppDescription() string     { return s.Get(KeyAppDescription) }
func (s *Service) AppURL() string             { return s.Get(KeyAppURL) }
func (s *Service) SessionSecret() string      { return s.Get(KeySessionSecret) }
func (s *Service) AdminPasswordChanged() bool { return s.Get(KeyAdminPasswordChanged) == "true" }
