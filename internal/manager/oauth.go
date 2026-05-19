// Package manager — oauth.go: Generic connector OAuth 2.0 flow handler.
//
// Purpose: Implements the two-leg OAuth 2.0 flow for any connector that has
// Module.OAuth set to a non-nil *connector.OAuthMeta. Routes are registered
// under /manager/connectors/{key}/oauth/* so the Redirect URI is always
// /manager/connectors/{key}/oauth/callback.
//
// Caller:   Handler.connectorRoutes() (public, not behind auth middleware)
// Dependencies: connector.OAuthMeta, configs.Service, connectors.Service
// Main Functions:
//   - oauthRoutes()      — registers start + callback routes (public)
//   - oauthStart()       — builds state, redirects to provider consent page
//   - oauthCallback()    — validates state, exchanges code, saves token
//   - oauthSaveToken()   — persists xoxp/access token to connector row
//
// Storage:
//   - OAuth App credentials: configs owner "connector_oauth:{key}"
//     keys: "client_id", "client_secret"
//   - Pending states:       Handler.oauthPending (sync.Map, 10-min TTL)
//
// Side Effects: writes access token to connector row via connectors.Service.
package manager

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/yogasw/wick/pkg/connector"
)

// oauthStateEntry stores a pending OAuth state with its expiry and optional
// connector row ID so the callback knows which row to update.
type oauthStateEntry struct {
	key            string    // connector key (e.g. "slack")
	expiresAt      time.Time
	connectorRowID string // non-empty = update this specific row
}

// oauthRoutes registers the public (no-auth) OAuth 2.0 routes for all connectors.
// The key path parameter selects which connector's OAuthMeta to use.
// These routes are intentionally NOT behind authMidd because the OAuth callback
// comes from the provider and cannot carry a wick session cookie.
func (h *Handler) oauthRoutes(mux *http.ServeMux) {
	// start is accessed by the logged-in user clicking "Connect"; no bearer
	// is needed because the Redirect URI is what carries the session back.
	mux.Handle("GET /manager/connectors/{key}/oauth/start", http.HandlerFunc(h.oauthStart))
	// callback is public — the provider redirects here after user consent.
	mux.Handle("GET /manager/connectors/{key}/oauth/callback", http.HandlerFunc(h.oauthCallback))
}

// oauthStart redirects the browser to the connector provider's OAuth consent page.
// A cryptographically-signed state token is generated, stored with a 10-minute
// TTL, and sent to the provider so the callback can verify the round-trip.
func (h *Handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok || mod.OAuth == nil {
		http.Error(w, "connector not found or OAuth not supported", http.StatusNotFound)
		return
	}

	clientID := h.configs.GetOwned("connector_oauth:"+key, "client_id")
	if clientID == "" {
		http.Error(w, "OAuth not configured — set Client ID in Manager → Connectors → "+mod.OAuth.DisplayName, http.StatusServiceUnavailable)
		return
	}

	connectorRowID := r.URL.Query().Get("connector_id")

	state, err := h.generateOAuthState(key)
	if err != nil {
		log.Error().Err(err).Str("connector", key).Msg("manager oauth: failed to generate state token")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.oauthPending.Store(state, oauthStateEntry{
		key:            key,
		expiresAt:      time.Now().Add(10 * time.Minute),
		connectorRowID: connectorRowID,
	})

	redirectURI := h.oauthRedirectURI(r, key)

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("user_scope", mod.OAuth.Scopes) // Slack uses user_scope; harmless for others
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)

	http.Redirect(w, r, mod.OAuth.AuthorizeURL+"?"+params.Encode(), http.StatusTemporaryRedirect)
}

