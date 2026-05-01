// Package oauth implements the minimal OAuth 2.1 authorization server
// every spec-compliant MCP client (notably Claude.ai web) requires.
//
// Surface — all served from the same wick HTTP origin:
//
//	GET  /.well-known/oauth-protected-resource (RFC 9728)
//	GET  /.well-known/oauth-authorization-server (RFC 8414)
//	POST /oauth/register                       Dynamic Client Registration (RFC 7591)
//	GET  /oauth/authorize                      PKCE authorization (RFC 7636)
//	POST /oauth/token                          code + refresh exchange (RFC 6749 §4.1, §6)
//
// Design choices, deliberately narrow:
//
//   - Tokens are opaque random hex strings, hashed at rest. We do NOT
//     issue JWTs — same authentication strength, no key-management
//     burden, and the validator is a single DB lookup. JWTs can layer
//     in later if/when we need stateless validation across replicas.
//   - Public clients only (PKCE mandatory). MCP authorization spec
//     explicitly requires PKCE for all clients, so the simplification
//     costs us nothing.
//   - The user-agent dance reuses wick's existing cookie session: at
//     /authorize we check for a logged-in user, redirect to /auth/login
//     if missing, and treat the post-login redirect as implicit
//     consent (an admin-only consent UI is a follow-up; out of scope
//     for the first cut).
//   - Refresh tokens rotate on every redemption per OAuth BCP. Reuse
//     of a rotated refresh triggers chain-wide revocation.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Token TTLs. Conservative defaults — short access lifetime keeps
// blast radius small if a token leaks; long refresh keeps re-auth
// rare for active clients.
const (
	AccessTokenTTL  = 1 * time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	AuthCodeTTL     = 5 * time.Minute

	// Token wire prefixes. The bearer middleware inspects these to
	// route to the right validator (PAT vs OAuth).
	AccessTokenPrefix  = "wick_oat_"
	RefreshTokenPrefix = "wick_ort_"
)

// ErrInvalid is the uniform "something is wrong with this request"
// signal for /token. Mapped to "invalid_grant" or "invalid_token"
// at the HTTP layer.
var ErrInvalid = errors.New("invalid")

// Service holds the OAuth runtime — repo handle plus the issuer URL
// (used in metadata documents and verified against token aud claim
// when we eventually add JWTs).
type Service struct {
	repo   *Repo
	issuer string // e.g. "https://wick.example.com" (no trailing slash)
}

func NewService(r *Repo, issuer string) *Service {
	return &Service{repo: r, issuer: strings.TrimRight(issuer, "/")}
}

func NewServiceFromDB(db *gorm.DB, issuer string) *Service {
	return NewService(NewRepo(db), issuer)
}

// Issuer returns the canonical issuer URL. Exposed so the .well-known
// handlers can render the metadata documents.
func (s *Service) Issuer() string { return s.issuer }

// SetIssuer overrides the issuer URL — useful when the app_url config
// changes after the service is constructed (admin saves a new URL via
// /admin/configs, no restart).
func (s *Service) SetIssuer(issuer string) {
	s.issuer = strings.TrimRight(issuer, "/")
}

// ── Dynamic Client Registration (RFC 7591) ───────────────────────────

// RegisterClientParams are the inbound DCR fields wick honors. We
// ignore the optional metadata (logo_uri, tos_uri, ...) — keeping the
// happy path tiny.
type RegisterClientParams struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// RegisterClient stores a new client and returns the freshly minted
// client_id. ClientName defaults to "MCP client" when empty so the
// admin UI never shows a blank row.
func (s *Service) RegisterClient(ctx context.Context, p RegisterClientParams) (*entity.OAuthClient, error) {
	if len(p.RedirectURIs) == 0 {
		return nil, errors.New("redirect_uris is required")
	}
	for _, u := range p.RedirectURIs {
		if u == "" {
			return nil, errors.New("redirect_uris must not contain empty strings")
		}
	}
	if p.ClientName == "" {
		p.ClientName = "MCP client"
	}
	clientID, err := randomID("wick_app_", 16)
	if err != nil {
		return nil, fmt.Errorf("generate client_id: %w", err)
	}
	c := &entity.OAuthClient{
		ClientID:     clientID,
		Name:         p.ClientName,
		RedirectURIs: encodeRedirectURIs(p.RedirectURIs),
	}
	if err := s.repo.CreateClient(ctx, c); err != nil {
		return nil, fmt.Errorf("save client: %w", err)
	}
	return c, nil
}

