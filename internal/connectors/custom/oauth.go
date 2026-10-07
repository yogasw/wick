package custom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

// This file is the OAuth 2.1 client for MCP servers that gate with a
// standard `Authorization: Bearer` token (the MCP authorization spec):
// discovery via RFC 9728 protected-resource metadata + RFC 8414
// authorization-server metadata, dynamic client registration (RFC
// 7591), the PKCE authorization-code flow, and refresh. Tokens are
// per-instance — each connector instance row carries its own account —
// while the client material (client_id/secret, endpoints) lives on the
// server row's AuthExtra column.

// oauthClientMeta is the client-side OAuth material persisted in the
// server row's AuthExtra when auth_scheme=oauth. ClientSecret is
// stored encrypted (wick_enc_).
type oauthClientMeta struct {
	Issuer        string `json:"issuer,omitempty"`
	AuthEndpoint  string `json:"auth_endpoint,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
	// UserinfoEndpoint (OIDC) resolves the connected account's identity
	// for the instance header chip — display only.
	UserinfoEndpoint string `json:"userinfo_endpoint,omitempty"`
	// Resource is the RFC 8707 resource indicator — the canonical MCP
	// URL, sent with every authorization and token request so the AS
	// audience-binds the token to this server (required by the MCP
	// authorization spec; servers may reject tokens minted without it).
	Resource string `json:"resource,omitempty"`
}

// oauthTokens is one account's token set. Persisted per instance row
// as owner-scoped config values (access/refresh encrypted at rest).
type oauthTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string // OIDC id_token when issued — identity display only
	ExpiresAt    time.Time
}

// Instance config keys carrying the per-instance account. Declared on
// the module (hidden) so + New row seeds them and SetOwned accepts the
// writes.
const (
	cfgOAuthAccount = "oauth_account"
	cfgOAuthAccess  = "oauth_access_token"
	cfgOAuthRefresh = "oauth_refresh_token"
	cfgOAuthExpiry  = "oauth_expires_at"
	// cfgOAuthPerUser ("1") marks an instance as per-user (SSO): calls
	// run as each caller's own ConnectorAccount. Only the per-user
	// save/connect path sets it — Enable SSO alone never diverts a legacy
	// instance away from its own stored token.
	cfgOAuthPerUser = "oauth_per_user"
)

// oauthLogin is one in-flight browser login, created by StartOAuthLogin
// and completed by the callback. Nothing here is persisted — abandoning
// the popup just lets the session expire.
type oauthLogin struct {
	ID         string
	State      string
	Verifier   string
	Form       ServerForm
	Meta       oauthClientMeta
	Tokens     *oauthTokens
	Account    string // resolved identity label (userinfo / id_token)
	InstanceID string // non-empty when connecting an account to an existing row
	// Popup marks a login opened in a popup window rather than a full-page
	// navigation. The callback must then close itself and signal the opener;
	// redirecting would strand the popup on a page nobody is looking at.
	// The redirect URI is fixed (it must match what the AS registered), so
	// this rides the session instead of a query parameter.
	Popup bool
	// AccountUserID, when set, makes this a per-user (SSO) connect: the
	// tokens land in a ConnectorAccount owned by this wick user on
	// InstanceID instead of the instance's own config rows.
	// AccountFallbackName labels the account when the AS yields no
	// identity (plain OAuth, no userinfo / id_token).
	AccountUserID       string
	AccountFallbackName string
	Expires             time.Time
}

const oauthLoginTTL = 10 * time.Minute

// oauthLogins is the in-memory in-flight login store on Service.
type oauthLogins struct {
	mu   sync.Mutex
	byID map[string]*oauthLogin
}

func (l *oauthLogins) put(s *oauthLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil {
		l.byID = map[string]*oauthLogin{}
	}
	// Opportunistic sweep — the map only ever holds a handful of rows.
	now := time.Now()
	for id, v := range l.byID {
		if now.After(v.Expires) {
			delete(l.byID, id)
		}
	}
	l.byID[s.ID] = s
}

func (l *oauthLogins) get(id string) (*oauthLogin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.byID[id]
	if !ok || time.Now().After(s.Expires) {
		return nil, false
	}
	return s, true
}

func (l *oauthLogins) byState(state string) (*oauthLogin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.byID {
		if s.State == state && !time.Now().After(s.Expires) {
			return s, true
		}
	}
	return nil, false
}

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ── discovery ────────────────────────────────────────────────────────

// discoverOAuth resolves the authorization-server endpoints for an MCP
// URL: an unauthenticated POST is expected to 401 with a
// WWW-Authenticate resource_metadata pointer (RFC 9728); well-known
// paths on the MCP origin are the fallback. The AS metadata itself is
// read from /.well-known/oauth-authorization-server with an OIDC
// discovery fallback.
func (s *Service) discoverOAuth(ctx context.Context, mcpURL string) (*oauthClientMeta, string, error) {
	resourceMetaURL := s.probeResourceMetadataURL(ctx, mcpURL)

	asURL, resource := "", ""
	if resourceMetaURL != "" {
		var doc struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		if err := s.getJSON(ctx, resourceMetaURL, &doc); err == nil {
			resource = doc.Resource
			if len(doc.AuthorizationServers) > 0 {
				asURL = doc.AuthorizationServers[0]
			}
		}
	}
	if asURL == "" {
		// Last resort: assume the MCP origin is its own AS.
		u, err := url.Parse(mcpURL)
		if err != nil {
			return nil, "", fmt.Errorf("parse MCP URL: %w", err)
		}
		asURL = u.Scheme + "://" + u.Host
	}
	if resource == "" {
		// RFC 8707 canonical fallback when the metadata declares none.
		resource = mcpURL
	}

	meta, regEndpoint, err := s.fetchASMetadata(ctx, asURL)
	if err != nil {
		return nil, "", err
	}
	meta.Resource = resource
	return meta, regEndpoint, nil
}

// probeResourceMetadataURL fires one unauthenticated initialize and
// reads the WWW-Authenticate resource_metadata parameter from the 401.
// Falls back to the RFC 9728 default well-known path on the MCP origin.
func (s *Service) probeResourceMetadataURL(ctx context.Context, mcpURL string) string {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"wick","version":"1.0"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := s.http.Do(req)
	if err != nil {
		return ""
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		for _, h := range resp.Header.Values("WWW-Authenticate") {
			if v := paramFromAuthHeader(h, "resource_metadata"); v != "" {
				return v
			}
		}
	}
	if u, err := url.Parse(mcpURL); err == nil {
		p := strings.TrimSuffix(u.Path, "/")
		return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + p
	}
	return ""
}

// paramFromAuthHeader extracts a quoted auth-param from a
// WWW-Authenticate challenge value.
func paramFromAuthHeader(header, param string) string {
	idx := strings.Index(header, param+"=")
	if idx < 0 {
		return ""
	}
	rest := header[idx+len(param)+1:]
	rest = strings.TrimPrefix(rest, `"`)
	if end := strings.IndexAny(rest, `",`); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func (s *Service) fetchASMetadata(ctx context.Context, asURL string) (*oauthClientMeta, string, error) {
	asURL = strings.TrimSuffix(asURL, "/")
	var doc struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
		UserinfoEndpoint      string `json:"userinfo_endpoint"`
	}
	u, err := url.Parse(asURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse authorization server URL: %w", err)
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimSuffix(u.Path, "/")
	candidates := []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + path + "/.well-known/oauth-authorization-server",
		origin + "/.well-known/openid-configuration" + path,
		origin + path + "/.well-known/openid-configuration",
	}
	var lastErr error
	for _, c := range candidates {
		if err := s.getJSON(ctx, c, &doc); err != nil {
			lastErr = err
			continue
		}
		if doc.AuthorizationEndpoint != "" && doc.TokenEndpoint != "" {
			return &oauthClientMeta{
				Issuer:           doc.Issuer,
				AuthEndpoint:     doc.AuthorizationEndpoint,
				TokenEndpoint:    doc.TokenEndpoint,
				UserinfoEndpoint: doc.UserinfoEndpoint,
			}, doc.RegistrationEndpoint, nil
		}
	}
	return nil, "", fmt.Errorf("authorization server metadata not found for %s: %v", asURL, lastErr)
}

