package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// feasUsers is a two-user directory: user A (plain) and user B (admin).
type feasUsers struct{}

func (feasUsers) GetUserByID(_ context.Context, id string) (*entity.User, error) {
	switch id {
	case "user-a":
		return &entity.User{ID: "user-a", Name: "A", Role: entity.RoleUser, Approved: true}, nil
	case "user-b":
		return &entity.User{ID: "user-b", Name: "B", Role: entity.RoleAdmin, Approved: true}, nil
	}
	return nil, errors.New("no such user")
}
func (feasUsers) GetUserFilterTagIDs(_ context.Context, id string) []string {
	return []string{"tag-of-" + id}
}

// probe captures the identity the middleware stamped onto the request.
type probe struct {
	called  bool
	userID  string
	isAdmin bool
	tagIDs  []string
}

func serveWith(t *testing.T, m *AuthMiddleware, bearer string, extra map[string]string) (*probe, int) {
	t.Helper()
	p := &probe{}
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.called = true
		if u := login.GetUser(r.Context()); u != nil {
			p.userID, p.isAdmin = u.ID, u.IsAdmin()
		}
		p.tagIDs = login.GetUserTagIDs(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return p, rec.Code
}

// TestFeasibility_PerUserTokenSwapsIdentity is the core question: if a spawn
// carries a DIFFERENT scoped token, does the MCP server resolve a DIFFERENT
// user? If this passes, per-user MCP identity is achievable by swapping the
// token in the spawn argv — no new identity machinery needed.
func TestFeasibility_PerUserTokenSwapsIdentity(t *testing.T) {
	scoped := NewScopedTokens()
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").
		WithInternalToken("boot-secret").
		WithScopedTokens(scoped)

	tokA, err := scoped.Issue("user-a", []string{"tag-a1", "tag-a2"})
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	tokB, err := scoped.Issue("user-b", []string{"tag-b1"})
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}

	pa, code := serveWith(t, m, tokA, nil)
	if code != http.StatusOK || pa.userID != "user-a" {
		t.Fatalf("token A resolved user=%q code=%d, want user-a/200", pa.userID, code)
	}
	pb, code := serveWith(t, m, tokB, nil)
	if code != http.StatusOK || pb.userID != "user-b" {
		t.Fatalf("token B resolved user=%q code=%d, want user-b/200", pb.userID, code)
	}
	t.Logf("PROVEN: token swap -> identity swap (A=%q tags=%v, B=%q tags=%v)",
		pa.userID, pa.tagIDs, pb.userID, pb.tagIDs)

	// Per-user tag filtering must follow the token, else connector
	// visibility can't be scoped per user.
	if len(pa.tagIDs) == 0 || pa.tagIDs[0] != "tag-a1" {
		t.Fatalf("user A tags = %v, want the token's narrowed set", pa.tagIDs)
	}
	if len(pb.tagIDs) != 1 || pb.tagIDs[0] != "tag-b1" {
		t.Fatalf("user B tags = %v, want [tag-b1]", pb.tagIDs)
	}
}

// TestScopedToken_StripAdminPerGrant pins the split the two token kinds need.
//
// Sub-agent (Issue, stripAdmin=true): admin MUST be stripped, or a profile's
// tag narrowing is decorative for an admin's children.
//
// Top-level session (IssueFor stripAdmin=false): admin MUST survive, because
// the principal is the human who is chatting — demoting them removes their own
// admin-only tools while narrowing nothing on their behalf.
func TestScopedToken_StripAdminPerGrant(t *testing.T) {
	scoped := NewScopedTokens()
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithScopedTokens(scoped)

	subTok, _ := scoped.Issue("user-b", nil) // user-b IS RoleAdmin
	sub, code := serveWith(t, m, subTok, nil)
	if code != http.StatusOK || sub.userID != "user-b" {
		t.Fatalf("sub-agent token: user=%q code=%d", sub.userID, code)
	}
	if sub.isAdmin {
		t.Fatal("sub-agent token preserved admin — tag narrowing would be bypassed")
	}

	topTok, _ := scoped.IssueFor("user-b", nil, false)
	top, code := serveWith(t, m, topTok, nil)
	if code != http.StatusOK || top.userID != "user-b" {
		t.Fatalf("top-level token: user=%q code=%d", top.userID, code)
	}
	if !top.isAdmin {
		t.Fatal("top-level token stripped admin — chatting admin loses own tools")
	}

	// A plain user must never gain admin from either kind.
	plainTok, _ := scoped.IssueFor("user-a", nil, false)
	plain, _ := serveWith(t, m, plainTok, nil)
	if plain.isAdmin {
		t.Fatal("non-admin escalated to admin via top-level token")
	}
	t.Log("CONFIRMED: sub-agent strips admin, top-level preserves it, no escalation")
}

