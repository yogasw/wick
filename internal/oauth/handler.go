package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/oauth/view"
)

// appConfig is the subset of configs.Service this handler needs to
// resolve the issuer URL at request time (admin can change app_url
// without restart).
type appConfig interface {
	AppURL() string
}

// Handler exposes the OAuth surface. All endpoints live at the same
// HTTP origin as the rest of wick — issuer == app_url. /authorize
// reads login.GetUser from the request context, so the root-level
// cookie-session middleware must run before this handler.
type Handler struct {
	svc *Service
	cfg appConfig
}

func NewHandler(svc *Service, cfg appConfig) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// Register wires routes. /authorize and the /profile/connections
// pages need the cookie session populated (they call login.GetUser);
// the root mux already wraps everything with Session middleware.
//
// The /profile/connections POST routes pass auth via the supplied
// middleware so an unauthenticated request gets bounced to /auth/login
// instead of silently failing.
func (h *Handler) Register(mux *http.ServeMux, midd *login.Middleware) {
	mux.Handle("GET /.well-known/oauth-protected-resource", http.HandlerFunc(h.protectedResourceMetadata))
	mux.Handle("GET /.well-known/oauth-authorization-server", http.HandlerFunc(h.authorizationServerMetadata))
	mux.Handle("POST /oauth/register", http.HandlerFunc(h.registerClient))
	mux.Handle("GET /oauth/authorize", http.HandlerFunc(h.authorize))
	mux.Handle("POST /oauth/authorize", http.HandlerFunc(h.consent))
	mux.Handle("POST /oauth/token", http.HandlerFunc(h.token))

	// Per-user grant management lives under /profile/* so it sits
	// alongside Access Tokens in the profile tab strip.
	auth := func(next http.HandlerFunc) http.Handler { return midd.RequireAuth(next) }
	mux.Handle("GET /profile/connections", auth(h.connectionsPage))
	mux.Handle("POST /profile/connections/{client_id}/disconnect", auth(h.disconnect))
}

