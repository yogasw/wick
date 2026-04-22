// Package sso manages SSO provider credentials (currently Google OAuth).
// Credentials live in the `sso_providers` table and are cached in
// memory after Bootstrap — lookups don't touch the DB. Saving a
// provider via the admin UI refreshes the cache, so OAuth wiring
// reflects changes without restarting the server.
//
// The callback URL is never stored. It's derived from
// configs.Service.AppURL() + "/auth/callback" every time the oauth2
// config is built.
package sso

import (
	"context"
	"fmt"
	"github.com/yogasw/wick/internal/entity"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gorm.io/gorm"
)

// CallbackPath is the relative path the OAuth redirect lands on.
// Kept as a package constant so the admin UI and login handler agree
// on the same suffix.
const CallbackPath = "/auth/callback"

type Service struct {
	repo  *repo
	mu    sync.RWMutex
	cache map[string]entity.SSOProvider
}

func NewService(db *gorm.DB) *Service {
	return &Service{
		repo:  newRepo(db),
		cache: make(map[string]entity.SSOProvider),
	}
}

// Bootstrap ensures every known provider has at least an empty row and
// populates the cache. Call once at startup.
func (s *Service) Bootstrap(ctx context.Context) error {
	// Seed the provider rows we care about. New providers get added
	// here as we support them.
	for _, provider := range []string{entity.SSOProviderGoogle} {
		if err := s.repo.EnsureProvider(ctx, provider); err != nil {
			return fmt.Errorf("ensure provider %s: %w", provider, err)
		}
	}
	return s.reloadCache(ctx)
}

// reloadCache pulls every provider row from the DB into the in-memory
// cache. Called on Bootstrap and after every Update.
func (s *Service) reloadCache(ctx context.Context) error {
	rows, err := s.repo.ListAll(ctx)
	if err != nil {
		return err
	}
	fresh := make(map[string]entity.SSOProvider, len(rows))
	for _, r := range rows {
		fresh[r.Provider] = r
	}
	s.mu.Lock()
	s.cache = fresh
	s.mu.Unlock()
	return nil
}

// List returns every provider row, sorted by provider name. Used by
// the admin UI.
func (s *Service) List() []entity.SSOProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]entity.SSOProvider, 0, len(s.cache))
	// Iterate in a stable order — follow the seed list above.
	for _, provider := range []string{entity.SSOProviderGoogle} {
		if p, ok := s.cache[provider]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Get returns the cached row for a provider, or (zero, false) if the
// provider was never bootstrapped.
func (s *Service) Get(provider string) (entity.SSOProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.cache[provider]
	return p, ok
}

// AnyEnabled reports whether at least one provider has Enabled=true
// and non-empty credentials. Used by the login page to decide whether
// to render the "Continue with Google" button.
func (s *Service) AnyEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.cache {
		if p.Enabled && p.ClientID != "" && p.ClientSecret != "" {
			return true
		}
	}
	return false
}

// Update writes new credentials for a provider and refreshes the cache.
// allowedDomains is a comma-separated list of email domains; empty
// string disables the restriction.
func (s *Service) Update(ctx context.Context, provider, clientID, clientSecret string, enabled bool, allowedDomains string) error {
	if err := s.repo.Update(ctx, provider, clientID, clientSecret, enabled, normalizeDomains(allowedDomains)); err != nil {
		return err
	}
	return s.reloadCache(ctx)
}

// IsEmailAllowed reports whether an email's domain is permitted to sign
// in through this provider. Empty AllowedDomains means no restriction.
// Returns false for malformed emails.
func (s *Service) IsEmailAllowed(provider, email string) bool {
	p, ok := s.Get(provider)
	if !ok {
		return false
	}
	if p.AllowedDomains == "" {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range strings.Split(p.AllowedDomains, ",") {
		if strings.ToLower(strings.TrimSpace(d)) == domain {
			return true
		}
	}
	return false
}

// normalizeDomains trims whitespace around each entry, lowercases it,
// and drops empty ones. Keeps the stored value canonical so matching
// doesn't need to re-parse on every login.
func normalizeDomains(raw string) string {
	parts := strings.Split(raw, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// OAuthConfig builds the oauth2.Config for a provider, with the
// callback URL derived from appURL. Returns (nil, false) if the
// provider isn't configured or isn't enabled — callers should treat
// that as "Google login disabled".
func (s *Service) OAuthConfig(provider, appURL string) (*oauth2.Config, bool) {
	p, ok := s.Get(provider)
	if !ok || !p.Enabled || p.ClientID == "" || p.ClientSecret == "" {
		return nil, false
	}
	switch provider {
	case entity.SSOProviderGoogle:
		return &oauth2.Config{
			ClientID:     p.ClientID,
			ClientSecret: p.ClientSecret,
			RedirectURL:  appURL + CallbackPath,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}, true
	default:
		return nil, false
	}
}
