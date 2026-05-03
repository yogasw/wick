package configs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"

	"gorm.io/gorm"
)

// isAnyToken reports whether value is already an encryption token in
// either layer (per-user wick_enc_ or master wick_cenc_). Reused by
// reconcile / setOwned to skip double-encryption.
func isAnyToken(v string) bool { return enc.IsToken(v) || enc.IsMasterToken(v) }

// Encryptor is the subset of *enc.Service the configs layer uses for
// at-rest encryption of `secret`-tagged rows. Set once after boot via
// SetEncryptor; reconcile/setOwned check it before encrypting writes
// or decrypting reads.
type Encryptor interface {
	EncryptMaster(plain string) (string, error)
	DecryptMaster(token string) (string, error)
	Disabled() bool
}

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
	// enc encrypts `secret`-tagged values at rest. Nil until
	// SetEncryptor runs (after Bootstrap, since the master key is
	// itself a config row). When nil, secret values are stored
	// plaintext — same behaviour as before this layer existed.
	enc Encryptor
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

// SetEncryptor wires the at-rest cipher. Call once at boot after the
// enc service is built (which itself depends on Bootstrap having
// reconciled the encryption_key row). After this point, every Set
// against an IsSecret row writes ciphertext to the DB and the cache
// holds the decrypted plaintext.
//
// Calling with a Disabled() encryptor is a no-op — the layer behaves
// as if SetEncryptor was never called.
func (s *Service) SetEncryptor(e Encryptor) {
	if e == nil || e.Disabled() {
		return
	}
	s.mu.Lock()
	s.enc = e
	// Two-pass walk over the cache (Bootstrap ran before this call,
	// so the encryptor was nil during reconcile and IsSecret rows
	// landed in the cache untouched):
	//   - wick_cenc_ tokens → decrypt into the cache so callers see
	//     plaintext via Get/GetOwned.
	//   - plaintext IsSecret rows → encrypt and persist so legacy
	//     installs migrate to ciphertext on this boot.
	type pending struct{ owner, key, value string }
	var toEncrypt []pending
	for k, row := range s.cache {
		m, ok := s.meta[k]
		if !ok || !m.IsSecret || row.Value == "" {
			continue
		}
		if k.Owner == "" && k.Key == KeyEncryptionKey {
			continue
		}
		if enc.IsMasterToken(row.Value) {
			plain, err := e.DecryptMaster(row.Value)
			if err != nil {
				continue
			}
			row.Value = plain
			s.cache[k] = row
			continue
		}
		if isAnyToken(row.Value) {
			continue
		}
		toEncrypt = append(toEncrypt, pending{owner: k.Owner, key: k.Key, value: row.Value})
	}
	s.mu.Unlock()
	for _, p := range toEncrypt {
		ct, err := e.EncryptMaster(p.value)
		if err != nil {
			continue
		}
		_ = s.repo.SetValue(context.Background(), p.owner, p.key, ct)
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

// EnsureOwned reconciles a runtime-declared set of rows for owner.
// Used when a connector / tool / job instance is created after boot:
// the caller stamps Owner on each row and hands them in; the rows that
// already exist are left alone (metadata is refreshed, value
// preserved), missing rows are created.
//
// The owner string scopes the rows — by convention "connector:{id}"
// for connector instances. Any owner string is accepted.
func (s *Service) EnsureOwned(ctx context.Context, owner string, rows ...entity.Config) error {
	for _, row := range rows {
		row.Owner = owner
		if err := s.reconcile(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOwned removes every row scoped to owner from the DB and the
// in-memory cache. Used when a connector / tool / job instance is
// destroyed. Returns nil when owner has no rows.
func (s *Service) DeleteOwned(ctx context.Context, owner string) error {
	if err := s.repo.DeleteByOwner(ctx, owner); err != nil {
		return fmt.Errorf("delete configs for owner %q: %w", owner, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cache {
		if k.Owner == owner {
			delete(s.cache, k)
		}
	}
	for k := range s.meta {
		if k.Owner == owner {
			delete(s.meta, k)
		}
	}
	delete(s.declOrder, owner)
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
	// nothing was stored yet). New rows tagged secret get encrypted on
	// the seed write itself when the encryptor is wired (currently
	// only true for EnsureOwned post-boot — Bootstrap-time secrets
	// land plaintext, then SetEncryptor migrates them on the next
	// boot).
	if existing == nil {
		fresh.Value = value
		stored := value
		if value != "" && row.IsSecret && s.shouldEncrypt(row.Owner, row.Key) {
			if ct, encErr := s.enc.EncryptMaster(value); encErr == nil {
				stored = ct
			}
		}
		_ = s.repo.SetValue(ctx, row.Owner, row.Key, stored)
	}
	// Cache holds plaintext: reads via Get/GetOwned must return what
	// the admin pasted, not the ciphertext.
	if fresh.IsSecret && enc.IsMasterToken(fresh.Value) && s.enc != nil {
		if plain, decErr := s.enc.DecryptMaster(fresh.Value); decErr == nil {
			fresh.Value = plain
		}
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
	// IsSecret rows render as password inputs with a "leave blank to
	// keep" placeholder — an empty submit means the admin did not
	// touch the field, so don't clobber the stored value.
	if value == "" {
		s.mu.RLock()
		m, ok := s.meta[ownerKey{Owner: owner, Key: key}]
		s.mu.RUnlock()
		if ok && m.IsSecret {
			return nil
		}
	}
	stored := value
	plaintext := value
	if value != "" && !isAnyToken(value) && s.shouldEncrypt(owner, key) {
		ct, err := s.enc.EncryptMaster(value)
		if err != nil {
			return fmt.Errorf("encrypt %s/%s: %w", owner, key, err)
		}
		stored = ct
	}
	if err := s.repo.SetValue(ctx, owner, key, stored); err != nil {
		return err
	}
	fresh, err := s.repo.FindByOwnerKey(ctx, owner, key)
	if err != nil {
		return err
	}
	// Cache holds plaintext (see reconcile). Reuse what we already
	// have — the just-written ciphertext would round-trip through
	// DecryptMaster, but skipping the call avoids a needless decrypt.
	fresh.Value = plaintext
	s.mu.Lock()
	s.cache[ownerKey{Owner: owner, Key: key}] = *fresh
	s.mu.Unlock()
	return nil
}

// shouldEncrypt reports whether (owner, key) is a secret row that the
// at-rest layer should encrypt. False when no encryptor is wired, the
// row is not declared IsSecret, or the row is the encryption_key
// itself (chicken-and-egg: it would need to decrypt itself to read).
func (s *Service) shouldEncrypt(owner, key string) bool {
	if s.enc == nil {
		return false
	}
	if owner == "" && key == KeyEncryptionKey {
		return false
	}
	s.mu.RLock()
	m, ok := s.meta[ownerKey{Owner: owner, Key: key}]
	s.mu.RUnlock()
	return ok && m.IsSecret
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

// EncryptionKey returns the master key for the encrypted-fields layer.
// WICK_ENC_KEY in the environment wins when set — production deploys
// inject from a vault here so the secret never lands in the DB. Falls
// back to the DB-stored value (auto-generated on first boot via the
// generators map in spec.go).
func (s *Service) EncryptionKey() string {
	if v := os.Getenv("WICK_ENC_KEY"); v != "" {
		return v
	}
	return s.Get(KeyEncryptionKey)
}
