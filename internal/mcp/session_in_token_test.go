package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// approvedStubUsers resolves any id to an approved plain user, so the
// scoped-token branch of the middleware runs to completion.
type approvedStubUsers struct{}

func (approvedStubUsers) GetUserByID(_ context.Context, id string) (*entity.User, error) {
	return &entity.User{ID: id, Name: id, Role: entity.RoleUser, Approved: true}, nil
}
func (approvedStubUsers) GetUserFilterTagIDs(context.Context, string) []string { return nil }

func TestScopedTokenCarriesSession(t *testing.T) {
	s := NewScopedTokens()

	tok, err := s.IssueForSession("user-1", "sess-abc", []string{"a"}, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if sid, ok := s.LookupSession(tok); !ok || sid != "sess-abc" {
		t.Fatalf("LookupSession = %q ok=%v, want sess-abc", sid, ok)
	}
	// The identity half must be untouched by the addition.
	if user, tags, ok := s.Lookup(tok); !ok || user != "user-1" || len(tags) != 1 {
		t.Fatalf("Lookup = %q %v ok=%v", user, tags, ok)
	}

	// A sub-agent token is minted before its session exists: valid, but
	// with no opinion about which conversation it belongs to.
	sub, _ := s.Issue("user-1", nil)
	if sid, ok := s.LookupSession(sub); !ok || sid != "" {
		t.Fatalf("session-less grant: sid=%q ok=%v, want empty+ok", sid, ok)
	}

	if _, ok := s.LookupSession("wick_sub_nope"); ok {
		t.Fatal("unknown token resolved a session")
	}
}

// The whole point of putting the session in the token: providers that
// cannot send a custom header (codex) still resolve their session.
func TestAuthMiddleware_ResolvesSessionFromToken(t *testing.T) {
	s := NewScopedTokens()
	tok, _ := s.IssueForSession("user-1", "sess-abc", nil, false)
	m := NewAuthMiddleware(nil, approvedStubUsers{}, nil, "").WithScopedTokens(s)

	var seen string
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = handlers.SessionOf(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if seen != "sess-abc" {
		t.Fatalf("resolved session = %q, want sess-abc", seen)
	}
}

// The credential wins over the header: a token minted for one session
// cannot be pointed at another by whoever sends the request.
func TestAuthMiddleware_TokenSessionOverridesHeader(t *testing.T) {
	s := NewScopedTokens()
	tok, _ := s.IssueForSession("user-1", "sess-own", nil, false)
	m := NewAuthMiddleware(nil, approvedStubUsers{}, nil, "").WithScopedTokens(s)

	var seen string
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = handlers.SessionOf(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(handlers.SessionHeader, "sess-somebody-else")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "sess-own" {
		t.Fatalf("resolved session = %q, want the token's own sess-own", seen)
	}
}

// A sub-agent's token is minted before its session exists, so it names
// none — and its own header must survive untouched.
func TestAuthMiddleware_SessionlessGrantKeepsHeader(t *testing.T) {
	s := NewScopedTokens()
	tok, _ := s.Issue("user-1", nil)
	m := NewAuthMiddleware(nil, approvedStubUsers{}, nil, "").WithScopedTokens(s)

	var seen string
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = handlers.SessionOf(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(handlers.SessionHeader, "sess-child")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "sess-child" {
		t.Fatalf("resolved session = %q, want the sub-agent's own sess-child", seen)
	}
}

// A grant with no session must leave the request exactly as it arrived.
func TestAuthMiddleware_SessionlessGrantAndNoHeaderResolvesNothing(t *testing.T) {
	s := NewScopedTokens()
	tok, _ := s.Issue("user-1", nil)
	m := NewAuthMiddleware(nil, approvedStubUsers{}, nil, "").WithScopedTokens(s)

	var seen string
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = handlers.SessionOf(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "" {
		t.Fatalf("resolved session = %q, want empty", seen)
	}
}

// Grants travel across a graceful upgrade; the session has to travel with
// them or every agent alive at handover goes back to typing session ids.
func TestScopedTokenHandoffKeepsSession(t *testing.T) {
	dir := t.TempDir()
	out := NewScopedTokens()
	tok, _ := out.IssueForSession("user-1", "sess-abc", []string{"a"}, false)
	if n, err := out.SaveHandoff(dir); err != nil || n != 1 {
		t.Fatalf("save: n=%d err=%v", n, err)
	}

	in := NewScopedTokens()
	if n := in.LoadHandoff(dir); n != 1 {
		t.Fatalf("load: n=%d", n)
	}
	if sid, ok := in.LookupSession(tok); !ok || sid != "sess-abc" {
		t.Fatalf("after handoff: sid=%q ok=%v, want sess-abc", sid, ok)
	}
}
