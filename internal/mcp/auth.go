package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// userResolver loads a user record by id. login.Service satisfies it
// — kept as an interface so tests can stub.
type userResolver interface {
	GetUserByID(ctx context.Context, id string) (*entity.User, error)
	GetUserFilterTagIDs(ctx context.Context, userID string) []string
}

// oauthValidator is the optional OAuth opaque-token validator. nil
// while the OAuth server isn't wired (Phase C); when present it gets
// every bearer that doesn't carry the wick_pat_ prefix.
//
// Returns user_id on success; ErrInvalid (or any error) on mismatch
// — the middleware reports a single uniform 401, so callers can't
// distinguish "wrong format" from "wrong token".
type oauthValidator interface {
	Authenticate(ctx context.Context, token string) (userID string, err error)
}

// AuthMiddleware extracts the Authorization: Bearer header, routes the
// token to the right validator (PAT vs OAuth), loads the user record
// + filter-tag IDs, and stamps both onto the request context using
// login.WithUser so downstream code reads identity exactly the way it
// does for cookie-authed requests.
//
// Unauth requests get a 401 with WWW-Authenticate pointing at the
// resource-metadata document (RFC 9728), so spec-compliant MCP clients
// can discover the OAuth server and run the auth dance.
type AuthMiddleware struct {
	tokens         *accesstoken.Service
	users          userResolver
	oauth          oauthValidator // optional; nil disables OAuth validation
	resourceMetaURL string        // absolute URL to /.well-known/oauth-protected-resource
}

// NewAuthMiddleware wires the bearer middleware. Pass nil oauth to
// run PAT-only (Phase B); attach later by replacing the field.
func NewAuthMiddleware(tokens *accesstoken.Service, users userResolver, oauth oauthValidator, resourceMetaURL string) *AuthMiddleware {
	return &AuthMiddleware{
		tokens:          tokens,
		users:           users,
		oauth:           oauth,
		resourceMetaURL: resourceMetaURL,
	}
}

// Wrap returns the http.Handler middleware. Apply to /mcp.
func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerFromHeader(r)
		if !ok {
			m.reject(w, "")
			return
		}

		userID, err := m.resolveToken(r.Context(), token)
		if err != nil {
			m.reject(w, "invalid_token")
			return
		}
		user, err := m.users.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			m.reject(w, "invalid_token")
			return
		}
		if !user.Approved {
			m.reject(w, "insufficient_scope")
			return
		}
		tagIDs := m.users.GetUserFilterTagIDs(r.Context(), user.ID)
		ctx := login.WithUser(r.Context(), user, tagIDs)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveToken dispatches by token shape. PAT prefix → accesstoken
// path; anything else → OAuth (when wired) or reject.
func (m *AuthMiddleware) resolveToken(ctx context.Context, token string) (string, error) {
	if strings.HasPrefix(token, accesstoken.Prefix) {
		return m.tokens.Authenticate(ctx, token)
	}
	if m.oauth != nil {
		return m.oauth.Authenticate(ctx, token)
	}
	return "", errors.New("no validator for token")
}

// reject writes a 401 with the WWW-Authenticate challenge per RFC
// 6750 + RFC 9728. errKind is one of "" (no token), "invalid_token",
// "insufficient_scope". Body is short text — clients read the header.
func (m *AuthMiddleware) reject(w http.ResponseWriter, errKind string) {
	challenge := `Bearer realm="wick"`
	if m.resourceMetaURL != "" {
		challenge += `, resource_metadata="` + m.resourceMetaURL + `"`
	}
	if errKind != "" {
		challenge += `, error="` + errKind + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("unauthorized"))
}

// bearerFromHeader returns the token portion of an Authorization:
// Bearer header. Treats whitespace tolerantly; rejects any other
// scheme.
func bearerFromHeader(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
