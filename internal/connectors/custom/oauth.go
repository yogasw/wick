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
	Expires    time.Time
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
	meta, regEndpoint, err := s.discoverOAuth(ctx, f.URL)
	if err != nil {
		return "", "", err
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
			return "", "", fmt.Errorf("the authorization server offers no dynamic registration — fill in a client ID")
		}
		id, secret, err := s.registerClient(ctx, regEndpoint, redirectURI)
		if err != nil {
			return "", "", err
		}
		meta.ClientID, meta.ClientSecret = id, secret
	}

	return s.newLogin(*f, *meta, redirectURI, instanceID)
}

// StartOAuthLoginForServer begins a browser login against a stored
// server's existing client material — the per-instance "Connect
// account" flow, where no re-discovery or registration is needed.
func (s *Service) StartOAuthLoginForServer(srv *entity.CustomConnectorMCPServer, redirectURI, instanceID string) (authURL, loginID string, err error) {
	meta := parseOAuthMeta(srv.AuthExtra)
	if meta.AuthEndpoint == "" || meta.TokenEndpoint == "" || meta.ClientID == "" {
		return "", "", fmt.Errorf("server has no OAuth client material — edit the server and run Test once")
	}
	return s.newLogin(ServerForm{URL: srv.URL, AuthScheme: "oauth"}, meta, redirectURI, instanceID)
}

// newLogin creates the in-flight session and assembles the PKCE
// authorization URL.
func (s *Service) newLogin(form ServerForm, meta oauthClientMeta, redirectURI, instanceID string) (string, string, error) {
	login := &oauthLogin{
		ID:         randB64(16),
		State:      randB64(24),
		Verifier:   randB64(48),
		Form:       form,
		Meta:       meta,
		InstanceID: instanceID,
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
	res := &OAuthLoginResult{LoginID: login.ID, InstanceID: login.InstanceID}
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
		if strings.HasPrefix(secret, "wick_enc_") && s.keys != nil {
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
		if meta.ClientSecret != "" && !strings.HasPrefix(meta.ClientSecret, "wick_enc_") && s.keys != nil {
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