// oauthCallback handles the redirect from the provider after the user approves
// (or denies) the OAuth grant.
//
//  1. Validates the state token against the pending map.
//  2. Exchanges the authorization code for an access token via oauth.v2.access (Slack).
//  3. Calls GetUserIdentity to resolve the user's display name.
//  4. Persists the token to the connector row.
//  5. Shows a success page (or error page on failure).
func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	q := r.URL.Query()

	// User denied the grant.
	if errParam := q.Get("error"); errParam != "" {
		log.Warn().Str("connector", key).Str("oauth_error", errParam).Msg("manager oauth: user denied grant")
		http.Error(w, "OAuth denied: "+errParam, http.StatusBadRequest)
		return
	}

	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	// Validate and consume state token.
	val, ok := h.oauthPending.LoadAndDelete(state)
	if !ok {
		log.Warn().Str("connector", key).Msg("manager oauth: unknown or already-used state token")
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}
	entry := val.(oauthStateEntry)
	if entry.key != key {
		log.Warn().Str("connector", key).Str("state_key", entry.key).Msg("manager oauth: state key mismatch")
		http.Error(w, "invalid state (key mismatch)", http.StatusBadRequest)
		return
	}
	if time.Now().After(entry.expiresAt) {
		log.Warn().Str("connector", key).Msg("manager oauth: state token expired")
		http.Error(w, "state token expired", http.StatusBadRequest)
		return
	}

	mod, ok := h.connectors.Module(key)
	if !ok || mod.OAuth == nil {
		http.Error(w, "connector not found or OAuth not supported", http.StatusNotFound)
		return
	}

	clientID := h.configs.GetOwned("connector_oauth:"+key, "client_id")
	clientSecret := h.configs.GetOwned("connector_oauth:"+key, "client_secret")
	if clientID == "" || clientSecret == "" {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	redirectURI := h.oauthRedirectURI(r, key)

	// Exchange code for token. Slack uses oauth.v2.access with user_scope.
	accessToken, slackUserID, err := h.exchangeSlackCode(r.Context(), clientID, clientSecret, code, redirectURI)
	if err != nil {
		log.Error().Err(err).Str("connector", key).Msg("manager oauth: token exchange failed")
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Resolve display name via GetUserIdentity.
	userID, displayName, err := mod.OAuth.GetUserIdentity(r.Context(), accessToken)
	if err != nil {
		log.Warn().Err(err).Str("connector", key).Msg("manager oauth: GetUserIdentity failed; using token user ID as display name")
		// Use the slack user ID from token exchange as fallback
		userID = slackUserID
		displayName = slackUserID
	}

	// Persist token to connector row.
	if saveErr := h.oauthSaveToken(r.Context(), key, userID, displayName, accessToken, entry.connectorRowID, mod.OAuth); saveErr != nil {
		log.Error().Err(saveErr).Str("connector", key).Str("user_id", userID).Msg("manager oauth: oauthSaveToken failed")
		http.Error(w, "failed to save token: "+saveErr.Error(), http.StatusInternalServerError)
		return
	}

	log.Info().Str("connector", key).Str("user_id", userID).Str("display_name", displayName).
		Str("connector_row_id", entry.connectorRowID).
		Msg("manager oauth: user token saved successfully")

	// Redirect to connector detail page or show success page.
	if entry.connectorRowID != "" {
		http.Redirect(w, r,
			"/manager/connectors/"+key+"/"+entry.connectorRowID+"?oauth=success&user="+url.QueryEscape(displayName),
			http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>%s Connected</title></head>
<body style="font-family:sans-serif;max-width:480px;margin:60px auto;text-align:center">
  <h2>&#x2713; %s connected</h2>
  <p>Your account (<strong>@%s</strong>) has been connected to wick.</p>
  <p><a href="/manager/connectors/%s">Back to %s connectors</a></p>
</body>
</html>`,
		mod.OAuth.DisplayName,
		mod.OAuth.DisplayName,
		strings.ReplaceAll(displayName, "<", "&lt;"),
		key,
		mod.OAuth.DisplayName,
	)
}

// exchangeSlackCode exchanges a Slack authorization code for an xoxp user token
// via Slack's oauth.v2.access endpoint.
func (h *Handler) exchangeSlackCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (accessToken, slackUserID string, err error) {
	resp, err := slackgo.GetOAuthV2ResponseContext(ctx, http.DefaultClient, clientID, clientSecret, code, redirectURI)
	if err != nil {
		return "", "", err
	}
	if resp.AuthedUser.AccessToken == "" || resp.AuthedUser.ID == "" {
		return "", "", fmt.Errorf("empty token or user ID in Slack response")
	}
	return resp.AuthedUser.AccessToken, resp.AuthedUser.ID, nil
}

// oauthSaveToken persists an access token to the correct connector row.
// When connectorRowID is non-empty, updates that row directly.
// Otherwise scans for an existing user_token row for the same user, updates it,
// or creates a new row if none found.
func (h *Handler) oauthSaveToken(ctx context.Context, key, userID, displayName, accessToken, connectorRowID string, meta *connector.OAuthMeta) error {
	rows, err := h.connectors.ListByKey(ctx, key)
	if err != nil {
		return fmt.Errorf("oauth save: list connectors for key %q: %w", key, err)
	}

	// Fast path: connector_id known — update that row directly.
	if connectorRowID != "" {
		for _, row := range rows {
			if row.ID == connectorRowID {
				if err := h.connectors.Update(ctx, row.ID, row.Label, map[string]string{
					"auth_mode":  "user_token",
					"user_token": accessToken,
				}, row.Disabled); err != nil {
					return fmt.Errorf("oauth save: update row %s: %w", row.ID, err)
				}
				log.Info().Str("connector", key).Str("user_id", userID).Str("connector_id", row.ID).
					Msg("manager oauth: updated connector row (from detail page button)")
				return nil
			}
		}
	}

	// Slow path: scan for existing user_token row for this user.
	for _, row := range rows {
		cfgs := h.connectors.LoadConfigs(row)
		if strings.TrimSpace(cfgs["auth_mode"]) != "user_token" {
			continue
		}
		// Use GetUserIdentity to check if the existing token belongs to the same user.
		existingToken := strings.TrimSpace(cfgs["user_token"])
		if existingToken == "" {
			continue
		}
		if meta.GetUserIdentity != nil {
			existingUID, _, identErr := meta.GetUserIdentity(ctx, existingToken)
			if identErr == nil && existingUID == userID {
				if err := h.connectors.Update(ctx, row.ID, row.Label, map[string]string{"user_token": accessToken}, row.Disabled); err != nil {
					return fmt.Errorf("oauth save: update row %s: %w", row.ID, err)
				}
				log.Info().Str("connector", key).Str("user_id", userID).Str("connector_id", row.ID).
					Msg("manager oauth: updated existing connector row")
				return nil
			}
		}
	}

	// No existing row — create new.
	newRow, err := h.connectors.Create(ctx, key, meta.DisplayName+" – @"+displayName, map[string]string{
		"auth_mode":  "user_token",
		"user_token": accessToken,
	}, "oauth")
	if err != nil {
		return fmt.Errorf("oauth save: create connector row: %w", err)
	}
	log.Info().Str("connector", key).Str("user_id", userID).Str("connector_id", newRow.ID).
		Msg("manager oauth: created new connector row")
	return nil
}

// generateOAuthState generates a cryptographically-signed state token of the
// form HMAC-SHA256(secret, timestamp+"|"+randomHex+"|"+key). The random
// component ensures uniqueness; the HMAC ensures forgery resistance.
func (h *Handler) generateOAuthState(key string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	payload := ts + "|" + hex.EncodeToString(nonce) + "|" + key

	mac := hmac.New(sha256.New, h.oauthSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return payload + "." + sig, nil
}

// oauthRedirectURI builds the absolute redirect URI for the OAuth callback.
// It derives the scheme+host from the incoming request when AppURL is not set.
func (h *Handler) oauthRedirectURI(r *http.Request, key string) string {
	appURL := h.configs.AppURL()
	base := strings.TrimRight(appURL, "/")
	if base == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/manager/connectors/" + key + "/oauth/callback"
}

