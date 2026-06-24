package custom

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/pkg/entity"
)

// SecretCodec is the narrow slice of the configs service the MCP client
// needs: master-key encrypt/decrypt for server-level credentials
// (Bearer tokens, secret header values). These are deployment secrets,
// not per-user ones, so the per-user key derivation of the
// encrypted-fields layer does not apply.
type SecretCodec interface {
	EncryptSecret(plain string) (string, error)
	DecryptSecret(token string) (string, error)
}

// SSOSigner mints the short-lived caller-identity JWT for the sso auth
// scheme and exposes the public key MCP servers validate against.
type SSOSigner interface {
	Mint(claims SSOClaims, ttl time.Duration) (string, error)
	PublicKeyPEM() (string, error)
}

// SSOClaims is the caller identity forwarded as X-Wick-User.
type SSOClaims struct {
	Subject  string   `json:"sub"`
	Email    string   `json:"email,omitempty"`
	Name     string   `json:"name,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	Audience string   `json:"aud"`
	Issuer   string   `json:"iss"`
}

// MCPTool is one entry from a tools/list response. Meta carries the
// MCP _meta block; its category field (when the server sets one) names
// the group the tool belongs to, matched by title against the top-level
// _meta.categories legend.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Meta        *MCPToolMeta   `json:"_meta,omitempty"`
}

// MCPToolMeta is the per-tool _meta block from a tools/list entry.
type MCPToolMeta struct {
	Category string `json:"category,omitempty"`
}

// MCPCategory is one entry in the top-level _meta.categories legend a
// server may attach to its tools/list result. Category titles on tools
// (MCPToolMeta.Category) reference these by Title.
type MCPCategory struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// ProbeResult is what Test connection returns to the UI. NeedsLogin
// flags the oauth scheme's "no token yet" state — the form reacts by
// starting the browser login instead of showing an error.
type ProbeResult struct {
	OK         bool      `json:"ok"`
	Tools      []MCPTool `json:"tools,omitempty"`
	LatencyMs  int       `json:"latency_ms"`
	Error      string    `json:"error,omitempty"`
	NeedsLogin bool      `json:"needs_login,omitempty"`
	// ServerName/Instructions come from the initialize handshake's
	// serverInfo + instructions — the def adopts Instructions as its
	// description while the admin hasn't written one.
	ServerName    string `json:"server_name,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	Instructions  string `json:"instructions,omitempty"`
	// Categories is the server's _meta.categories legend (title +
	// description), when it groups its tools. Empty when the server ships
	// no grouping — every tool then lands in one untitled section.
	Categories []MCPCategory `json:"categories,omitempty"`
}

// mcpClient speaks per-call JSON-RPC over streamable HTTP to a
// registered MCP server. Stateless by design: every Probe/Call does its
// own initialize handshake and carries the returned Mcp-Session-Id (if
// any) for the follow-up request. No connection pool, no background
// goroutine — same forwarder discipline as the httprest connector.
type mcpClient struct {
	http    *http.Client
	secrets SecretCodec
	sso     SSOSigner
	issuer  func() string // wick base URL for the iss claim
}

