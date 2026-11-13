package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// lendingTestScope lists one connector and lends it from its owner, the
// shape an agent with a "Lend" grant carries.
type lendingTestScope struct{ openScope }

func (lendingTestScope) LentBy(id string) (string, bool) { return "user-owner", id == "conn-lent" }
func (lendingTestScope) LentIDs() []string               { return []string{"conn-lent"} }

// approvedUsers resolves any id to an approved, non-admin user.
type approvedUsers struct{}

func (approvedUsers) GetUserByID(_ context.Context, id string) (*entity.User, error) {
	return &entity.User{ID: id, Name: id, Role: entity.RoleUser, Approved: true}, nil
}
func (approvedUsers) GetUserFilterTagIDs(context.Context, string) []string { return nil }

// patScopeFor sends one request authenticated by a real personal access
// token of userID, with the session header set to headerSession, and
// returns the agent scope the handler saw.
func patScopeFor(t *testing.T, userID, headerSession string) connectors.AgentScope {
	t.Helper()
	tokens := accesstoken.NewServiceFromDB(newTestDB(t))
	issued, err := tokens.Issue(context.Background(), userID, "script")
	if err != nil {
		t.Fatalf("issue pat: %v", err)
	}
	var got connectors.AgentScope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = connectors.AgentScopeFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	m := NewAuthMiddleware(tokens, approvedUsers{}, nil, "")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+issued.Token)
	req.Header.Set(handlers.SessionHeader, headerSession)
	rec := httptest.NewRecorder()
	m.Wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pat request: code=%d", rec.Code)
	}
	return got
}

// A PAT holder who names someone else's agent session in the header must
// not pick up the connectors that agent's owner lends: the header is
// typed by the caller, so it may narrow what they reach, never widen it.
func TestPATHeaderNamingAnotherSessionDoesNotLend(t *testing.T) {
	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return lendingTestScope{} })

	scope := patScopeFor(t, "user-stranger", "sess-of-someone-else")
	if scope == nil {
		t.Fatal("the named session's checklist must still narrow the caller")
	}
	if _, lends := scope.(connectors.LendingScope); lends {
		t.Fatal("a session named only by the header lent its owner's connectors to a stranger")
	}
	if !scope.AllowConnector("conn-lent") {
		t.Fatal("narrowing must keep the checklist itself intact")
	}
}

// withSessionOwner installs a session → owner map for the test.
func withSessionOwner(t *testing.T, owners map[string]string) {
	t.Helper()
	handlers.SetSessionOwnerResolver(func(sid string) (string, bool) {
		uid, ok := owners[sid]
		return uid, ok
	})
	t.Cleanup(func() { handlers.SetSessionOwnerResolver(nil) })
}

// The person who owns a session keeps its lend when their PAT names it.
func TestPATHeaderNamingOwnSessionKeepsLend(t *testing.T) {
	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return lendingTestScope{} })
	withSessionOwner(t, map[string]string{"sess-mine": "user-a"})

	if _, lends := patScopeFor(t, "user-a", "sess-mine").(connectors.LendingScope); !lends {
		t.Fatal("the session owner lost the lend on their own session")
	}
	if _, lends := patScopeFor(t, "user-b", "sess-mine").(connectors.LendingScope); lends {
		t.Fatal("ownership of the session was not checked against the caller")
	}
}

// A per-session token names its session in the grant: the lend applies
// there exactly as before, and a header naming another session is ignored.
func TestSessionTokenKeepsLend(t *testing.T) {
	withScopeResolver(t, func(_ context.Context, sid string) connectors.AgentScope {
		if sid == "sess-granted" {
			return lendingTestScope{}
		}
		return nil
	})
	st := NewScopedTokens()
	tok, err := st.IssueForSession("user-chatting", "sess-granted", nil, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var got connectors.AgentScope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = connectors.AgentScopeFrom(r.Context())
	})
	m := NewAuthMiddleware(nil, approvedUsers{}, nil, "").WithScopedTokens(st)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(handlers.SessionHeader, "sess-other")
	m.Wrap(next).ServeHTTP(httptest.NewRecorder(), req)
	if _, lends := got.(connectors.LendingScope); !lends {
		t.Fatal("a session token lost the lend of its own session")
	}
}

// wick's own internal token (legacy spawns that send only the header)
// keeps the lend of the session it names.
func TestInternalTokenHeaderKeepsLend(t *testing.T) {
	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return lendingTestScope{} })
	var got connectors.AgentScope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = connectors.AgentScopeFrom(r.Context())
	})
	m := NewAuthMiddleware(nil, internalStubUsers{}, nil, "").WithInternalToken("internal-secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer internal-secret")
	req.Header.Set(handlers.SessionHeader, "sess-legacy")
	m.Wrap(next).ServeHTTP(httptest.NewRecorder(), req)
	if _, lends := got.(connectors.LendingScope); !lends {
		t.Fatal("the internal token lost the lend of the session it names")
	}
}
