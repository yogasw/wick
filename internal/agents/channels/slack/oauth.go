// Package slack — oauth.go: Slack user OAuth flow (user-token acquisition).
//
// Purpose: Implements the two-leg OAuth 2.0 flow that lets a Slack user
// grant wick an xoxp token. The token is persisted to a connector row
// so the send proxy can post as the user without repeating the grant.
//
// Routes (mounted by HTTPHandlers):
//
//	GET /integrations/slack/oauth/start    — redirects to Slack consent page
//	GET /integrations/slack/oauth/callback — exchanges code, saves token
//
// Caller:   Channel.HTTPHandlers(), Channel.SetOAuthConfig()
// Dependencies: slack-go, crypto/hmac, crypto/sha256, crypto/rand
// Main Functions:
//   - oauthStartHandler()    — builds state token, redirects to Slack
//   - oauthCallbackHandler() — validates state, exchanges code, calls OnTokenSaved
//
// Side Effects: writes xoxp token to connector row via OnTokenSaved callback.
package slack

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
)

// OAuthConfig holds the Slack app credentials needed for user OAuth flow.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// OnTokenSaved is called after a successful OAuth exchange with the
	// user's Slack ID, display name, and xoxp token. Server.go wires this
	// to upsert the connector row and refresh the token map.
	OnTokenSaved func(ctx context.Context, slackUserID, displayName, xoxpToken string) error
}

// oauthStateEntry stores a pending OAuth state with its expiry.
type oauthStateEntry struct {
	expiresAt time.Time
}

// oauthStartHandler redirects the browser to the Slack OAuth consent page.
// A cryptographically-signed state token is generated, stored with a 10-minute
// TTL, and sent to Slack so the callback can verify the round-trip.
func (s *Channel) oauthStartHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfgMu.Lock()
		cfg := s.oauthCfg
		s.cfgMu.Unlock()

		if cfg.ClientID == "" {
			http.Error(w, "Slack OAuth not configured (missing client_id)", http.StatusServiceUnavailable)
			return
		}

		state, err := s.generateOAuthState()
		if err != nil {
			log.Error().Err(err).Msg("slack oauth: failed to generate state token")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Store state with 10-minute expiry.
		s.oauthPending.Store(state, oauthStateEntry{expiresAt: time.Now().Add(10 * time.Minute)})

		// Build Slack OAuth URL with user scopes.
		params := url.Values{}
		params.Set("client_id", cfg.ClientID)
		params.Set("user_scope", "chat:write,im:write,channels:read,users:read")
		params.Set("redirect_uri", cfg.RedirectURI)
		params.Set("state", state)

		slackURL := "https://slack.com/oauth/v2/authorize?" + params.Encode()
		http.Redirect(w, r, slackURL, http.StatusTemporaryRedirect)
	})
}

// oauthCallbackHandler handles the redirect from Slack after the user approves
// (or denies) the OAuth grant.
//
//  1. Validates the state token against the pending map.
//  2. Exchanges the authorization code for an xoxp token via oauth.v2.access.
//  3. Calls auth.test with the new token to resolve the user's display name.
//  4. Calls OnTokenSaved so the server can upsert the connector row.
//  5. Refreshes the in-memory token map.
//  6. Shows a success page (or error page on failure).
func (s *Channel) oauthCallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// User denied the grant.
		if errParam := q.Get("error"); errParam != "" {
			log.Warn().Str("slack_error", errParam).Msg("slack oauth: user denied grant")
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
		val, ok := s.oauthPending.LoadAndDelete(state)
		if !ok {
			log.Warn().Msg("slack oauth: unknown or already-used state token")
			http.Error(w, "invalid or expired state", http.StatusBadRequest)
			return
		}
		entry := val.(oauthStateEntry)
		if time.Now().After(entry.expiresAt) {
			log.Warn().Msg("slack oauth: state token expired")
			http.Error(w, "state token expired", http.StatusBadRequest)
			return
		}

		s.cfgMu.Lock()
		cfg := s.oauthCfg
		s.cfgMu.Unlock()

		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			http.Error(w, "Slack OAuth not configured", http.StatusServiceUnavailable)
			return
		}

		// Exchange code for token via Slack API.
		resp, err := slackgo.GetOAuthV2ResponseContext(r.Context(), http.DefaultClient, cfg.ClientID, cfg.ClientSecret, code, cfg.RedirectURI)
		if err != nil {
			log.Error().Err(err).Msg("slack oauth: token exchange failed")
			http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
			return
		}

		xoxpToken := resp.AuthedUser.AccessToken
		slackUserID := resp.AuthedUser.ID

		if xoxpToken == "" || slackUserID == "" {
			log.Error().Msg("slack oauth: empty token or user ID in response")
			http.Error(w, "invalid OAuth response from Slack", http.StatusBadGateway)
			return
		}

		// Resolve display name via auth.test.
		userClient := slackgo.New(xoxpToken)
		authResp, err := userClient.AuthTestContext(r.Context())
		displayName := slackUserID // fallback
		if err == nil && authResp != nil {
			if authResp.User != "" {
				displayName = authResp.User
			}
		} else if err != nil {
			log.Warn().Err(err).Str("user_id", slackUserID).Msg("slack oauth: auth.test failed; using user ID as display name")
		}

		// Persist token via callback.
		if cfg.OnTokenSaved != nil {
			if saveErr := cfg.OnTokenSaved(r.Context(), slackUserID, displayName, xoxpToken); saveErr != nil {
				log.Error().Err(saveErr).Str("user_id", slackUserID).Msg("slack oauth: OnTokenSaved failed")
				http.Error(w, "failed to save token: "+saveErr.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Refresh the in-memory token map immediately (not debounced here —
		// the user just completed the flow and expects instant effect).
		s.userTokenMu.Lock()
		if s.userTokenCache == nil {
			s.userTokenCache = make(map[string]string)
		}
		s.userTokenCache[slackUserID] = xoxpToken
		s.userTokenMu.Unlock()

		log.Info().Str("user_id", slackUserID).Str("display_name", displayName).
			Msg("slack oauth: user token saved successfully")

		// Show a simple success page.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Slack Connected</title></head>
<body style="font-family:sans-serif;max-width:480px;margin:60px auto;text-align:center">
  <h2>&#x2713; Slack connected</h2>
  <p>Your Slack account (<strong>@%s</strong>) has been connected to wick.<br>
  You can close this window.</p>
</body>
</html>`, strings.ReplaceAll(displayName, "<", "&lt;"))
	})
}

// generateOAuthState generates a cryptographically-signed state token of the
// form HMAC-SHA256(secret, timestamp+"|"+randomHex). The random component
// ensures uniqueness; the HMAC ensures forgery resistance.
func (s *Channel) generateOAuthState() (string, error) {
	// 16 random bytes → 32-char hex nonce.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	payload := ts + "|" + hex.EncodeToString(nonce)

	mac := hmac.New(sha256.New, s.oauthSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	// Final state = payload + "." + sig  (URL-safe, no base64 needed).
	return payload + "." + sig, nil
}