// LookupClient returns the client by ID, or an error when missing.
func (s *Service) LookupClient(ctx context.Context, clientID string) (*entity.OAuthClient, error) {
	return s.repo.GetClient(ctx, clientID)
}

// ValidateRedirectURI reports whether redirectURI is one of the
// strings the client registered. Strict equality — the OAuth spec
// allows no fuzzy matching.
func ValidateRedirectURI(client *entity.OAuthClient, redirectURI string) bool {
	for _, u := range decodeRedirectURIs(client.RedirectURIs) {
		if u == redirectURI {
			return true
		}
	}
	return false
}

// ── Authorization code (RFC 6749 §4.1 + RFC 7636 PKCE) ───────────────

// IssueAuthCodeParams bundles the values /authorize stamps onto a
// new code row.
type IssueAuthCodeParams struct {
	ClientID            string
	UserID              string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string // "S256" — wick rejects "plain" per OAuth 2.1
}

// IssueAuthCode mints a new authorization code, stores the PKCE
// challenge, and returns the opaque code string.
func (s *Service) IssueAuthCode(ctx context.Context, p IssueAuthCodeParams) (string, error) {
	if p.CodeChallenge == "" {
		return "", errors.New("code_challenge required")
	}
	if p.CodeChallengeMethod != "S256" {
		return "", errors.New("code_challenge_method must be S256")
	}
	code, err := randomHex(32)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	row := &entity.OAuthAuthorizationCode{
		Code:                code,
		ClientID:            p.ClientID,
		UserID:              p.UserID,
		RedirectURI:         p.RedirectURI,
		Scope:               p.Scope,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: p.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(AuthCodeTTL),
	}
	if err := s.repo.CreateAuthCode(ctx, row); err != nil {
		return "", fmt.Errorf("save auth code: %w", err)
	}
	return code, nil
}

// ── /token grant handlers ────────────────────────────────────────────

// TokenPair is the response shape /token returns and the bearer
// middleware works against. AccessToken / RefreshToken are plaintext
// — only crossing the wire on this single response. After that, only
// hashes survive in the DB.
type TokenPair struct {
	AccessToken    string
	RefreshToken   string
	AccessExpires  time.Duration
	RefreshExpires time.Duration
}

// ExchangeAuthCode redeems an authorization code per RFC 6749 §4.1.3
// + RFC 7636 §4.6 (PKCE verification). Marks the code used so a
// replay fails atomically.
func (s *Service) ExchangeAuthCode(ctx context.Context, code, clientID, redirectURI, codeVerifier string) (*TokenPair, error) {
	if code == "" || clientID == "" || codeVerifier == "" {
		return nil, ErrInvalid
	}
	row, err := s.repo.ConsumeAuthCode(ctx, code)
	if err != nil {
		return nil, ErrInvalid
	}
	if row.ClientID != clientID {
		return nil, ErrInvalid
	}
	if row.RedirectURI != redirectURI {
		return nil, ErrInvalid
	}
	if !verifyPKCE(codeVerifier, row.CodeChallenge, row.CodeChallengeMethod) {
		return nil, ErrInvalid
	}
	return s.mintTokenPair(ctx, clientID, row.UserID, row.Scope, nil)
}