// TestFeasibility_InternalTokenIsAdminForEveryone reproduces the reported
// problem: every web spawn shares one boot secret and lands as the SAME
// synthetic admin, so wick cannot tell user A from user B.
func TestFeasibility_InternalTokenIsAdminForEveryone(t *testing.T) {
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithInternalToken("boot-secret")

	p1, c1 := serveWith(t, m, "boot-secret", nil)
	p2, c2 := serveWith(t, m, "boot-secret", nil)
	if c1 != http.StatusOK || c2 != http.StatusOK {
		t.Fatalf("codes %d %d", c1, c2)
	}
	if p1.userID != InternalAgentUserID || p2.userID != InternalAgentUserID {
		t.Fatalf("got %q / %q, want both %q", p1.userID, p2.userID, InternalAgentUserID)
	}
	if !p1.isAdmin || !p2.isAdmin {
		t.Fatalf("internal token expected admin: %v %v", p1.isAdmin, p2.isAdmin)
	}
	if len(p1.tagIDs) != 0 {
		t.Fatalf("internal principal carries tags %v; tag filtering would apply", p1.tagIDs)
	}
	t.Log("REPRODUCED: all web spawns collapse to one synthetic admin, no tag filtering")
}

// TestFeasibility_UnauthUserIDHeaderIsSpoofable tests the "no auth, just send
// a user id header" idea. A user-id header with no token must NOT authenticate.
func TestFeasibility_UnauthUserIDHeaderIsSpoofable(t *testing.T) {
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithInternalToken("boot-secret")

	p, code := serveWith(t, m, "", map[string]string{"X-Wick-User-Id": "user-b"})
	if code != http.StatusUnauthorized || p.called {
		t.Fatalf("headerless request: code=%d called=%v, want 401/false", code, p.called)
	}
	// And a header must not override a token's identity.
	scoped := NewScopedTokens()
	m2 := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithScopedTokens(scoped)
	tokA, _ := scoped.Issue("user-a", nil)
	p2, code2 := serveWith(t, m2, tokA, map[string]string{"X-Wick-User-Id": "user-b"})
	if code2 != http.StatusOK || p2.userID != "user-a" {
		t.Fatalf("header override: user=%q code=%d, want user-a", p2.userID, code2)
	}
	t.Log("CONFIRMED: identity comes from the token, not a spoofable header")
}

// TestFeasibility_RevokeAndExpiryCutAccess covers session-reap cleanup.
func TestFeasibility_RevokeAndExpiryCutAccess(t *testing.T) {
	scoped := NewScopedTokens()
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithScopedTokens(scoped)

	tok, _ := scoped.Issue("user-a", nil)
	if _, code := serveWith(t, m, tok, nil); code != http.StatusOK {
		t.Fatalf("pre-revoke code=%d", code)
	}
	scoped.Revoke(tok)
	p, code := serveWith(t, m, tok, nil)
	if code != http.StatusUnauthorized || p.called {
		t.Fatalf("post-revoke code=%d called=%v, want 401", code, p.called)
	}
	t.Log("CONFIRMED: revoke cuts MCP access — usable for session reap")
}

// TestFeasibility_UnknownAndUnapprovedRejected covers the failure modes a
// per-user rollout must not regress: a stale token whose user is gone, and a
// pending-approval user.
func TestFeasibility_UnknownAndUnapprovedRejected(t *testing.T) {
	scoped := NewScopedTokens()
	m := NewAuthMiddleware(nil, feasUsers{}, nil, "").WithScopedTokens(scoped)

	tok, _ := scoped.Issue("ghost-user", nil)
	p, code := serveWith(t, m, tok, nil)
	if code != http.StatusUnauthorized || p.called {
		t.Fatalf("unknown user: code=%d called=%v, want 401", code, p.called)
	}
	if _, code := serveWith(t, m, ScopedTokenPrefix+"never-issued", nil); code != http.StatusUnauthorized {
		t.Fatalf("forged scoped token code=%d, want 401", code)
	}
	t.Log("CONFIRMED: stale/forged scoped tokens rejected")
}
