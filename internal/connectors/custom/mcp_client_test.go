package custom

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	"github.com/yogasw/wick/pkg/entity"
	"sync"
	"testing"
	"time"
)

// ── fake JSON-RPC MCP server ─────────────────────────────────────────

type rpcCall struct {
	method string
	header http.Header
	params json.RawMessage
}

type fakeMCP struct {
	mu         sync.Mutex
	calls      []rpcCall
	sse        bool // frame every response as SSE
	status     int  // non-zero → reply with this HTTP status
	tools      []map[string]any
	callResult map[string]any // result payload for tools/call
}

func (f *fakeMCP) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.calls = append(f.calls, rpcCall{method: req.Method, header: r.Header.Clone(), params: req.Params})
		f.mu.Unlock()

		if f.status != 0 {
			http.Error(w, "denied", f.status)
			return
		}

		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-123")
			result = map[string]any{"protocolVersion": "2025-03-26"}
		case "tools/list":
			result = map[string]any{"tools": f.tools}
		case "tools/call":
			result = f.callResult
		}
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		if f.sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}
}

func (f *fakeMCP) callFor(method string) *rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.calls {
		if f.calls[i].method == method {
			return &f.calls[i]
		}
	}
	return nil
}

func newMCPClient(srv *httptest.Server, secrets SecretCodec, sso SSOSigner) *mcpClient {
	return &mcpClient{
		http:    srv.Client(),
		secrets: secrets,
		sso:     sso,
		issuer:  func() string { return "https://wick.example.com" },
	}
}

// fakeCodec is a transparent SecretCodec: wick_enc_<plain>.
type fakeCodec struct{}

func (fakeCodec) EncryptSecret(plain string) (string, error) { return "wick_enc_" + plain, nil }
func (fakeCodec) DecryptSecret(token string) (string, error) {
	return strings.TrimPrefix(token, "wick_enc_"), nil
}

// ── Probe / Call ─────────────────────────────────────────────────────