// ExchangeRefreshToken swaps a refresh token for a new pair (rotation
// per OAuth BCP). A reuse of an already-rotated refresh is treated
// as token theft: the entire chain is revoked.
func (s *Service) ExchangeRefreshToken(ctx context.Context, refresh, clientID string) (*TokenPair, error) {
	if !strings.HasPrefix(refresh, RefreshTokenPrefix) {
		return nil, ErrInvalid
	}
	row, err := s.repo.FindAnyTokenByHash(ctx, hashToken(refresh))
	if err != nil {
		return nil, ErrInvalid
	}
	if row.Kind != "refresh" || row.ClientID != clientID {
		return nil, ErrInvalid
	}
	// Detect replay: refresh already revoked or expired = treat as
	// theft, kill the descendant chain too.
	if row.RevokedAt != nil || time.Now().After(row.ExpiresAt) {
		_ = s.repo.RevokeChain(ctx, row.ID)
		return nil, ErrInvalid
	}
	pair, err := s.mintTokenPair(ctx, clientID, row.UserID, row.Scope, &row.ID)
	if err != nil {
		return nil, err
	}
	// Revoke the spent refresh now that its successor exists.
	_ = s.repo.Revoke(ctx, row.ID)
	return pair, nil
}

// mintTokenPair generates a new access + refresh token, stores both
// hashed, and returns the plaintext pair.
func (s *Service) mintTokenPair(ctx context.Context, clientID, userID, scope string, parentRefreshID *string) (*TokenPair, error) {
	access, err := randomID(AccessTokenPrefix, 16)
	if err != nil {
		return nil, err
	}
	refresh, err := randomID(RefreshTokenPrefix, 32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.repo.CreateToken(ctx, &entity.OAuthToken{
		TokenHash: hashToken(access),
		Kind:      "access",
		ClientID:  clientID,
		UserID:    userID,
		Scope:     scope,
		ExpiresAt: now.Add(AccessTokenTTL),
	}); err != nil {
		return nil, err
	}
	if err := s.repo.CreateToken(ctx, &entity.OAuthToken{
		TokenHash:     hashToken(refresh),
		Kind:          "refresh",
		ClientID:      clientID,
		UserID:        userID,
		Scope:         scope,
		ParentTokenID: parentRefreshID,
		ExpiresAt:     now.Add(RefreshTokenTTL),
	}); err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:    access,
		RefreshToken:   refresh,
		AccessExpires:  AccessTokenTTL,
		RefreshExpires: RefreshTokenTTL,
	}, nil
}

// Authenticate validates an access token presented in the
// Authorization: Bearer header and returns the owning user_id.
// Mirrors accesstoken.Service.Authenticate so the MCP middleware can
// hand off identical-shaped calls.
func (s *Service) Authenticate(ctx context.Context, plain string) (string, error) {
	if !strings.HasPrefix(plain, AccessTokenPrefix) {
		return "", ErrInvalid
	}
	row, err := s.repo.FindActiveTokenByHash(ctx, hashToken(plain))
	if err != nil {
		return "", ErrInvalid
	}
	if row.Kind != "access" {
		return "", ErrInvalid
	}
	_ = s.repo.MarkUsed(ctx, row.ID)
	return row.UserID, nil
}

// ── Hashing / random helpers ─────────────────────────────────────────

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomID(prefix string, nBytes int) (string, error) {
	suffix, err := randomHex(nBytes)
	if err != nil {
		return "", err
	}
	return prefix + suffix, nil
}

// verifyPKCE checks code_verifier against the stored code_challenge
// per RFC 7636. Only "S256" is accepted (OAuth 2.1 forbids "plain").
func verifyPKCE(verifier, challenge, method string) bool {
	if method != "S256" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return got == challenge
}

// encodeRedirectURIs / decodeRedirectURIs use a tab separator instead
// of JSON to keep the column human-readable in psql for ops debugging.
// Tabs are forbidden in URIs by RFC 3986.
func encodeRedirectURIs(uris []string) string {
	return strings.Join(uris, "\t")
}

func decodeRedirectURIs(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\t")
}
