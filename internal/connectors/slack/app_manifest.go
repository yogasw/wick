// Package slack — app_manifest.go: reads the Slack app's declared User
// Token Scopes so the consent URL can ask for exactly those.
//
// Purpose: A user token only ever carries what the authorize URL asked
// for (unioned with what that user already granted the app), so a scope
// added in App Management never reaches anybody until wick requests it.
// Reconnecting does not help, which is the whole trap this file removes:
// with an app configuration token present, every connect and re-connect
// reads oauth_config.scopes.user straight from the app and requests that.
//
// Why a third credential: the app manifest is only readable with an app
// CONFIGURATION token (xoxe.xoxp-…, minted at api.slack.com/apps → Your
// Apps → App Configuration Tokens). Bot and user tokens both answer
// apps.manifest.export with missing_scope — the scope it wants,
// app_configurations:write, is not offered in either token's scope
// picker. Those tokens cannot be made to work, which is why this is
// opt-in config rather than something derived from what is already there.
//
// Caller:   SlackOAuthMeta().ResolveScopes, via the manager OAuth handler
// Dependencies: Slack Web API (apps.manifest.export, tooling.tokens.rotate)
// Main Functions:
//   - resolveUserScopesFromManifest() — ResolveScopes implementation
//
// Side Effects: rotates the app configuration token when it has expired
// and persists the new pair through the save callback.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// slackAPIBase is a var so the tests can point it at a stub server.
var slackAPIBase = "https://slack.com/api"

// resolveUserScopesFromManifest implements connector.OAuthMeta.ResolveScopes.
//
// Returns "" — "no opinion", the manager then falls back to the derived
// list — in three cases: the app configuration credentials are not set
// up, the app declares no user scopes at all, or the manifest has none
// of the fields we read. It returns an error only when the credentials
// ARE configured and the lookup genuinely failed, so a misconfigured
// token is visible in the log instead of silently degrading forever.
func resolveUserScopesFromManifest(ctx context.Context, cfgs map[string]string, save func(map[string]string) error) (string, error) {
	appID := strings.TrimSpace(cfgs["app_id"])
	token := strings.TrimSpace(cfgs["app_config_token"])
	refresh := strings.TrimSpace(cfgs["app_config_refresh_token"])
	if appID == "" || (token == "" && refresh == "") {
		return "", nil
	}

	scopes, err := exportUserScopes(ctx, token, appID)
	if err == nil {
		return scopes, nil
	}
	// A configuration token lives ~12h, so an expired one is the normal
	// state rather than an exception: rotate and retry once. Anything
	// else (wrong app_id, a token missing app_configurations:write) is
	// not fixable by rotating, so it surfaces as-is.
	if !isExpiredConfigToken(err) || refresh == "" {
		return "", err
	}
	newToken, newRefresh, rotErr := rotateAppConfigToken(ctx, refresh)
	if rotErr != nil {
		return "", fmt.Errorf("app config token expired and rotate failed: %w", rotErr)
	}
	if save != nil {
		if saveErr := save(map[string]string{
			"app_config_token":         newToken,
			"app_config_refresh_token": newRefresh,
		}); saveErr != nil {
			// The lookup can still proceed on the in-memory pair; the
			// next connect just has to rotate again.
			return exportUserScopes(ctx, newToken, appID)
		}
	}
	return exportUserScopes(ctx, newToken, appID)
}

// exportUserScopes calls apps.manifest.export and joins
// oauth_config.scopes.user into the comma-separated form the authorize
// URL wants. Bot scopes are deliberately ignored: they belong to the
// app's own installation, not to the account being connected.
func exportUserScopes(ctx context.Context, token, appID string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("no app config token")
	}
	var out struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Manifest struct {
			OAuthConfig struct {
				Scopes struct {
					User []string `json:"user"`
				} `json:"scopes"`
			} `json:"oauth_config"`
		} `json:"manifest"`
	}
	if err := slackAPIForm(ctx, "apps.manifest.export", token, url.Values{"app_id": {appID}}, &out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("apps.manifest.export: %s", errorOrUnknown(out.Error))
	}
	return strings.Join(out.Manifest.OAuthConfig.Scopes.User, ","), nil
}

// rotateAppConfigToken trades a refresh token for a fresh
// token/refresh_token pair. Slack invalidates the old refresh token in
// the process, so the caller must persist both halves or the next
// rotation has nothing to rotate.
func rotateAppConfigToken(ctx context.Context, refreshToken string) (token, refresh string, err error) {
	var out struct {
		OK           bool   `json:"ok"`
		Error        string `json:"error"`
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := slackAPIForm(ctx, "tooling.tokens.rotate", "", url.Values{"refresh_token": {refreshToken}}, &out); err != nil {
		return "", "", err
	}
	if !out.OK {
		return "", "", fmt.Errorf("tooling.tokens.rotate: %s", errorOrUnknown(out.Error))
	}
	if out.Token == "" || out.RefreshToken == "" {
		return "", "", fmt.Errorf("tooling.tokens.rotate: response missing a token")
	}
	return out.Token, out.RefreshToken, nil
}

// slackAPIForm posts a form-encoded call to a Slack Web API method and
// decodes the JSON reply into out. token may be empty for the methods
// that carry their credential in the body (tooling.tokens.rotate).
func slackAPIForm(ctx context.Context, method, token string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPIBase+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: decode response: %w", method, err)
	}
	return nil
}

// isExpiredConfigToken reports whether the failure is the routine
// "your 12-hour token aged out" and nothing worse.
func isExpiredConfigToken(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "token_expired") || strings.Contains(msg, "invalid_auth")
}

func errorOrUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown_error"
	}
	return s
}