// maxMCPResponse caps how much of an upstream MCP response wick reads.
const maxMCPResponse = 4 << 20

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	// ID is omitted for notifications (notifications/initialized) —
	// JSON-RPC notifications carry no id and expect no response body.
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// buildHeaders resolves the outbound header set for one call from the
// server row's auth scheme + extra headers. Secret values decrypt here
// and exist only for the lifetime of the request.
func (c *mcpClient) buildHeaders(srv serverConfig, user *SSOClaims) (http.Header, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/event-stream")

	switch srv.AuthScheme {
	case "", "none":
		// nothing
	case "bearer":
		tok, err := c.decrypt(srv.AuthSecret)
		if err != nil {
			return nil, fmt.Errorf("decrypt bearer token: %w", err)
		}
		h.Set("Authorization", "Bearer "+tok)
	case "custom_header":
		for _, row := range srv.AuthHeaders {
			v := row.Value
			if row.Secret {
				dec, err := c.decrypt(v)
				if err != nil {
					return nil, fmt.Errorf("decrypt header %s: %w", row.Key, err)
				}
				v = dec
			}
			h.Set(row.Key, v)
		}
	case "oauth":
		if srv.AccessToken == "" {
			return nil, fmt.Errorf("no OAuth account connected — connect one on the instance page")
		}
		h.Set("Authorization", "Bearer "+srv.AccessToken)
	case "sso":
		if c.sso == nil {
			return nil, fmt.Errorf("sso auth scheme requires the SSO signer")
		}
		if user == nil {
			return nil, fmt.Errorf("sso auth scheme requires a calling user identity")
		}
		claims := *user
		if claims.Audience == "" {
			claims.Audience = srv.SSOAudience
		}
		claims.Issuer = c.issuer()
		ttl := time.Duration(srv.SSOTTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		jwt, err := c.sso.Mint(claims, ttl)
		if err != nil {
			return nil, fmt.Errorf("mint sso token: %w", err)
		}
		h.Set("X-Wick-User", jwt)
	default:
		return nil, fmt.Errorf("unknown auth scheme %q", srv.AuthScheme)
	}

	for _, row := range srv.ExtraHeaders {
		v := row.Value
		if row.Secret {
			dec, err := c.decrypt(v)
			if err != nil {
				return nil, fmt.Errorf("decrypt header %s: %w", row.Key, err)
			}
			v = dec
		}
		h.Set(row.Key, v)
	}
	return h, nil
}

func (c *mcpClient) decrypt(v string) (string, error) {
	// Server-level credentials are master-encrypted, so the stored token
	// carries the wick_cenc_ prefix — not the per-user wick_enc_ one. Match
	// both: checking only wick_enc_ here let master tokens through verbatim,
	// so the Bearer header shipped the ciphertext and the server 401'd.
	if c.secrets == nil || !(enc.IsToken(v) || enc.IsMasterToken(v)) {
		return v, nil
	}
	return c.secrets.DecryptSecret(v)
}

// serverConfig is the resolved runtime view of one
// entity.CustomConnectorMCPServer row. AccessToken is runtime-only:
// the per-instance OAuth token the caller resolved for this call
// (oauth scheme), never read from the row itself.
type serverConfig struct {
	URL           string
	AuthScheme    string
	AuthSecret    string
	AuthHeaders   []HeaderRow
	ExtraHeaders  []HeaderRow
	SSOAudience   string
	SSOTTLSeconds int
	AccessToken   string
}

// resolveServerConfig parses the JSON columns of a server row into the
// runtime shape. The audience default is the MCP URL host.
func resolveServerConfig(srvURL, authScheme, authSecret, authHeaders, authExtra, headers string) (serverConfig, error) {
	cfg := serverConfig{URL: srvURL, AuthScheme: authScheme, AuthSecret: authSecret}
	var err error
	if cfg.AuthHeaders, err = ParseHeaderRows(authHeaders); err != nil {
		return cfg, err
	}
	if cfg.ExtraHeaders, err = ParseHeaderRows(headers); err != nil {
		return cfg, err
	}
	var extra SSOExtra
	if strings.TrimSpace(authExtra) != "" {
		if err := json.Unmarshal([]byte(authExtra), &extra); err != nil {
			return cfg, fmt.Errorf("parse auth_extra: %w", err)
		}
	}
	cfg.SSOAudience = extra.Audience
	cfg.SSOTTLSeconds = extra.TTLSeconds
	if cfg.SSOAudience == "" {
		if u, uerr := url.Parse(srvURL); uerr == nil {
			cfg.SSOAudience = u.Host
		}
	}
	return cfg, nil
}

// rpc fires one JSON-RPC request and decodes the response, tolerating
// both plain-JSON and SSE-framed (streamable HTTP) replies. sessionID
// is forwarded as Mcp-Session-Id when non-empty; the response's session
// header (if any) is returned for the follow-up call.
func (c *mcpClient) rpc(ctx context.Context, srv serverConfig, headers http.Header, sessionID string, req rpcRequest) (json.RawMessage, string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	// Outbound MCP debug trail: every forwarded JSON-RPC logs its URL,
	// method, and (for tools/call) the exact payload sent — when the
	// upstream rejects a call, this shows whether wick mangled the
	// arguments or the server is at fault. Auth header values never
	// appear here.
	l := log.Ctx(ctx).With().
		Str("component", "custom-connector").
		Str("mcp_url", srv.URL).
		Str("rpc", req.Method).
		Logger()
	outBody := ""
	if req.Method == "tools/call" {
		outBody = snippet(body, 2048)
	}
	start := time.Now()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header = headers.Clone()
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		l.Debug().Err(err).Str("request_body", outBody).Dur("latency", time.Since(start)).Msg("mcp outbound request failed")
		return nil, "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPResponse))
	if err != nil {
		return nil, "", err
	}
	newSession := resp.Header.Get("Mcp-Session-Id")
	if newSession == "" {
		newSession = sessionID
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		l.Debug().Int("status", resp.StatusCode).Str("request_body", outBody).Str("response", snippet(raw, 500)).Dur("latency", time.Since(start)).Msg("mcp outbound non-2xx")
		return nil, newSession, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(raw, 200))
	}

	payload := raw
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload = lastSSEData(raw)
		if payload == nil {
			return nil, newSession, fmt.Errorf("empty SSE response")
		}
	}
	var rpcResp rpcResponse
	if err := json.Unmarshal(payload, &rpcResp); err != nil {
		return nil, newSession, fmt.Errorf("decode JSON-RPC response: %w (%s)", err, snippet(payload, 120))
	}
	if rpcResp.Error != nil {
		l.Debug().Int("rpc_error", rpcResp.Error.Code).Str("request_body", outBody).Str("rpc_error_msg", snippet([]byte(rpcResp.Error.Message), 500)).Dur("latency", time.Since(start)).Msg("mcp outbound rpc error")
		return nil, newSession, fmt.Errorf("MCP error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	l.Debug().Int("status", resp.StatusCode).Str("request_body", outBody).Str("response", string(payload)).Dur("latency", time.Since(start)).Msg("mcp outbound ok")
	return rpcResp.Result, newSession, nil
}

// lastSSEData returns the final `data:` payload of an SSE body — the
// JSON-RPC response frame on streamable HTTP servers.
func lastSSEData(raw []byte) []byte {
	var last []byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), maxMCPResponse)
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.HasPrefix(line, []byte("data:")) {
			last = append([]byte(nil), bytes.TrimSpace(line[5:])...)
		}
	}
	return last
}