func TestProbePlainJSON(t *testing.T) {
	f := &fakeMCP{tools: []map[string]any{
		{"name": "get_pet", "description": "Get a pet", "inputSchema": map[string]any{"type": "object"}},
		{"name": "list_pets", "description": "List pets"},
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res := newMCPClient(srv, nil, nil).Probe(context.Background(), serverConfig{URL: srv.URL, AuthScheme: "none"}, nil)
	if !res.OK {
		t.Fatalf("Probe failed: %s", res.Error)
	}
	if len(res.Tools) != 2 || res.Tools[0].Name != "get_pet" {
		t.Errorf("tools = %+v", res.Tools)
	}
	if res.LatencyMs < 0 {
		t.Errorf("latency = %d", res.LatencyMs)
	}
	// Session from initialize must be forwarded to tools/list.
	tl := f.callFor("tools/list")
	if tl == nil {
		t.Fatal("tools/list never reached the server")
	}
	if got := tl.header.Get("Mcp-Session-Id"); got != "sess-123" {
		t.Errorf("Mcp-Session-Id = %q, want sess-123", got)
	}
}

func TestProbeSSEFramed(t *testing.T) {
	f := &fakeMCP{
		sse:   true,
		tools: []map[string]any{{"name": "echo", "description": "Echo back"}},
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res := newMCPClient(srv, nil, nil).Probe(context.Background(), serverConfig{URL: srv.URL, AuthScheme: "none"}, nil)
	if !res.OK {
		t.Fatalf("SSE Probe failed: %s", res.Error)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "echo" {
		t.Errorf("tools = %+v", res.Tools)
	}
}

func TestCallUnwrapsTextJSONEnvelope(t *testing.T) {
	f := &fakeMCP{callResult: map[string]any{
		"content": []map[string]any{{"type": "text", "text": `{"a":1}`}},
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	out, err := newMCPClient(srv, nil, nil).Call(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "none"}, "get_pet", map[string]any{"id": "1"}, nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !reflect.DeepEqual(out, map[string]any{"a": float64(1)}) {
		t.Errorf("out = %#v, want unwrapped JSON map", out)
	}

	// Arguments must arrive inside params.
	tc := f.callFor("tools/call")
	if tc == nil {
		t.Fatal("tools/call never reached the server")
	}
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(tc.params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "get_pet" || params.Arguments["id"] != "1" {
		t.Errorf("params = %+v", params)
	}
}

func TestCallPlainTextBlock(t *testing.T) {
	f := &fakeMCP{callResult: map[string]any{
		"content": []map[string]any{{"type": "text", "text": "hello world"}},
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	out, err := newMCPClient(srv, nil, nil).Call(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "none"}, "t", nil, nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "hello world" {
		t.Errorf("out = %#v", out)
	}
}

func TestCallIsErrorEnvelope(t *testing.T) {
	f := &fakeMCP{callResult: map[string]any{
		"content": []map[string]any{{"type": "text", "text": "kaboom"}},
		"isError": true,
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	_, err := newMCPClient(srv, nil, nil).Call(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "none"}, "t", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "MCP tool error") || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v, want MCP tool error with payload", err)
	}
}

// ── auth schemes ─────────────────────────────────────────────────────

func TestBearerAuthPlaintextPassthrough(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	// No wick_enc_ prefix → value passes through even with a codec set.
	res := newMCPClient(srv, fakeCodec{}, nil).Probe(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "bearer", AuthSecret: "plain-token"}, nil)
	if !res.OK {
		t.Fatalf("Probe failed: %s", res.Error)
	}
	init := f.callFor("initialize")
	if got := init.header.Get("Authorization"); got != "Bearer plain-token" {
		t.Errorf("Authorization = %q, want plaintext passthrough", got)
	}
}

func TestBearerAuthDecryptsToken(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res := newMCPClient(srv, fakeCodec{}, nil).Probe(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "bearer", AuthSecret: "wick_enc_real-token"}, nil)
	if !res.OK {
		t.Fatalf("Probe failed: %s", res.Error)
	}
	init := f.callFor("initialize")
	if got := init.header.Get("Authorization"); got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want decrypted token", got)
	}
}

func TestCustomHeaderAndExtraHeaders(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	cfg := serverConfig{
		URL:        srv.URL,
		AuthScheme: "custom_header",
		AuthHeaders: []HeaderRow{
			{Key: "X-Api-Key", Value: "wick_enc_k1", Secret: true},
			{Key: "X-Plain", Value: "p1"},
		},
		ExtraHeaders: []HeaderRow{{Key: "X-Extra", Value: "e1"}},
	}
	res := newMCPClient(srv, fakeCodec{}, nil).Probe(context.Background(), cfg, nil)
	if !res.OK {
		t.Fatalf("Probe failed: %s", res.Error)
	}
	h := f.callFor("initialize").header
	if got := h.Get("X-Api-Key"); got != "k1" {
		t.Errorf("X-Api-Key = %q, want decrypted", got)
	}
	if got := h.Get("X-Plain"); got != "p1" {
		t.Errorf("X-Plain = %q", got)
	}
	if got := h.Get("X-Extra"); got != "e1" {
		t.Errorf("X-Extra = %q", got)
	}
}

func TestProbeHTTPErrorStatus(t *testing.T) {
	f := &fakeMCP{status: http.StatusUnauthorized}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res := newMCPClient(srv, nil, nil).Probe(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "none"}, nil)
	if res.OK {
		t.Fatal("Probe must fail on HTTP 401")
	}
	if !strings.Contains(res.Error, "HTTP 401") {
		t.Errorf("error = %q, want HTTP 401 status", res.Error)
	}
}

func TestUnknownAuthScheme(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res := newMCPClient(srv, nil, nil).Probe(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "magic"}, nil)
	if res.OK || !strings.Contains(res.Error, "unknown auth scheme") {
		t.Fatalf("res = %+v, want unknown auth scheme failure", res)
	}
}

// ── SSO ──────────────────────────────────────────────────────────────

// fakeKeyStore is an in-memory KeyStore for the SSO signer.
type fakeKeyStore struct {
	mu   sync.Mutex
	vals map[string]string
}

func (f *fakeKeyStore) GetOwned(owner, key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vals[owner+"/"+key]
}

func (f *fakeKeyStore) SetOwned(ctx context.Context, owner, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[owner+"/"+key] = value
	return nil
}

func (f *fakeKeyStore) EnsureOwned(ctx context.Context, owner string, rows ...entity.Config) error {
	return nil
}

func (f *fakeKeyStore) EncryptSecret(plain string) (string, error) { return "wick_enc_" + plain, nil }
func (f *fakeKeyStore) DecryptSecret(token string) (string, error) {
	return strings.TrimPrefix(token, "wick_enc_"), nil
}

func TestSSOSchemeMintsVerifiableJWT(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	signer := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}})
	c := newMCPClient(srv, nil, signer)

	claims := &SSOClaims{Subject: "user-1", Email: "u@example.com", Name: "User One", Groups: []string{"g1"}}
	cfg := serverConfig{URL: srv.URL, AuthScheme: "sso", SSOAudience: "mcp.example.com", SSOTTLSeconds: 120}
	res := c.Probe(context.Background(), cfg, claims)
	if !res.OK {
		t.Fatalf("Probe failed: %s", res.Error)
	}

	jwt := f.callFor("initialize").header.Get("X-Wick-User")
	if jwt == "" {
		t.Fatal("X-Wick-User header missing")
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments, want 3", len(parts))
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if hdr.Alg != "EdDSA" || hdr.Typ != "JWT" {
		t.Errorf("header = %+v, want EdDSA/JWT", hdr)
	}

	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var pl struct {
		Sub    string   `json:"sub"`
		Aud    string   `json:"aud"`
		Iss    string   `json:"iss"`
		Iat    int64    `json:"iat"`
		Exp    int64    `json:"exp"`
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(payloadRaw, &pl); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if pl.Sub != "user-1" {
		t.Errorf("sub = %q", pl.Sub)
	}
	if pl.Aud != "mcp.example.com" {
		t.Errorf("aud = %q", pl.Aud)
	}
	if pl.Iss != "https://wick.example.com" {
		t.Errorf("iss = %q", pl.Iss)
	}
	if pl.Exp-pl.Iat != 120 {
		t.Errorf("exp-iat = %d, want configured TTL 120", pl.Exp-pl.Iat)
	}
	now := time.Now().Unix()
	if pl.Iat < now-60 || pl.Iat > now+60 {
		t.Errorf("iat = %d, not near now (%d)", pl.Iat, now)
	}
	if pl.Email != "u@example.com" || pl.Name != "User One" || !reflect.DeepEqual(pl.Groups, []string{"g1"}) {
		t.Errorf("identity claims = %+v", pl)
	}

	// Signature verifies against the exported public key.
	pemStr, err := signer.PublicKeyPEM()
	if err != nil {
		t.Fatalf("PublicKeyPEM: %v", err)
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil || block.Type != "PUBLIC KEY" {
		t.Fatalf("bad PEM: %q", pemStr)
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKIX: %v", err)
	}
	pub, ok := pubAny.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("public key is %T, want ed25519.PublicKey", pubAny)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Error("signature does not verify against PublicKeyPEM")
	}
}

func TestSSOSchemeRequiresUser(t *testing.T) {
	f := &fakeMCP{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	signer := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}})
	res := newMCPClient(srv, nil, signer).Probe(context.Background(),
		serverConfig{URL: srv.URL, AuthScheme: "sso"}, nil)
	if res.OK || !strings.Contains(res.Error, "calling user identity") {
		t.Fatalf("res = %+v, want missing-identity failure", res)
	}
}

// ── server config resolution ─────────────────────────────────────────

func TestResolveServerConfig(t *testing.T) {
	cfg, err := resolveServerConfig("https://mcp.example.com/rpc", "sso", "", "", "", "")
	if err != nil {
		t.Fatalf("resolveServerConfig: %v", err)
	}
	if cfg.SSOAudience != "mcp.example.com" {
		t.Errorf("default audience = %q, want URL host", cfg.SSOAudience)
	}

	cfg, err = resolveServerConfig("https://mcp.example.com", "sso", "",
		`[{"key":"X-A","value":"1"}]`, `{"audience":"aud1","ttl_seconds":60}`, `[{"key":"X-B","value":"2","secret":true}]`)
	if err != nil {
		t.Fatalf("resolveServerConfig: %v", err)
	}
	if cfg.SSOAudience != "aud1" || cfg.SSOTTLSeconds != 60 {
		t.Errorf("sso extra = %q/%d", cfg.SSOAudience, cfg.SSOTTLSeconds)
	}
	if len(cfg.AuthHeaders) != 1 || cfg.AuthHeaders[0].Key != "X-A" {
		t.Errorf("auth headers = %+v", cfg.AuthHeaders)
	}
	if len(cfg.ExtraHeaders) != 1 || !cfg.ExtraHeaders[0].Secret {
		t.Errorf("extra headers = %+v", cfg.ExtraHeaders)
	}

	if _, err := resolveServerConfig("https://x", "sso", "", "", "{broken", ""); err == nil {
		t.Error("expected error for invalid auth_extra JSON")
	}
	if _, err := resolveServerConfig("https://x", "none", "", "[broken", "", ""); err == nil {
		t.Error("expected error for invalid auth_headers JSON")
	}
}
