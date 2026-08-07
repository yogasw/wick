package custom

import (
	"context"
	"net/url"
	"testing"
)

// The callback branches on Popup to decide between closing the window and
// redirecting to the instance page. An instance-bound login opened from a
// popup that redirected instead would strand the window on a page nobody is
// looking at, and the opener would wait for a signal that never comes — the
// "clicked Re-connect, nothing happened" bug.
func TestOAuthPopupFlagSurvivesRoundTrip(t *testing.T) {
	ctx := context.Background()
	redirect := "http://wick.local/cb"

	t.Run("popup login reports Popup", func(t *testing.T) {
		svc, mcpURL, _ := newOAuthFixture(t)
		authURL, _, err := svc.StartOAuthLogin(ctx, &ServerForm{Label: "x", URL: mcpURL, AuthScheme: "oauth"}, redirect, "")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		res, err := svc.CompleteOAuthLogin(ctx, stateOf(t, authURL), "code-1", redirect)
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if !res.Popup {
			t.Error("Popup = false; the edit-form login runs in a popup and must close itself")
		}
	})

	// The full-page flow from the instance page must keep redirecting: it has
	// no opener to signal, and the user expects to land back on the instance.
	t.Run("full-page instance login does not report Popup", func(t *testing.T) {
		svc, _, _ := newOAuthFixture(t)
		meta := oauthClientMeta{
			AuthEndpoint:  "https://as.example.com/authorize",
			TokenEndpoint: "https://as.example.com/token",
			ClientID:      "cid",
		}
		authURL, _, err := svc.newLogin(ServerForm{URL: "https://mcp.example.com", AuthScheme: "oauth"}, meta, redirect, "inst-1", false)
		if err != nil {
			t.Fatalf("newLogin: %v", err)
		}
		login, ok := svc.logins.byState(stateOf(t, authURL))
		if !ok {
			t.Fatal("login session not found by state")
		}
		if login.Popup {
			t.Error("Popup = true for a full-page login; the callback would close a window that has no opener")
		}
		if login.InstanceID != "inst-1" {
			t.Errorf("InstanceID = %q, want inst-1", login.InstanceID)
		}
	})

	t.Run("popup instance login reports Popup", func(t *testing.T) {
		svc, _, _ := newOAuthFixture(t)
		meta := oauthClientMeta{
			AuthEndpoint:  "https://as.example.com/authorize",
			TokenEndpoint: "https://as.example.com/token",
			ClientID:      "cid",
		}
		authURL, _, err := svc.newLogin(ServerForm{URL: "https://mcp.example.com", AuthScheme: "oauth"}, meta, redirect, "inst-1", true)
		if err != nil {
			t.Fatalf("newLogin: %v", err)
		}
		login, ok := svc.logins.byState(stateOf(t, authURL))
		if !ok {
			t.Fatal("login session not found by state")
		}
		if !login.Popup {
			t.Error("Popup = false; a row-menu Connect opens a popup and must close it on completion")
		}
	})
}

func stateOf(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("state missing from authorization URL")
	}
	return state
}