func snippet(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// serverIdentity is what the initialize handshake reveals about the
// server — name/version from serverInfo plus the optional free-text
// instructions, which the connector adopts as its description.
type serverIdentity struct {
	Name         string
	Version      string
	Instructions string
}

// initialize performs the MCP handshake and returns the session id (if
// the server issued one) for follow-up calls, plus the server's
// self-description.
func (c *mcpClient) initialize(ctx context.Context, srv serverConfig, headers http.Header) (string, serverIdentity, error) {
	res, session, err := c.rpc(ctx, srv, headers, "", rpcRequest{
		JSONRPC: "2.0", ID: 1, Method: "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "wick", "version": "1.0"},
		},
	})
	if err != nil {
		return "", serverIdentity{}, err
	}
	var ident serverIdentity
	if len(res) > 0 {
		var doc struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
			Instructions string `json:"instructions"`
		}
		if jerr := json.Unmarshal(res, &doc); jerr == nil {
			ident = serverIdentity{
				Name:         doc.ServerInfo.Name,
				Version:      doc.ServerInfo.Version,
				Instructions: strings.TrimSpace(doc.Instructions),
			}
		}
	}
	// Spec-compliant servers expect notifications/initialized before
	// further requests; best-effort — some minimal servers reject
	// notifications entirely.
	_, _, _ = c.rpc(ctx, srv, headers, session, rpcRequest{
		JSONRPC: "2.0", ID: 2, Method: "notifications/initialized",
	})
	return session, ident, nil
}