func (s *Service) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// registerClient performs RFC 7591 dynamic client registration.
func (s *Service) registerClient(ctx context.Context, regEndpoint, redirectURI string) (clientID, clientSecret string, err error) {
	payload := map[string]any{
		"client_name":                "wick",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("client registration failed: HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("decode registration response: %w", err)
	}
	if out.ClientID == "" {
		return "", "", fmt.Errorf("registration response carries no client_id")
	}
	return out.ClientID, out.ClientSecret, nil
}

// ── login flow ───────────────────────────────────────────────────────

// StartOAuthLogin discovers the MCP URL's authorization server, makes
// sure a client exists (dynamic registration when the form carries no
// client_id), and returns the browser authorization URL plus the
// in-flight login id. instanceID binds the eventual tokens to an
// existing instance row ("" for the register-form flow, where the
// tokens ride the login session until save).
func (s *Service) StartOAuthLogin(ctx context.Context, f *ServerForm, redirectURI, instanceID string) (authURL, loginID string, err error) {
	meta, err := s.discoverOAuthClient(ctx, f, redirectURI)
	if err != nil {
		return "", "", err
	}

	// The edit-form "Test now" login always runs in a popup.
	return s.newLogin(*f, *meta, redirectURI, instanceID, true)
}

// discoverOAuthClient resolves the full client material for a form:
// RFC 9728 → RFC 8414 discovery, the form's client overrides, and RFC
// 7591 dynamic registration when no client_id was given. No user login
// is involved — the register form's per-user save calls it directly.
func (s *Service) discoverOAuthClient(ctx context.Context, f *ServerForm, redirectURI string) (*oauthClientMeta, error) {
	meta, regEndpoint, err := s.discoverOAuth(ctx, f.URL)
	if err != nil {
		return nil, err
	}
	meta.ClientID = strings.TrimSpace(f.OAuth.ClientID)
	meta.ClientSecret = strings.TrimSpace(f.OAuth.ClientSecret)
	meta.Scopes = strings.TrimSpace(f.OAuth.Scopes)
	if meta.Scopes == "" && meta.UserinfoEndpoint != "" {
		// The AS speaks OIDC — ask for identity so the instance chip
		// can show who connected. Explicit form scopes always win.
		meta.Scopes = "openid email profile"
	}
	if meta.ClientID == "" {
		if regEndpoint == "" {
			return nil, fmt.Errorf("the authorization server offers no dynamic registration — fill in a client ID")
		}
		id, secret, err := s.registerClient(ctx, regEndpoint, redirectURI)
		if err != nil {
			return nil, err
		}
		meta.ClientID, meta.ClientSecret = id, secret
	}
	return meta, nil
}

// StartOAuthLoginForServer begins a browser login against a stored
// server's existing client material — the per-instance "Connect
// account" flow, where no re-discovery or registration is needed.
func (s *Service) StartOAuthLoginForServer(srv *entity.CustomConnectorMCPServer, redirectURI, instanceID string) (authURL, loginID string, err error) {
	return s.startOAuthLoginForServer(srv, redirectURI, instanceID, false)
}

// StartOAuthLoginForServerPopup is StartOAuthLoginForServer for a login
// opened in a popup: the callback closes the window and signals the opener
// instead of redirecting to the instance page.
func (s *Service) StartOAuthLoginForServerPopup(srv *entity.CustomConnectorMCPServer, redirectURI, instanceID string) (authURL, loginID string, err error) {
	return s.startOAuthLoginForServer(srv, redirectURI, instanceID, true)
}

func (s *Service) startOAuthLoginForServer(srv *entity.CustomConnectorMCPServer, redirectURI, instanceID string, popup bool) (authURL, loginID string, err error) {
	meta := parseOAuthMeta(srv.AuthExtra)
	if meta.AuthEndpoint == "" || meta.TokenEndpoint == "" || meta.ClientID == "" {
		return "", "", fmt.Errorf("server has no OAuth client material — edit the server and run Test once")
	}
	return s.newLogin(ServerForm{URL: srv.URL, AuthScheme: "oauth"}, meta, redirectURI, instanceID, popup)
}

// StartOAuthAccountLogin begins the per-user (SSO) connect: the same
// PKCE popup login against the server's stored client material, but the
// callback saves a ConnectorAccount for wickUserID on instanceID instead
// of writing the instance's own token rows. fallbackName labels the
// account when the AS exposes no identity.
func (s *Service) StartOAuthAccountLogin(srv *entity.CustomConnectorMCPServer, redirectURI, instanceID, wickUserID, fallbackName string) (authURL string, err error) {
	meta := parseOAuthMeta(srv.AuthExtra)
	if meta.AuthEndpoint == "" || meta.TokenEndpoint == "" || meta.ClientID == "" {
		return "", fmt.Errorf("server has no OAuth client material — edit the server and save it again")
	}
	if wickUserID == "" {
		return "", fmt.Errorf("connecting an account requires a logged-in user")
	}
	authURL, loginID, err := s.newLogin(ServerForm{URL: srv.URL, AuthScheme: "oauth"}, meta, redirectURI, instanceID, true)
	if err != nil {
		return "", err
	}
	if login, ok := s.logins.get(loginID); ok {
		login.AccountUserID = wickUserID
		login.AccountFallbackName = fallbackName
	}
	return authURL, nil
}

// newLogin creates the in-flight session and assembles the PKCE
// authorization URL.
func (s *Service) newLogin(form ServerForm, meta oauthClientMeta, redirectURI, instanceID string, popup bool) (string, string, error) {
	login := &oauthLogin{
		ID:         randB64(16),
		State:      randB64(24),
		Verifier:   randB64(48),
		Form:       form,
		Meta:       meta,
		InstanceID: instanceID,
		Popup:      popup,
		Expires:    time.Now().Add(oauthLoginTTL),
	}
	s.logins.put(login)

	sum := sha256.Sum256([]byte(login.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {meta.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {login.State},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	if meta.Scopes != "" {
		q.Set("scope", meta.Scopes)
	}
	if meta.Resource != "" {
		q.Set("resource", meta.Resource)
	}
	sep := "?"
	if strings.Contains(meta.AuthEndpoint, "?") {
		sep = "&"
	}
	return meta.AuthEndpoint + sep + q.Encode(), login.ID, nil
}

// OAuthLoginResult is what the callback handler needs to route the
// response: the popup flow posts LoginID back to the form, the
// instance-connect flow redirects to InstanceID's page.
type OAuthLoginResult struct {
	LoginID    string
	InstanceID string
	Key        string // connector key when the login was instance-bound
	// Popup echoes how the login was opened. An instance-bound login from a
	// popup must end by closing the window, not by redirecting.
	Popup bool
	// AsAccount marks a per-user (SSO) connect: the tokens were saved as
	// the caller's ConnectorAccount, labelled Account.
	AsAccount bool
	Account   string
}

// CompleteOAuthLogin exchanges the callback code (PKCE) and stashes the
// tokens on the login session. When the login was bound to an instance
// row, the tokens are persisted onto it immediately.
func (s *Service) CompleteOAuthLogin(ctx context.Context, state, code, redirectURI string) (*OAuthLoginResult, error) {
	login, ok := s.logins.byState(state)
	if !ok {
		return nil, fmt.Errorf("login session expired — run Test again")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {login.Meta.ClientID},
		"code_verifier": {login.Verifier},
	}
	if login.Meta.Resource != "" {
		form.Set("resource", login.Meta.Resource)
	}
	tokens, err := s.tokenRequest(ctx, &login.Meta, form)
	if err != nil {
		return nil, err
	}
	login.Tokens = tokens
	login.Account = s.resolveAccountLabel(ctx, &login.Meta, tokens)
	res := &OAuthLoginResult{LoginID: login.ID, InstanceID: login.InstanceID, Popup: login.Popup}
	if login.InstanceID != "" && login.AccountUserID != "" {
		label := login.Account
		if label == "" {
			label = login.AccountFallbackName
		}
		if err := s.saveAccountTokens(ctx, login.InstanceID, login.AccountUserID, label, tokens); err != nil {
			return nil, err
		}
		res.AsAccount, res.Account = true, label
		if row, err := s.conns.Get(ctx, login.InstanceID); err == nil {
			res.Key = row.Key
		}
		return res, nil
	}
	if login.InstanceID != "" {
		if err := s.persistInstanceTokens(ctx, login.InstanceID, tokens, login.Account); err != nil {
			return nil, err
		}
		if row, err := s.conns.Get(ctx, login.InstanceID); err == nil {
			res.Key = row.Key
		}
	}
	return res, nil
}

// tokenRequest posts to the token endpoint (client secret via basic
// auth when present) and decodes the token response.
func (s *Service) tokenRequest(ctx context.Context, meta *oauthClientMeta, form url.Values) (*oauthTokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if meta.ClientSecret != "" {
		secret := meta.ClientSecret
		if isSecretToken(secret) && s.keys != nil {
			if dec, err := s.keys.DecryptSecret(secret); err == nil {
				secret = dec
			}
		}
		req.SetBasicAuth(meta.ClientID, secret)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token exchange failed: HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token response carries no access_token")
	}
	t := &oauthTokens{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, IDToken: out.IDToken}
	if out.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return t, nil
}

// refreshTokens trades a refresh token for a fresh access token.
func (s *Service) refreshTokens(ctx context.Context, meta *oauthClientMeta, refreshToken string) (*oauthTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {meta.ClientID},
	}
	if meta.Resource != "" {
		form.Set("resource", meta.Resource)
	}
	t, err := s.tokenRequest(ctx, meta, form)
	if err != nil {
		return nil, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refreshToken // servers may omit it on refresh
	}
	return t, nil
}

// OAuthLoginStatus reports an in-flight login's state for the form's
// polling fallback: "done" (tokens landed), "pending" (popup still
// out), or "expired" (unknown/expired session). Polling the server is
// the only reliable completion signal when the authorization server's
// COOP headers sever the popup handle — window.closed lies there.
func (s *Service) OAuthLoginStatus(loginID string) string {
	login, ok := s.logins.get(loginID)
	switch {
	case !ok:
		return "expired"
	case login.Tokens != nil:
		return "done"
	default:
		return "pending"
	}
}

// oauthAuthExtra resolves the AuthExtra payload to persist for an
// oauth-scheme save: the completed login's client material (secret
// encrypted) when a login rode this form session, else the stored
// row's existing material (edits that only touch label/exclusions
// don't force a re-login). A fresh registration without a login can't
// be saved — the save gate requires a successful test, which for oauth
// implies a login.
func (s *Service) oauthAuthExtra(ctx context.Context, f *ServerForm, existingID string) (string, error) {
	if login, ok := s.logins.get(f.OAuthLoginID); ok {
		meta := login.Meta
		if meta.ClientSecret != "" && !isSecretToken(meta.ClientSecret) && s.keys != nil {
			if enc, err := s.keys.EncryptSecret(meta.ClientSecret); err == nil {
				meta.ClientSecret = enc
			}
		}
		return mustJSON(meta), nil
	}
	if existingID != "" {
		existing, err := s.store.GetServer(ctx, existingID)
		if err != nil {
			return "", err
		}
		if m := parseOAuthMeta(existing.AuthExtra); m.TokenEndpoint != "" {
			return existing.AuthExtra, nil
		}
	}
	if f.OAuthPerUser {
		// Per-user (SSO) mode saves without anyone logging in: discovery
		// and dynamic registration run here, server-side.
		meta, err := s.discoverOAuthClient(ctx, f, f.OAuthRedirectURI)
		if err != nil {
			return "", err
		}
		if meta.ClientSecret != "" && !isSecretToken(meta.ClientSecret) && s.keys != nil {
			if enc, err := s.keys.EncryptSecret(meta.ClientSecret); err == nil {
				meta.ClientSecret = enc
			}
		}
		return mustJSON(meta), nil
	}
	return "", fmt.Errorf("sign in on Test connection before saving an oauth server")
}

// parseOAuthMeta tolerates an empty or non-oauth AuthExtra column.
func parseOAuthMeta(raw string) oauthClientMeta {
	var m oauthClientMeta
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// ParseOAuthFormExtra extracts the form-facing client overrides from a
// stored AuthExtra column — the edit form prefill. The secret stays in
// its wick_enc_ shape and round-trips unchanged unless replaced.
func ParseOAuthFormExtra(authExtra string) OAuthFormExtra {
	m := parseOAuthMeta(authExtra)
	return OAuthFormExtra{ClientID: m.ClientID, ClientSecret: m.ClientSecret, Scopes: m.Scopes}
}

// ── per-instance token storage ───────────────────────────────────────

// oauthInstanceConfigs declares the per-instance account fields the
// module carries when its server uses the oauth scheme. Hidden — the
// values are managed by the Connect flow, not typed by hand.
func oauthInstanceConfigs() []DefField {
	return []DefField{
		// Hidden too — the connected-account indicator renders in the
		// instance header next to the Connect button, not as a fake
		// credential row.
		{Key: cfgOAuthAccount, Label: "Connected account", Hidden: true, Desc: "Set by the Connect account flow."},
		{Key: cfgOAuthAccess, Secret: true, Widget: "secret", Hidden: true, Desc: "OAuth access token (managed automatically)."},
		{Key: cfgOAuthRefresh, Secret: true, Widget: "secret", Hidden: true, Desc: "OAuth refresh token (managed automatically)."},
		{Key: cfgOAuthExpiry, Hidden: true, Desc: "Access token expiry (RFC3339, managed automatically)."},
		{Key: cfgOAuthPerUser, Hidden: true, Desc: "Per-user (SSO) mode marker (managed automatically)."},
	}
}

// persistInstanceTokens writes one account's tokens onto an instance
// row's owner-scoped configs (registering the rows first so SetOwned
// accepts them; secret rows encrypt at rest). account is the resolved
// identity label for the header chip — empty (token refreshes) keeps
// whatever label is already stored.
func (s *Service) persistInstanceTokens(ctx context.Context, instanceID string, t *oauthTokens, account string) error {
	owner := "connector:" + instanceID
	if err := s.keys.EnsureOwned(ctx, owner, FieldsToConfigs(oauthInstanceConfigs())...); err != nil {
		return fmt.Errorf("register oauth config rows: %w", err)
	}
	expiry := ""
	if !t.ExpiresAt.IsZero() {
		expiry = t.ExpiresAt.Format(time.RFC3339)
	}
	values := map[string]string{
		cfgOAuthAccess:  t.AccessToken,
		cfgOAuthRefresh: t.RefreshToken,
		cfgOAuthExpiry:  expiry,
	}
	if account != "" {
		values[cfgOAuthAccount] = account
	}
	for k, v := range values {
		if k == cfgOAuthRefresh && v == "" {
			continue
		}
		if err := s.keys.SetOwned(ctx, owner, k, v); err != nil {
			return fmt.Errorf("store %s: %w", k, err)
		}
	}
	return nil
}

// ── per-user (SSO) account storage ───────────────────────────────────

// markPerUser sets the instance's per-user (SSO) marker.
func (s *Service) markPerUser(ctx context.Context, instanceID string) error {
	if s.keys == nil {
		return nil
	}
	owner := "connector:" + instanceID
	if s.keys.GetOwned(owner, cfgOAuthPerUser) == "1" {
		return nil
	}
	if err := s.keys.EnsureOwned(ctx, owner, FieldsToConfigs(oauthInstanceConfigs())...); err != nil {
		return fmt.Errorf("register oauth config rows: %w", err)
	}
	if err := s.keys.SetOwned(ctx, owner, cfgOAuthPerUser, "1"); err != nil {
		return fmt.Errorf("mark per-user instance: %w", err)
	}
	return nil
}

// InstancePerUser reports whether an oauth MCP instance runs in per-user
// (SSO) mode: Enable SSO on AND the per-user marker set. A legacy
// per-instance-token row with Enable SSO flipped on keeps its own token.
func (s *Service) InstancePerUser(row entity.Connector) bool {
	return row.EnableSSO && s.keys != nil && s.keys.GetOwned("connector:"+row.ID, cfgOAuthPerUser) == "1"
}

// saveAccountTokens lands one login's tokens as wickUserID's
// ConnectorAccount on instanceID. The refresh token is encrypted at
// rest; the access token is stored like every other connector account's.
// Connecting marks the instance per-user and forces MultiAccount on first:
// with it off the upsert replaces every other user's account.
func (s *Service) saveAccountTokens(ctx context.Context, instanceID, wickUserID, label string, t *oauthTokens) error {
	if label == "" {
		label = wickUserID
	}
	row, err := s.conns.Get(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := s.markPerUser(ctx, instanceID); err != nil {
		return err
	}
	if !row.MultiAccount {
		if err := s.conns.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{
			AllowOthersConfigure:   row.AllowOthersConfigure,
			AllowOthersConnectSSO:  row.AllowOthersConnectSSO,
			EnableSSO:              row.EnableSSO,
			MultiAccount:           true,
			AllowOthersSeeAccounts: row.AllowOthersSeeAccounts,
		}); err != nil {
			return fmt.Errorf("enable multi-account: %w", err)
		}
	}
	refresh, err := s.encryptRefresh(t.RefreshToken)
	if err != nil {
		return err
	}
	var exp *time.Time
	if !t.ExpiresAt.IsZero() {
		e := t.ExpiresAt
		exp = &e
	}
	if err := s.conns.SaveAccountTokens(ctx, instanceID, wickUserID, label, label, t.AccessToken, refresh, exp); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	return nil
}

func (s *Service) encryptRefresh(v string) (string, error) {
	if v == "" || isSecretToken(v) || s.keys == nil {
		return v, nil
	}
	enc, err := s.keys.EncryptSecret(v)
	if err != nil {
		return "", fmt.Errorf("encrypt refresh token: %w", err)
	}
	return enc, nil
}

// isSecretToken reports whether v is already an encrypted token. The
// configs service encrypts at rest with the wick_cenc_ master prefix, so
// both prefixes must count — gating on wick_enc_ alone sends the raw
// ciphertext to the authorization server.
func isSecretToken(v string) bool {
	return enc.IsToken(v) || enc.IsMasterToken(v)
}

// accountAccessToken returns a live access token for one connected
// account, refreshing through the server's client when expired and
// persisting the fresh set back onto that same account.
func (s *Service) accountAccessToken(ctx context.Context, meta *oauthClientMeta, acc *entity.ConnectorAccount) (string, error) {
	if acc.ExpiresAt == nil || time.Now().Before(acc.ExpiresAt.Add(-30*time.Second)) {
		return acc.AccessToken, nil
	}
	refresh := acc.RefreshToken
	if refresh == "" {
		return acc.AccessToken, nil // expired with no refresh path — let the call 401
	}
	if isSecretToken(refresh) && s.keys != nil {
		dec, err := s.keys.DecryptSecret(refresh)
		if err != nil {
			return "", fmt.Errorf("decrypt refresh token: %w", err)
		}
		refresh = dec
	}
	fresh, err := s.refreshTokens(ctx, meta, refresh)
	if err != nil {
		return "", fmt.Errorf("refresh oauth token for @%s: %w", acc.DisplayName, err)
	}
	storedRefresh := ""
	if fresh.RefreshToken != refresh {
		if storedRefresh, err = s.encryptRefresh(fresh.RefreshToken); err != nil {
			return "", err
		}
	}
	var exp *time.Time
	if !fresh.ExpiresAt.IsZero() {
		e := fresh.ExpiresAt
		exp = &e
	}
	if err := s.conns.UpdateAccountTokens(ctx, acc.ID, fresh.AccessToken, storedRefresh, exp); err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}
	acc.AccessToken, acc.ExpiresAt = fresh.AccessToken, exp
	return fresh.AccessToken, nil
}

// ErrNoOAuthAccount is returned when an SSO-mode MCP instance is called
// by someone who has not connected their own account yet.
var ErrNoOAuthAccount = fmt.Errorf("no connected account")

// ErrOAuthAccountGone is returned when a call names an account (@accountId)
// that is no longer connected to the instance.
var ErrOAuthAccountGone = fmt.Errorf("account not connected to this instance")

// ErrOAuthAccountNotYours is returned when a call names (@accountId)
// another user's account on an instance that keeps accounts private.
var ErrOAuthAccountNotYours = fmt.Errorf("account belongs to another user")

// callerAccount resolves the account an SSO-mode call runs as.
//
// callerUserID is the session owner (connectors.Service.Execute stamps it
// over the MCP principal, which for owner-less spawns is a synthetic
// admin), so ownership is checked here too rather than trusting the
// framework's AccountVisibleTo gate alone: that gate runs against the MCP
// principal and passes every account for an admin one. An explicit
// @accountId may name the caller's own account, an ownerless legacy row,
// or any account when the instance shares them (AllowOthersSeeAccounts).
// Without an explicit account the call runs as the caller's own account
// and never falls back to someone else's.
func (s *Service) callerAccount(ctx context.Context, inst entity.Connector, accountID, callerUserID string) (*entity.ConnectorAccount, error) {
	accs, err := s.conns.ListAccounts(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	if accountID != "" {
		for i := range accs {
			if accs[i].ID != accountID {
				continue
			}
			owner := accs[i].WickUserID
			if owner == "" || inst.AllowOthersSeeAccounts || (callerUserID != "" && owner == callerUserID) {
				return &accs[i], nil
			}
			return nil, ErrOAuthAccountNotYours
		}
		return nil, ErrOAuthAccountGone
	}
	var best *entity.ConnectorAccount
	for i := range accs {
		if callerUserID == "" || accs[i].WickUserID != callerUserID {
			continue
		}
		if best == nil || accs[i].UpdatedAt.After(best.UpdatedAt) {
			best = &accs[i]
		}
	}
	if best == nil {
		return nil, ErrNoOAuthAccount
	}
	return best, nil
}

// syncAccount picks the account whose token authenticates an SSO-mode
// instance's tools/list: the caller's own when connected, else the most
// recently refreshed account on the row. nil when nobody connected yet.
func syncAccount(accs []entity.ConnectorAccount, callerUserID string) *entity.ConnectorAccount {
	var mine, latest *entity.ConnectorAccount
	for i := range accs {
		a := &accs[i]
		if callerUserID != "" && a.WickUserID == callerUserID && (mine == nil || a.UpdatedAt.After(mine.UpdatedAt)) {
			mine = a
		}
		if latest == nil || a.UpdatedAt.After(latest.UpdatedAt) {
			latest = a
		}
	}
	if mine != nil {
		return mine
	}
	return latest
}

// resolveAccountLabel turns a fresh token set into a human identity for
// the header chip: OIDC userinfo when the AS advertises the endpoint,
// the id_token claims as fallback (decoded, not verified — display
// only), and a timestamp when neither yields anything.
func (s *Service) resolveAccountLabel(ctx context.Context, meta *oauthClientMeta, t *oauthTokens) string {
	if meta.UserinfoEndpoint != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.UserinfoEndpoint, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+t.AccessToken)
			req.Header.Set("Accept", "application/json")
			if resp, err := s.http.Do(req); err == nil {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					if label := identityFromClaims(raw); label != "" {
						return label
					}
				}
			}
		}
	}
	if t.IDToken != "" {
		if parts := strings.Split(t.IDToken, "."); len(parts) == 3 {
			if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
				if label := identityFromClaims(payload); label != "" {
					return label
				}
			}
		}
	}
	// No identity available (plain-OAuth server) — leave the label
	// empty; the header chip simply doesn't render.
	return ""
}