func (h *Handler) connectionsPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	grants, err := h.svc.ListGrants(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to load grants", http.StatusInternalServerError)
		return
	}
	rows := make([]view.GrantRow, len(grants))
	for i, g := range grants {
		rows[i] = view.GrantRow{
			ClientID:   g.ClientID,
			ClientName: g.ClientName,
			GrantedAt:  g.GrantedAt,
			LastUsedAt: g.LastUsedAt,
			TokenCount: g.TokenCount,
		}
	}
	view.ConnectionsPage(view.ConnectionsPageData{
		User:        user,
		Grants:      rows,
		JustRevoked: r.URL.Query().Get("revoked"),
	}).Render(r.Context(), w)
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	clientID := r.PathValue("client_id")
	// Look up the client name BEFORE revoking so we can show it in
	// the success banner; if the lookup fails we still revoke and
	// just skip the personalized message.
	var clientName string
	if c, err := h.svc.LookupClient(r.Context(), clientID); err == nil {
		clientName = c.Name
	}
	if err := h.svc.RevokeGrant(r.Context(), user.ID, clientID); err != nil {
		http.Error(w, "failed to disconnect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/profile/connections"
	if clientName != "" {
		target += "?revoked=" + url.QueryEscape(clientName)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// issuer returns the live app_url, falling back to a synthesized
// origin when AppURL is unset (typical on first boot).
func (h *Handler) issuer(r *http.Request) string {
	base := strings.TrimRight(h.cfg.AppURL(), "/")
	if base != "" {
		h.svc.SetIssuer(base)
		return base
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ── /.well-known/oauth-protected-resource (RFC 9728) ─────────────────

type protectedResourceDoc struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

func (h *Handler) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	iss := h.issuer(r)
	writeJSON(w, http.StatusOK, protectedResourceDoc{
		Resource:               iss + "/mcp",
		AuthorizationServers:   []string{iss},
		BearerMethodsSupported: []string{"header"},
	})
}

// ── /.well-known/oauth-authorization-server (RFC 8414) ───────────────

type authServerDoc struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

func (h *Handler) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	iss := h.issuer(r)
	writeJSON(w, http.StatusOK, authServerDoc{
		Issuer:                            iss,
		AuthorizationEndpoint:             iss + "/oauth/authorize",
		TokenEndpoint:                     iss + "/oauth/token",
		RegistrationEndpoint:              iss + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"}, // public clients only
		ScopesSupported:                   []string{"mcp"},
	})
}

// ── POST /oauth/register (RFC 7591 DCR) ──────────────────────────────

type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (h *Handler) registerClient(w http.ResponseWriter, r *http.Request) {
	var p RegisterClientParams
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	c, err := h.svc.RegisterClient(r.Context(), p)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, registerResponse{
		ClientID:                c.ClientID,
		ClientName:              c.Name,
		RedirectURIs:            decodeRedirectURIs(c.RedirectURIs),
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
}

// ── GET /oauth/authorize (RFC 6749 §4.1.1 + RFC 7636) ────────────────

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	scope := q.Get("scope")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	responseType := q.Get("response_type")

	// Errors that prevent us from safely redirecting (no validated
	// redirect_uri yet) get rendered on our own error page.
	client, err := h.svc.LookupClient(r.Context(), clientID)
	if err != nil {
		view.AuthorizeError("Unknown client.", "The application that sent you here is not registered. Try reconnecting from your MCP client.").Render(r.Context(), w)
		return
	}
	if !ValidateRedirectURI(client, redirectURI) {
		view.AuthorizeError("Redirect URI mismatch.", "The redirect URI does not match the one registered for this application.").Render(r.Context(), w)
		return
	}
	if responseType != "code" {
		redirectAuthError(w, r, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
		return
	}
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		redirectAuthError(w, r, redirectURI, state, "invalid_request", "PKCE S256 is required")
		return
	}

	user := login.GetUser(r.Context())
	if user == nil {
		// Bounce through wick's existing login flow, then come back.
		// One-shot cookie carries the destination so the password
		// and Google callback paths both honor it.
		login.SetAfterLoginRedirect(w, r, "/oauth/authorize?"+r.URL.RawQuery)
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if !user.Approved {
		http.Redirect(w, r, "/auth/pending", http.StatusFound)
		return
	}

	// Render consent. POST goes to /oauth/authorize (POST handler
	// below); on Approve it mints the code + redirects back to the
	// client's redirect_uri.
	view.ConsentPage(view.ConsentData{
		User:                user,
		ClientName:          client.Name,
		Scope:               scope,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		State:               state,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	}).Render(r.Context(), w)
}

// POST /oauth/authorize is the consent submit. Always re-validates
// the parameters since the form is hosted on a same-origin page.
func (h *Handler) consent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user := login.GetUser(r.Context())
	if user == nil || !user.Approved {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	scope := r.FormValue("scope")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	decision := r.FormValue("decision") // "approve" or "deny"

	client, err := h.svc.LookupClient(r.Context(), clientID)
	if err != nil || !ValidateRedirectURI(client, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id / redirect_uri mismatch")
		return
	}
	if decision != "approve" {
		redirectAuthError(w, r, redirectURI, state, "access_denied", "user denied")
		return
	}
	code, err := h.svc.IssueAuthCode(r.Context(), IssueAuthCodeParams{
		ClientID:            clientID,
		UserID:              user.ID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	})
	if err != nil {
		redirectAuthError(w, r, redirectURI, state, "server_error", err.Error())
		return
	}
	redirectAuthSuccess(w, r, redirectURI, state, code)
}

// ── POST /oauth/token (RFC 6749 §3.2 + §6) ───────────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope,omitempty"`
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	switch r.FormValue("grant_type") {
	case "authorization_code":
		h.tokenAuthCode(w, r)
	case "refresh_token":
		h.tokenRefresh(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (h *Handler) tokenAuthCode(w http.ResponseWriter, r *http.Request) {
	pair, err := h.svc.ExchangeAuthCode(
		r.Context(),
		r.FormValue("code"),
		r.FormValue("client_id"),
		r.FormValue("redirect_uri"),
		r.FormValue("code_verifier"),
	)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "")
		return
	}
	writeTokenPair(w, pair)
}

func (h *Handler) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	pair, err := h.svc.ExchangeRefreshToken(
		r.Context(),
		r.FormValue("refresh_token"),
		r.FormValue("client_id"),
	)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "")
		return
	}
	writeTokenPair(w, pair)
}

func writeTokenPair(w http.ResponseWriter, p *TokenPair) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  p.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(p.AccessExpires.Seconds()),
		RefreshToken: p.RefreshToken,
		Scope:        "mcp",
	})
}

// ── helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	body := map[string]string{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	writeJSON(w, status, body)
}

// redirectAuthError builds an OAuth error redirect to the client's
// redirect_uri, preserving state. Per RFC 6749 §4.1.2.1.
func redirectAuthError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	if desc != "" {
		q.Set("error_description", desc)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func redirectAuthSuccess(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