// Probe runs initialize + tools/list — the Test-connection round-trip
// that gates server saves and feeds the import picker (tool catalogs
// are never cached; every picker load re-hits the server).
func (c *mcpClient) Probe(ctx context.Context, srv serverConfig, user *SSOClaims) ProbeResult {
	start := time.Now()
	fail := func(err error) ProbeResult {
		return ProbeResult{OK: false, LatencyMs: int(time.Since(start).Milliseconds()), Error: err.Error()}
	}
	headers, err := c.buildHeaders(srv, user)
	if err != nil {
		return fail(err)
	}
	session, ident, err := c.initialize(ctx, srv, headers)
	if err != nil {
		return fail(fmt.Errorf("initialize: %w", err))
	}
	result, _, err := c.rpc(ctx, srv, headers, session, rpcRequest{
		JSONRPC: "2.0", ID: 3, Method: "tools/list",
	})
	if err != nil {
		return fail(fmt.Errorf("tools/list: %w", err))
	}
	var parsed struct {
		Tools []MCPTool `json:"tools"`
		Meta  struct {
			Categories []MCPCategory `json:"categories"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return fail(fmt.Errorf("decode tools/list: %w", err))
	}
	return ProbeResult{
		OK: true, Tools: parsed.Tools,
		LatencyMs:     int(time.Since(start).Milliseconds()),
		ServerName:    ident.Name,
		ServerVersion: ident.Version,
		Instructions:  ident.Instructions,
		Categories:    parsed.Meta.Categories,
	}
}

// Call proxies one tools/call. The result is the raw MCP content
// payload: text content unwraps to a JSON value when possible so the
// LLM sees clean data instead of an MCP envelope.
func (c *mcpClient) Call(ctx context.Context, srv serverConfig, toolName string, args map[string]any, user *SSOClaims) (any, error) {
	headers, err := c.buildHeaders(srv, user)
	if err != nil {
		return nil, err
	}
	session, _, err := c.initialize(ctx, srv, headers)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	result, _, err := c.rpc(ctx, srv, headers, session, rpcRequest{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{"name": toolName, "arguments": args},
	})
	if err != nil {
		return nil, err
	}
	out, err := unwrapToolResult(result)
	if err != nil {
		// isError envelopes ride a 200/result — surface them in the
		// outbound trail too, next to the request that triggered them.
		log.Ctx(ctx).Debug().
			Str("component", "custom-connector").
			Str("mcp_url", srv.URL).
			Str("tool", toolName).
			Err(err).
			Msg("mcp tool returned error envelope")
	}
	return out, err
}

// unwrapToolResult flattens the MCP tools/call envelope:
// {content: [{type: "text", text: "..."}], isError: bool}. Single text
// blocks parse to JSON when they look like it; multiple blocks return
// as a list.
func unwrapToolResult(raw json.RawMessage) (any, error) {
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Content) == 0 {
		// Not the standard envelope — pass through verbatim.
		var v any
		if jerr := json.Unmarshal(raw, &v); jerr != nil {
			return string(raw), nil
		}
		return v, nil
	}
	texts := make([]any, 0, len(env.Content))
	for _, blk := range env.Content {
		if looksLikeJSON(blk.Text) {
			var v any
			if err := json.Unmarshal([]byte(blk.Text), &v); err == nil {
				texts = append(texts, v)
				continue
			}
		}
		texts = append(texts, blk.Text)
	}
	var out any = texts
	if len(texts) == 1 {
		out = texts[0]
	}
	if env.IsError {
		return nil, fmt.Errorf("MCP tool error: %s", snippet([]byte(fmt.Sprintf("%v", out)), 300))
	}
	return out, nil
}

// ── SSO signer ───────────────────────────────────────────────────────

// ssoKeyOwner/ssoKeyName locate the ED25519 seed in the configs table.
// The seed is generated on first use and encrypted under the master
// key; the derived public key is served at /.well-known/wick-pubkey.pem.
const (
	ssoKeyOwner = "custom_connector"
	ssoKeyName  = "sso_signing_key"
)

// KeyStore is the slice of the configs service the signer needs to
// persist its seed. EnsureOwned registers the row's metadata — SetOwned
// rejects writes to keys the configs service has never seen, so the
// signer must ensure before its first persist.
type KeyStore interface {
	GetOwned(owner, key string) string
	SetOwned(ctx context.Context, owner, key, value string) error
	EnsureOwned(ctx context.Context, owner string, rows ...entity.Config) error
	EncryptSecret(plain string) (string, error)
	DecryptSecret(token string) (string, error)
}

type ssoSigner struct {
	keys KeyStore
}

// NewSSOSigner builds the default signer backed by the configs table.
func NewSSOSigner(keys KeyStore) SSOSigner { return &ssoSigner{keys: keys} }

func (s *ssoSigner) seed() (ed25519.PrivateKey, error) {
	ctx := context.Background()
	// EnsureOwned does double duty, and MUST run before the read:
	// it registers the row's metadata (SetOwned rejects keys the
	// configs service has never seen) and it loads a previously stored
	// seed into the cache — owner-scoped rows are cached on ensure,
	// not at boot, so GetOwned before this returns "" even when a seed
	// already exists in the DB (which would silently rotate the key,
	// breaking every MCP server pinning the published pubkey).
	if err := s.keys.EnsureOwned(ctx, ssoKeyOwner, entity.Config{
		Key:         ssoKeyName,
		IsSecret:    true,
		Description: "ED25519 seed for custom-connector SSO JWTs (X-Wick-User). Auto-generated on first use; rotating it invalidates the pubkey MCP servers pin.",
	}); err != nil {
		return nil, fmt.Errorf("register sso signing key config: %w", err)
	}
	stored := s.keys.GetOwned(ssoKeyOwner, ssoKeyName)
	if stored != "" {
		plain := stored
		if strings.HasPrefix(stored, "wick_enc_") {
			dec, err := s.keys.DecryptSecret(stored)
			if err != nil {
				return nil, fmt.Errorf("decrypt sso signing key: %w", err)
			}
			plain = dec
		}
		raw, err := base64.StdEncoding.DecodeString(plain)
		if err != nil || len(raw) != ed25519.SeedSize {
			return nil, fmt.Errorf("corrupt sso signing key")
		}
		return ed25519.NewKeyFromSeed(raw), nil
	}
	raw := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	val := base64.StdEncoding.EncodeToString(raw)
	// Pre-encrypt to a wick_enc_ token — the secret layer recognizes
	// it and stores it verbatim instead of re-encrypting.
	if enc, err := s.keys.EncryptSecret(val); err == nil {
		val = enc
	}
	if err := s.keys.SetOwned(ctx, ssoKeyOwner, ssoKeyName, val); err != nil {
		return nil, fmt.Errorf("persist sso signing key: %w", err)
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Mint signs an EdDSA JWT with the standard header/claims layout. No
// external JWT dependency — the token is two JSON blobs and a
// signature.
func (s *ssoSigner) Mint(claims SSOClaims, ttl time.Duration) (string, error) {
	priv, err := s.seed()
	if err != nil {
		return "", err
	}
	now := time.Now()
	full := map[string]any{
		"sub": claims.Subject,
		"aud": claims.Audience,
		"iss": claims.Issuer,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	if claims.Email != "" {
		full["email"] = claims.Email
	}
	if claims.Name != "" {
		full["name"] = claims.Name
	}
	if len(claims.Groups) > 0 {
		full["groups"] = claims.Groups
	}
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	payload, err := json.Marshal(full)
	if err != nil {
		return "", err
	}
	signing := b64url(header) + "." + b64url(payload)
	sig := ed25519.Sign(priv, []byte(signing))
	return signing + "." + b64url(sig), nil
}

// PublicKeyPEM exports the verification key in the PKIX PEM shape MCP
// servers fetch from /.well-known/wick-pubkey.pem.
func (s *ssoSigner) PublicKeyPEM() (string, error) {
	priv, err := s.seed()
	if err != nil {
		return "", err
	}
	pub := priv.Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}
