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
//   - oauthRoutes()                   — registers start + callback routes (public)
//   - oauthStart()                    — builds state, redirects to provider consent page
//   - oauthCallback()                 — validates state, exchanges code, saves token
//   - oauthSaveToken()                — persists xoxp/access token to connector row
//   - migrateOAuthAppToInstances()    — one-shot boot migration: copies shared
//     connector_oauth:{key} credentials into every
//     instance row that has not yet been configured.
//
// Storage:
//   - Per-instance OAuth credentials: connector row configs (client_id, client_secret)
//   - Legacy shared credentials:      configs owner "connector_oauth:{key}" (migrated away)
//   - Pending states:                 Handler.oauthPending (sync.Map, 10-min TTL)
//
// Side Effects: writes access token to connector row via connectors.Service.
package manager

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/login"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/yogasw/wick/pkg/connector"
)

// pluginIdentityResolver resolves an OAuth token's owner via a connector
// plugin subprocess. *connectorsplugin.Manager satisfies it. nil when no
// plugins are loaded.
type pluginIdentityResolver interface {
	IsPlugin(key string) bool
	ResolveIdentity(ctx context.Context, key, accessToken string) (userID, displayName string, err error)
}

// oauthStateEntry stores a pending OAuth state with its expiry and optional
// connector row ID so the callback knows which row to update.
type oauthStateEntry struct {
	key            string // connector key (e.g. "slack")
	expiresAt      time.Time
	connectorRowID string // non-empty = update this specific row
	wickUserID     string // wick user who initiated the flow (for owner tag)
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
// client_id is read from the connector row's per-instance configs (field "client_id").
// connector_id query param is required — the row must already exist.
func (h *Handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok || mod.OAuth == nil {
		http.Error(w, "connector not found or OAuth not supported", http.StatusNotFound)
		return
	}

	connectorRowID := r.URL.Query().Get("connector_id")
	if connectorRowID == "" {
		http.Error(w, "connector_id is required", http.StatusBadRequest)
		return
	}

	row, err := h.connectors.Get(r.Context(), connectorRowID)
	if err != nil || row.Key != key {
		http.Error(w, "connector row not found", http.StatusNotFound)
		return
	}

	// Gate: instance must have EnableSSO=true.
	if !row.EnableSSO {
		http.Error(w, "SSO not enabled on this instance", http.StatusForbidden)
		return
	}
	// Gate: only admin or rows with AllowOthersConnectSSO=true.
	if !h.canConnectSSO(login.GetUser(r.Context()), row) {
		http.Error(w, "SSO connect not allowed on this instance", http.StatusForbidden)
		return
	}

	cfgs := h.connectors.LoadConfigs(*row)
	clientID := strings.TrimSpace(cfgs["client_id"])
	if clientID == "" {
		http.Error(w, "OAuth not configured — set Client ID in the connector's Credentials section first", http.StatusServiceUnavailable)
		return
	}

	state, err := h.generateOAuthState(key)
	if err != nil {
		log.Error().Err(err).Str("connector", key).Msg("manager oauth: failed to generate state token")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	caller := login.GetUser(r.Context())
	var callerWickID string
	if caller != nil {
		callerWickID = caller.ID
	}
	h.oauthPending.Store(state, oauthStateEntry{
		key:            key,
		expiresAt:      time.Now().Add(10 * time.Minute),
		connectorRowID: connectorRowID,
		wickUserID:     callerWickID,
	})

	redirectURI := h.oauthRedirectURI(r, key)

	params := url.Values{}
	params.Set("client_id", clientID)
	if mod.OAuth.TokenURL == "" {
		params.Set("user_scope", mod.OAuth.Scopes)
	} else {
		params.Set("scope", mod.OAuth.Scopes)
	}
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	for k, v := range mod.OAuth.ExtraParams {
		params.Set(k, v)
	}

	http.Redirect(w, r, mod.OAuth.AuthorizeURL+"?"+params.Encode(), http.StatusTemporaryRedirect)
}

// oauthCallback handles the redirect from the provider after the user approves
// (or denies) the OAuth grant.
//
//  1. Validates the state token against the pending map.
//  2. Exchanges the authorization code for an access token via oauth.v2.access (Slack).
//  3. Calls GetUserIdentity to resolve the user's display name.
//  4. Persists the token: MultiAccount=true creates a new row; false updates the
//     existing row identified by connectorRowID in the state entry.
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

	// Read client credentials from the originating row.
	row, err := h.connectors.Get(r.Context(), entry.connectorRowID)
	if err != nil {
		http.Error(w, "connector row not found", http.StatusNotFound)
		return
	}
	cfgs := h.connectors.LoadConfigs(*row)
	clientID := strings.TrimSpace(cfgs["client_id"])
	clientSecret := strings.TrimSpace(cfgs["client_secret"])
	if clientID == "" || clientSecret == "" {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	redirectURI := h.oauthRedirectURI(r, key)

	// Exchange code for token. Generic standard OAuth2 when TokenURL is set; Slack-specific otherwise.
	var accessToken, refreshToken, fallbackUserID string
	if mod.OAuth.TokenURL != "" {
		accessToken, refreshToken, err = h.exchangeGenericCode(r.Context(), mod.OAuth.TokenURL, clientID, clientSecret, code, redirectURI)
	} else {
		accessToken, fallbackUserID, err = h.exchangeSlackCode(r.Context(), clientID, clientSecret, code, redirectURI)
	}
	if err != nil {
		log.Error().Err(err).Str("connector", key).Msg("manager oauth: token exchange failed")
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Resolve display name. Plugin connectors resolve over gRPC (their
	// GetUserIdentity func cannot cross the process boundary); in-process
	// connectors call GetUserIdentity directly.
	var userID, displayName string
	if h.pluginResolver != nil && h.pluginResolver.IsPlugin(key) {
		userID, displayName, err = h.pluginResolver.ResolveIdentity(r.Context(), key, accessToken)
	} else if mod.OAuth.GetUserIdentity != nil {
		userID, displayName, err = mod.OAuth.GetUserIdentity(r.Context(), accessToken)
	} else {
		err = fmt.Errorf("connector %q has no identity resolver", key)
	}
	if err != nil {
		if fallbackUserID == "" {
			log.Error().Err(err).Str("connector", key).Msg("manager oauth: GetUserIdentity failed, no fallback user ID")
			http.Error(w, "failed to resolve user identity: "+err.Error(), http.StatusBadGateway)
			return
		}
		log.Warn().Err(err).Str("connector", key).Msg("manager oauth: GetUserIdentity failed; using fallback user ID")
		userID = fallbackUserID
		displayName = fallbackUserID
	}

	// Persist token to connector row. Pass the wick platform user ID (from the
	// OAuth state entry) as wickUserID and the provider-side user ID as externalUserID.
	savedRowID, saveErr := h.oauthSaveToken(r.Context(), key, entry.wickUserID, userID, displayName, accessToken, entry.connectorRowID, mod.OAuth, row.MultiAccount)
	if saveErr != nil {
		log.Error().Err(saveErr).Str("connector", key).Str("user_id", userID).Msg("manager oauth: oauthSaveToken failed")
		http.Error(w, "failed to save token: "+saveErr.Error(), http.StatusInternalServerError)
		return
	}

	log.Info().Str("connector", key).Str("user_id", userID).Str("display_name", displayName).
		Str("connector_row_id", savedRowID).
		Msg("manager oauth: user token saved successfully")

	// Persist refresh_token when received (e.g. Google OAuth offline access).
	if refreshToken != "" {
		if err := h.connectors.Update(r.Context(), savedRowID, row.Label,
			map[string]string{"refresh_token": refreshToken}, row.Disabled); err != nil {
			log.Warn().Err(err).Str("connector", key).Msg("manager oauth: failed to save refresh token")
		}
	}

	// Assign owner tag when a new row was created (MultiAccount flow).
	if h.tags != nil && row.MultiAccount && entry.wickUserID != "" {
		if err := h.tags.CreateOwnerTag(r.Context(), savedRowID, entry.wickUserID); err != nil {
			log.Warn().Err(err).Str("row_id", savedRowID).Msg("manager oauth: create owner tag failed")
		}
	}

	http.Redirect(w, r,
		"/manager/connectors/"+key+"?oauth=success&user="+url.QueryEscape(displayName),
		http.StatusSeeOther)
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

// exchangeGenericCode exchanges an authorization code for tokens using a
// standard HTTP POST per RFC 6749. Returns accessToken and, if present,
// refreshToken (e.g. Google offline access).
func (h *Handler) exchangeGenericCode(ctx context.Context, tokenURL, clientID, clientSecret, code, redirectURI string) (accessToken, refreshToken string, err error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Error != "" {
		return "", "", fmt.Errorf("token error: %s: %s", result.Error, result.ErrorDesc)
	}
	if result.AccessToken == "" {
		return "", "", fmt.Errorf("empty access_token in token response")
	}
	return result.AccessToken, result.RefreshToken, nil
}

// oauthSaveToken persists an access token as a ConnectorAccount under the
// originating connector row. MultiAccount behaviour is handled by the repo
// upsert: false = replace existing account, true = add new account.
// wickUserID is the wick platform user; externalUserID is the provider-side ID.
func (h *Handler) oauthSaveToken(ctx context.Context, key, wickUserID, externalUserID, displayName, accessToken, connectorRowID string, meta *connector.OAuthMeta, multiAccount bool) (savedRowID string, err error) {
	if connectorRowID == "" {
		return "", fmt.Errorf("oauth save: connector_id is required")
	}
	if err := h.connectors.SaveAccount(ctx, connectorRowID, wickUserID, externalUserID, displayName, accessToken); err != nil {
		return "", fmt.Errorf("oauth save: %w", err)
	}
	log.Info().Str("connector", key).Str("wick_user_id", wickUserID).Str("external_user_id", externalUserID).
		Str("connector_id", connectorRowID).Str("display_name", displayName).Msg("manager oauth: account saved")
	return connectorRowID, nil
}

// migrateOAuthAppToInstances copies the legacy shared OAuth App credentials
// stored under owner="connector_oauth:{key}" into every instance row of that
// connector that does not yet have client_id configured. Called once at boot
// from connectorRoutes so admins do not need to re-enter credentials.
//
// The migration is idempotent: rows that already have a client_id are skipped.
// After all rows are migrated the legacy keys are left in place but are no
// longer read by the active code path.
func (h *Handler) migrateOAuthAppToInstances(ctx context.Context) {
	mods := h.connectors.Modules()
	for _, mod := range mods {
		if mod.OAuth == nil {
			continue
		}
		key := mod.Meta.Key
		legacyClientID := h.configs.GetOwned("connector_oauth:"+key, "client_id")
		legacySecret := h.configs.GetOwned("connector_oauth:"+key, "client_secret")
		if legacyClientID == "" {
			continue
		}
		rows, err := h.connectors.ListByKey(ctx, key)
		if err != nil {
			log.Warn().Err(err).Str("connector", key).Msg("oauth migrate: list rows failed")
			continue
		}
		for _, row := range rows {
			cfgs := h.connectors.LoadConfigs(row)
			if strings.TrimSpace(cfgs["client_id"]) != "" {
				continue // already configured
			}
			updates := map[string]string{"client_id": legacyClientID}
			if legacySecret != "" {
				updates["client_secret"] = legacySecret
			}
			if err := h.connectors.Update(ctx, row.ID, row.Label, updates, row.Disabled); err != nil {
				log.Warn().Err(err).Str("connector", key).Str("row_id", row.ID).
					Msg("oauth migrate: update row failed")
				continue
			}
			log.Info().Str("connector", key).Str("row_id", row.ID).
				Msg("oauth migrate: copied legacy OAuth App credentials to instance row")
		}
	}
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
