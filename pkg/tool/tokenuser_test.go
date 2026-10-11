package tool

import (
	"context"
	"net/http"
	"testing"
)

// withResolvers installs a PAT resolver that knows "good" and a header
// resolver that answers for X-Test-SSO, restoring the previous ones after.
func withResolvers(t *testing.T) {
	t.Helper()
	prevTok, prevHdr := tokenResolver, headerTokenResolver
	t.Cleanup(func() { tokenResolver, headerTokenResolver = prevTok, prevHdr })
	SetTokenResolver(func(_ context.Context, tok string) (User, bool) {
		if tok == "good" {
			return User{ID: "u-pat"}, true
		}
		return User{}, false
	})
	SetHeaderTokenResolver(func(r *http.Request, toolBase string) (User, bool) {
		switch {
		case toolBase != "/tools/t":
			return User{}, false
		case r.Header.Get("X-Test-SSO") == "system":
			return User{ID: SystemUserID, Source: TokenSourceSystem}, true
		case r.Header.Get("X-Test-SSO") != "":
			return User{ID: "u-sso", Source: TokenSourceSSO}, true
		}
		return User{}, false
	})
}

func TestTokenUserPATWinsAndIsTagged(t *testing.T) {
	withResolvers(t)
	c, _ := newCtx("", nil)
	c.R.Header.Set("Authorization", "Bearer good")
	c.R.Header.Set("X-Test-SSO", "1")
	u, ok := c.TokenUser()
	if !ok || u.ID != "u-pat" || u.Source != TokenSourcePAT {
		t.Fatalf("TokenUser = %+v, %v; want the PAT owner with source pat", u, ok)
	}
}

// A request with no bearer (an SSO MCP connector sends only X-Wick-User)
// must still be recognised through the header resolver.
func TestTokenUserFallsBackToHeaderResolver(t *testing.T) {
	withResolvers(t)
	c, _ := newCtx("", nil)
	c.R.Header.Set("X-Test-SSO", "1")
	u, ok := c.TokenUser()
	if !ok || u.ID != "u-sso" || u.Source != TokenSourceSSO {
		t.Fatalf("TokenUser = %+v, %v; want the SSO user", u, ok)
	}

	// An invalid bearer does not block a valid second credential.
	c.R.Header.Set("Authorization", "Bearer bad")
	if u, ok := c.TokenUser(); !ok || u.ID != "u-sso" {
		t.Fatalf("bad bearer + SSO: TokenUser = %+v, %v", u, ok)
	}
}

func TestTokenUserNoneOrNoResolver(t *testing.T) {
	withResolvers(t)
	c, _ := newCtx("", nil)
	if _, ok := c.TokenUser(); ok {
		t.Fatal("no credential: TokenUser ok")
	}
	headerTokenResolver = nil
	c.R.Header.Set("X-Test-SSO", "1")
	if _, ok := c.TokenUser(); ok {
		t.Fatal("no header resolver installed: TokenUser ok")
	}
}

// wick's system identity is a caller but not a person: TokenUser refuses
// it so `if _, ok := c.TokenUser(); ok` never admits it; TokenCaller
// returns it for the plugin proxy.
func TestTokenUserRefusesSystemTokenCallerReturnsIt(t *testing.T) {
	withResolvers(t)
	c, _ := newCtx("", nil)
	c.R.Header.Set("X-Test-SSO", "system")
	if u, ok := c.TokenUser(); ok {
		t.Fatalf("TokenUser admitted the system identity: %+v", u)
	}
	if u, ok := c.TokenCaller(); !ok || u.ID != SystemUserID || u.Source != TokenSourceSystem {
		t.Fatalf("TokenCaller = %+v, %v; want the system identity", u, ok)
	}
}