// identityFromClaims picks the most human field out of a userinfo /
// id_token claim set.
func identityFromClaims(raw []byte) string {
	var c struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		Sub               string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return ""
	}
	for _, v := range []string{c.Email, c.PreferredUsername, c.Name, c.Sub} {
		if v != "" {
			return v
		}
	}
	return ""
}

// instanceAccessToken returns a live access token for an instance row,
// refreshing through the server's client when expired. Empty when the
// row has no connected account.
func (s *Service) instanceAccessToken(ctx context.Context, meta *oauthClientMeta, instanceID string) (string, error) {
	owner := "connector:" + instanceID
	access := s.keys.GetOwned(owner, cfgOAuthAccess)
	if access == "" {
		return "", nil
	}
	expiry := s.keys.GetOwned(owner, cfgOAuthExpiry)
	if expiry == "" {
		return access, nil
	}
	exp, err := time.Parse(time.RFC3339, expiry)
	if err != nil || time.Now().Before(exp.Add(-30*time.Second)) {
		return access, nil
	}
	refresh := s.keys.GetOwned(owner, cfgOAuthRefresh)
	if refresh == "" {
		return access, nil // expired with no refresh path — let the call 401
	}
	fresh, err := s.refreshTokens(ctx, meta, refresh)
	if err != nil {
		return "", fmt.Errorf("refresh oauth token: %w", err)
	}
	// Refresh keeps the stored account label — "" skips that key.
	if err := s.persistInstanceTokens(ctx, instanceID, fresh, ""); err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}
