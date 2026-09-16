package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// tokenAuth is an Authenticator that can also name the credential — what
// accesstoken.Service does in production.
type tokenAuth struct {
	wantToken string
	row       entity.PersonalAccessToken
}

func (a *tokenAuth) Authenticate(ctx context.Context, plain string) (string, error) {
	row, err := a.AuthenticateToken(ctx, plain)
	if err != nil {
		return "", err
	}
	return row.UserID, nil
}

func (a *tokenAuth) AuthenticateToken(_ context.Context, plain string) (*entity.PersonalAccessToken, error) {
	if plain != a.wantToken {
		return nil, errAuth("invalid token")
	}
	row := a.row
	return &row, nil
}

// channelWithAuth is newTestChannel with a chosen Authenticator, capturing
// the context each dispatch carries rather than the text.
func channelWithAuth(t *testing.T, auth Authenticator) (*Channel, func() []context.Context) {
	t.Helper()
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, auth)
	var mu sync.Mutex
	var seen []context.Context
	ch.SetSessionChecker(fakeSessions{})
	ch.SetSendFunc(func(ctx context.Context, sessionID, _, _, role, _ string) error {
		mu.Lock()
		seen = append(seen, ctx)
		mu.Unlock()
		if role == "user" {
			go func() {
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
			}()
		}
		return nil
	})
	return ch, func() []context.Context {
		mu.Lock()
		defer mu.Unlock()
		out := append([]context.Context(nil), seen...)
		return out
	}
}

func post(t *testing.T, ch *Channel, token string) *httptest.ResponseRecorder {
	t.Helper()
	stubModels(t, "claude")
	return postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", token, map[string]any{
		"model":    "claude",
		"messages": []map[string]string{{"role": "user", "content": "halo"}},
	})
}

// The bug this fixes: REST never stamped the caller, so every session it
// created was ownerless. Hundreds of conversations belonged to nobody on
// the analytics page, and — worse than cosmetic — each spawn minted its
// MCP credential from an ownerless session and fell back to the shared
// internal token instead of the caller's own.
func TestRESTDispatchCarriesTheCaller(t *testing.T) {
	auth := &tokenAuth{
		wantToken: "good",
		row: entity.PersonalAccessToken{
			ID: "tok-1", UserID: "user-1", Name: "ci-runner", Last4: "beef",
		},
	}
	ch, contexts := channelWithAuth(t, auth)

	if w := post(t, ch, "good"); w.Code != http.StatusOK {
		t.Fatalf("dispatch failed: %d %s", w.Code, w.Body.String())
	}
	waitFor(t, func() bool { return len(contexts()) > 0 })

	for _, ctx := range contexts() {
		if got := agentchannels.CallerUserID(ctx); got != "user-1" {
			t.Errorf("caller user id = %q, want the token's owner", got)
		}
		tok := agentchannels.CallerTokenFrom(ctx)
		if tok.ID != "tok-1" || tok.Name != "ci-runner" {
			t.Errorf("caller token = %+v, want the credential that authenticated", tok)
		}
	}
}

// One account can hold several tokens. Recording which one called is the
// difference between "revoke something of Yoga's" and "revoke the CI
// token", so the id must be the token's own — not the user's.
func TestRESTTokenIdentityIsPerToken(t *testing.T) {
	for _, tc := range []struct{ id, name string }{
		{"tok-ci", "ci-runner"},
		{"tok-laptop", "laptop"},
	} {
		auth := &tokenAuth{wantToken: "good", row: entity.PersonalAccessToken{ID: tc.id, UserID: "user-1", Name: tc.name}}
		ch, contexts := channelWithAuth(t, auth)
		if w := post(t, ch, "good"); w.Code != http.StatusOK {
			t.Fatalf("dispatch failed: %d", w.Code)
		}
		waitFor(t, func() bool { return len(contexts()) > 0 })
		if got := agentchannels.CallerTokenFrom(contexts()[0]); got.ID != tc.id || got.Name != tc.name {
			t.Errorf("token = %+v, want %s/%s", got, tc.id, tc.name)
		}
	}
}

// An Authenticator that cannot name the token still authenticates. The
// identity is the same; only the audit detail is missing, and a plugin or
// a test double should not have to implement a method to keep working.
func TestRESTFallsBackToPlainAuthenticator(t *testing.T) {
	ch, contexts := channelWithAuth(t, &fakeAuth{wantToken: "good", userID: "user-9"})
	if w := post(t, ch, "good"); w.Code != http.StatusOK {
		t.Fatalf("dispatch failed: %d %s", w.Code, w.Body.String())
	}
	waitFor(t, func() bool { return len(contexts()) > 0 })
	ctx := contexts()[0]
	if got := agentchannels.CallerUserID(ctx); got != "user-9" {
		t.Errorf("caller user id = %q, want user-9", got)
	}
	if got := agentchannels.CallerTokenFrom(ctx); got.ID != "" {
		t.Errorf("token = %+v, want nothing recorded rather than a guess", got)
	}
}

// A refused token must reach neither the pool nor the session store.
func TestRESTBadTokenDispatchesNothing(t *testing.T) {
	ch, contexts := channelWithAuth(t, &tokenAuth{wantToken: "good", row: entity.PersonalAccessToken{ID: "t", UserID: "u"}})
	if w := post(t, ch, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if n := len(contexts()); n != 0 {
		t.Errorf("%d dispatches on a refused token, want none", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the dispatch")
}
