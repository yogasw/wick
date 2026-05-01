// Package accesstoken manages Personal Access Tokens — static bearer
// credentials a user generates from /profile/tokens and pastes into a
// remote MCP client (Claude Desktop, Cursor, VSCode plugin, custom
// CLI). The same token also authenticates any other wick HTTP API a
// user wires up.
//
// The plaintext token is shown to the user exactly once, at creation
// time. Wick stores only the SHA-256 hash so the token can be looked
// up on incoming requests but never reconstructed if the DB leaks.
//
// Token wire format:
//
//	wick_pat_<32 hex chars>
//
// The "wick_pat_" prefix is the routing hint auth middleware uses to
// distinguish static bearers from OAuth JWTs (see internal/docs/
// connectors-design.md §8.3).
package accesstoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Prefix is the leading marker of every wick PAT. MCP auth middleware
// uses it to route the request into pat.Service.Authenticate before
// falling back to the OAuth path.
const Prefix = "wick_pat_"

// suffixLen is the number of hex characters appended after Prefix.
// 32 hex = 16 bytes of entropy = 128 bits, ample for a bearer.
const suffixLen = 32

// ErrInvalid signals an unparseable or unrecognized token. Returned
// (rather than wrapped) so middleware can short-circuit cheaply.
var ErrInvalid = errors.New("invalid token")

// Service is the runtime façade for token CRUD. The handler at
// /profile/mcp drives Issue/Revoke; the future MCP middleware drives
// Authenticate.
type Service struct {
	repo *Repo
}

func NewService(r *Repo) *Service        { return &Service{repo: r} }
func NewServiceFromDB(db *gorm.DB) *Service { return NewService(NewRepo(db)) }

// IssueResult bundles the freshly stored row plus the plaintext token.
// The plaintext is only ever returned by Issue — there is no other
// API to read it. The handler displays it once and discards it.
type IssueResult struct {
	Token string                       // plaintext "wick_pat_..." — never re-derivable
	Row   *entity.PersonalAccessToken
}

// Issue mints a new token for userID with the given human label.
// Returns the plaintext token and the persisted row. The plaintext
// MUST NOT be logged or stored anywhere outside the response back to
// the user.
func (s *Service) Issue(ctx context.Context, userID, name string) (*IssueResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(name) > 120 {
		return nil, errors.New("name is too long (max 120 characters)")
	}

	suffix, err := randomHex(suffixLen / 2)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	plain := Prefix + suffix
	row := &entity.PersonalAccessToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(plain),
		Last4:     suffix[len(suffix)-4:],
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}
	return &IssueResult{Token: plain, Row: row}, nil
}

// ListActive returns the user's non-revoked tokens, newest first. The
// plaintext is unrecoverable — UI shows the masked form via Row.Masked().
func (s *Service) ListActive(ctx context.Context, userID string) ([]entity.PersonalAccessToken, error) {
	return s.repo.ListActiveByUser(ctx, userID)
}

// Revoke stamps RevokedAt on a token the user owns. Errors when the
// row does not exist or belongs to another user.
func (s *Service) Revoke(ctx context.Context, id, userID string) error {
	if _, err := s.repo.GetForUser(ctx, id, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("token not found")
		}
		return err
	}
	return s.repo.Revoke(ctx, id, userID)
}

// Authenticate validates a plaintext bearer pulled from an incoming
// request and returns the owning user_id. Returns ErrInvalid for any
// malformed, unknown, or revoked token so middleware can respond with
// a uniform 401 — callers MUST NOT distinguish "wrong format" from
// "wrong token" in the response.
//
// LastUsedAt is stamped best-effort; failure to update does not fail
// the auth (the DB write is observability, not a gate).
func (s *Service) Authenticate(ctx context.Context, plain string) (userID string, err error) {
	if !strings.HasPrefix(plain, Prefix) {
		return "", ErrInvalid
	}
	suffix := plain[len(Prefix):]
	if len(suffix) != suffixLen {
		return "", ErrInvalid
	}
	row, err := s.repo.FindByHash(ctx, hashToken(plain))
	if err != nil {
		return "", ErrInvalid
	}
	_ = s.repo.TouchLastUsed(ctx, row.ID)
	return row.UserID, nil
}

// hashToken returns the SHA-256 hex digest of the plaintext token.
// Stored in the DB; never reversible.
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
