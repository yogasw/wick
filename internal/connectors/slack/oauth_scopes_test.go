package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bug this guards: the consent URL used to carry a hand-written
// five-scope list that never included channels:history, so every account
// connected through the button came back unable to read a public
// channel's history — and because reconnecting re-requests the same
// list, no amount of disconnect/reconnect could fix it.
func TestUserOAuthScopesCoversEveryOperation(t *testing.T) {
	got := make(map[string]struct{})
	for _, s := range strings.Split(userOAuthScopes(), ",") {
		got[s] = struct{}{}
	}

	for opKey, groups := range opScopes {
		for _, anyOf := range groups {
			for _, scope := range anyOf {
				_, ok := got[scope]
				assert.True(t, ok, "op %q needs scope %q but the consent URL never asks for it", opKey, scope)
			}
		}
	}
	// conversations.open (send.go's DM path) has no opScopes entry.
	assert.Contains(t, got, "im:write")
	assert.Contains(t, got, "mpim:write")
	// The regression itself, named.
	assert.Contains(t, got, "channels:history")
}

func TestUserOAuthScopesIsSortedAndDeduplicated(t *testing.T) {
	list := strings.Split(userOAuthScopes(), ",")
	seen := make(map[string]struct{}, len(list))
	for i, scope := range list {
		assert.NotEmpty(t, scope)
		_, dup := seen[scope]
		assert.False(t, dup, "scope %q listed twice", scope)
		seen[scope] = struct{}{}
		if i > 0 {
			assert.Less(t, list[i-1], scope, "scope list must be sorted so the URL is stable across builds")
		}
	}
}

// stubSlack serves one Slack Web API method per call and records what it
// received, so the tests can assert on the request rather than the reply.
func stubSlack(t *testing.T, handler func(method string, r *http.Request) any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		method := strings.TrimPrefix(r.URL.Path, "/")
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(handler(method, r)))
	}))
	t.Cleanup(srv.Close)
	prev := slackAPIBase
	slackAPIBase = srv.URL
	t.Cleanup(func() { slackAPIBase = prev })
}

func TestResolveUserScopesFromManifest(t *testing.T) {
	t.Run("returns the app's declared user scopes", func(t *testing.T) {
		var gotAuth, gotAppID string
		stubSlack(t, func(_ string, r *http.Request) any {
			gotAuth = r.Header.Get("Authorization")
			gotAppID = r.Form.Get("app_id")
			return map[string]any{"ok": true, "manifest": map[string]any{
				"oauth_config": map[string]any{"scopes": map[string]any{
					"bot":  []string{"chat:write"},
					"user": []string{"channels:history", "channels:read"},
				}},
			}}
		})
		scopes, err := resolveUserScopesFromManifest(context.Background(), map[string]string{
			"app_id":           "A123",
			"app_config_token": "xoxe.xoxp-tok",
		}, nil)
		require.NoError(t, err)
		// Bot scopes belong to the app's install, not to the account
		// being connected, so they must not leak into user_scope.
		assert.Equal(t, "channels:history,channels:read", scopes)
		assert.Equal(t, "Bearer xoxe.xoxp-tok", gotAuth)
		assert.Equal(t, "A123", gotAppID)
	})

	t.Run("says nothing when app config is not set up", func(t *testing.T) {
		scopes, err := resolveUserScopesFromManifest(context.Background(), map[string]string{}, nil)
		require.NoError(t, err)
		// "" is the signal to fall back to the derived list, and it must
		// not be an error: most instances never set an app config token.
		assert.Empty(t, scopes)
	})

	t.Run("rotates an expired token, persists the new pair, and retries", func(t *testing.T) {
		exports := 0
		stubSlack(t, func(method string, r *http.Request) any {
			switch method {
			case "apps.manifest.export":
				exports++
				if r.Header.Get("Authorization") != "Bearer fresh-tok" {
					return map[string]any{"ok": false, "error": "token_expired"}
				}
				return map[string]any{"ok": true, "manifest": map[string]any{
					"oauth_config": map[string]any{"scopes": map[string]any{"user": []string{"files:read"}}},
				}}
			case "tooling.tokens.rotate":
				assert.Equal(t, "xoxe-1-old", r.Form.Get("refresh_token"))
				return map[string]any{"ok": true, "token": "fresh-tok", "refresh_token": "xoxe-1-new"}
			}
			return map[string]any{"ok": false, "error": "unexpected_method"}
		})

		var saved map[string]string
		scopes, err := resolveUserScopesFromManifest(context.Background(), map[string]string{
			"app_id":                   "A123",
			"app_config_token":         "stale-tok",
			"app_config_refresh_token": "xoxe-1-old",
		}, func(updates map[string]string) error {
			saved = updates
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "files:read", scopes)
		assert.Equal(t, 2, exports, "should retry the export exactly once after rotating")
		// Slack invalidates the old refresh token on rotation, so both
		// halves have to be written back or the next rotation is dead.
		assert.Equal(t, "fresh-tok", saved["app_config_token"])
		assert.Equal(t, "xoxe-1-new", saved["app_config_refresh_token"])
	})

	t.Run("surfaces a failure rotating cannot fix", func(t *testing.T) {
		stubSlack(t, func(string, *http.Request) any {
			return map[string]any{"ok": false, "error": "missing_scope"}
		})
		_, err := resolveUserScopesFromManifest(context.Background(), map[string]string{
			"app_id":                   "A123",
			"app_config_token":         "xoxb-not-a-config-token",
			"app_config_refresh_token": "xoxe-1-old",
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing_scope")
	})

	t.Run("says nothing when the app declares no user scopes", func(t *testing.T) {
		stubSlack(t, func(string, *http.Request) any {
			return map[string]any{"ok": true, "manifest": map[string]any{
				"oauth_config": map[string]any{"scopes": map[string]any{"bot": []string{"chat:write"}}},
			}}
		})
		scopes, err := resolveUserScopesFromManifest(context.Background(), map[string]string{
			"app_id":           "A123",
			"app_config_token": "xoxe.xoxp-tok",
		}, nil)
		require.NoError(t, err)
		assert.Empty(t, scopes, "an app with no user scopes must fall back, not request nothing")
	})
}
