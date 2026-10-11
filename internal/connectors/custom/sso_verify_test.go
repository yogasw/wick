package custom

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

const (
	ssoTestBase = "https://wick.example.com"
	ssoTestTool = "/tools/ws"
	ssoTestURL  = "https://wick.example.com/tools/ws/api/mcp"
)

func newSSOService() (*Service, SSOSigner) {
	signer := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}})
	return &Service{sso: signer, base: func() string { return ssoTestBase + "/" }}, signer
}

// mintTo mints the way mcpClient.authHeaders does: iss = the wick base URL,
// wick_url = the URL the token is sent to.
func mintTo(t *testing.T, s SSOSigner, sub, aud, wickURL string, ttl time.Duration) string {
	t.Helper()
	tok, err := s.Mint(SSOClaims{Subject: sub, Audience: aud, Issuer: ssoTestBase, Email: sub + "@example.com", WickURL: wickURL}, ttl)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return tok
}

func mintFor(t *testing.T, s SSOSigner, sub, aud string, ttl time.Duration) string {
	return mintTo(t, s, sub, aud, ssoTestURL, ttl)
}

// signRaw signs arbitrary claims with signer s's key (seeded by a Mint).
func signRaw(t *testing.T, s SSOSigner, claims map[string]any) string {
	t.Helper()
	priv, err := s.(*ssoSigner).seed()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64url(h) + "." + b64url(p)
	return in + "." + b64url(ed25519.Sign(priv, []byte(in)))
}

func TestVerifySSOCallerAcceptsOwnAudience(t *testing.T) {
	svc, signer := newSSOService()
	for _, aud := range []string{"wick.example.com", "WICK.example.com:443", "https://wick.example.com/tools/x/mcp"} {
		c, err := svc.VerifySSOCaller(mintFor(t, signer, "u1", aud, time.Minute), ssoTestTool)
		if err != nil || c.Subject != "u1" || c.Email != "u1@example.com" {
			t.Errorf("aud %q: claims %+v, err %v", aud, c, err)
		}
	}
}

// A JWT wick minted for an EXTERNAL MCP server (aud = that server's host)
// must not authenticate against wick itself.
func TestVerifySSOCallerRejectsReplayFromOtherAudience(t *testing.T) {
	svc, signer := newSSOService()
	for _, aud := range []string{"mcp.evil.example", "wick.example.com.evil.example", "wick.example.com:8443", ""} {
		if _, err := svc.VerifySSOCaller(mintFor(t, signer, "u1", aud, time.Minute), ssoTestTool); err == nil || !strings.Contains(err.Error(), "audience") {
			t.Errorf("aud %q accepted (err %v)", aud, err)
		}
	}
}

// B1: a connector registered with URL evil.example and audience = wick's
// host makes wick mint aud=wick tokens and send them to evil.example. The
// token names where it was sent (wick_url), so the replay is refused.
func TestVerifySSOCallerRejectsMaliciousConnectorWithWickAudience(t *testing.T) {
	svc, signer := newSSOService()
	svc.SetListenPort(9425)
	for _, aud := range []string{"wick.example.com", "127.0.0.1:9425"} {
		for _, sub := range []string{"admin1", tool.SystemUserID} {
			tok := mintTo(t, signer, sub, aud, "https://evil.example/tools/ws/mcp", time.Minute)
			if _, err := svc.VerifySSOCaller(tok, ssoTestTool); err == nil || !strings.Contains(err.Error(), "not this wick") {
				t.Errorf("aud %q sub %q sent to evil.example accepted (err %v)", aud, sub, err)
			}
		}
	}
	// Tokens without wick_url (older mint) are refused outright.
	if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", "wick.example.com", "", time.Minute), ssoTestTool); err == nil || !strings.Contains(err.Error(), "wick_url") {
		t.Errorf("token without wick_url: err %v", err)
	}
}

// B1b: saving (and minting for) an sso server whose audience names wick
// while its URL is elsewhere is refused.
func TestSSOAudienceNamingWickOnForeignURLRefused(t *testing.T) {
	svc, signer := newSSOService()
	svc.SetListenPort(9425)
	bad := []ServerForm{
		{AuthScheme: "sso", URL: "https://evil.example/mcp", SSO: SSOExtra{Audience: "wick.example.com"}},
		{AuthScheme: "sso", URL: "https://evil.example/mcp", SSO: SSOExtra{Audience: "https://wick.example.com"}},
		{AuthScheme: "sso", URL: "http://127.0.0.1:8787/mcp", SSO: SSOExtra{Audience: "127.0.0.1:9425"}},
	}
	for _, f := range bad {
		if err := svc.validateSSO(&f); err == nil {
			t.Errorf("form %+v accepted", f)
		}
	}
	good := []ServerForm{
		{AuthScheme: "sso", URL: "https://mcp.example.org/mcp"},
		{AuthScheme: "sso", URL: "http://127.0.0.1:9425/tools/ws/api/mcp"},
		{AuthScheme: "sso", URL: "https://wick.example.com/tools/ws/api/mcp", SSO: SSOExtra{Audience: "127.0.0.1:9425", TTLSeconds: 900}},
		{AuthScheme: "bearer", URL: "https://evil.example/mcp", SSO: SSOExtra{Audience: "wick.example.com", TTLSeconds: 99999}},
	}
	for _, f := range good {
		if err := svc.validateSSO(&f); err != nil {
			t.Errorf("form %+v refused: %v", f, err)
		}
	}

	// The same check runs at mint (base URL / port may change after save).
	c := &mcpClient{sso: signer, issuer: func() string { return ssoTestBase }, ownHosts: svc.ownAudiences}
	_, err := c.buildHeaders(serverConfig{URL: "https://evil.example/mcp", AuthScheme: "sso", SSOAudience: "wick.example.com"}, &SSOClaims{Subject: "u1"})
	if err == nil || !strings.Contains(err.Error(), "names this wick") {
		t.Fatalf("mint to evil.example with aud wick: err %v", err)
	}
	h, err := c.buildHeaders(serverConfig{URL: ssoTestURL, AuthScheme: "sso", SSOAudience: "wick.example.com", SSOTTLSeconds: 86400}, &SSOClaims{Subject: "u1"})
	if err != nil {
		t.Fatalf("mint to wick: %v", err)
	}
	claims, err := svc.VerifySSOCaller(h.Get("X-Wick-User"), ssoTestTool)
	if err != nil || claims.WickURL != ssoTestURL {
		t.Fatalf("token minted by authHeaders: %+v %v", claims, err)
	}
}

// B2: lifetime is capped at 15 minutes — at save, at mint (clamped) and at
// verify (a token signed with a longer exp-iat is refused).
func TestSSOTTLCapped(t *testing.T) {
	svc, signer := newSSOService()
	for _, ttl := range []int{-1, 901, 315360000} {
		f := ServerForm{AuthScheme: "sso", URL: "https://mcp.example.org/mcp", SSO: SSOExtra{TTLSeconds: ttl}}
		if err := svc.validateSSO(&f); err == nil {
			t.Errorf("ttl_seconds %d accepted", ttl)
		}
	}

	var p struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	tok := mintFor(t, signer, "u1", "wick.example.com", 10*365*24*time.Hour)
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	_ = json.Unmarshal(raw, &p)
	if time.Duration(p.Exp-p.Iat)*time.Second != maxSSOTTL {
		t.Fatalf("Mint did not clamp: exp-iat = %ds", p.Exp-p.Iat)
	}

	now := time.Now()
	long := signRaw(t, signer, map[string]any{"sub": "u1", "aud": "wick.example.com", "iss": ssoTestBase, "wick_url": ssoTestURL,
		"iat": now.Unix(), "exp": now.Add(10 * 365 * 24 * time.Hour).Unix()})
	if _, err := svc.VerifySSOCaller(long, ssoTestTool); err == nil || !strings.Contains(err.Error(), "lifetime") {
		t.Fatalf("10-year token: err %v", err)
	}
	noIat := signRaw(t, signer, map[string]any{"sub": "u1", "aud": "wick.example.com", "iss": ssoTestBase, "wick_url": ssoTestURL,
		"exp": now.Add(time.Minute).Unix()})
	if _, err := svc.VerifySSOCaller(noIat, ssoTestTool); err == nil {
		t.Fatal("token without iat accepted")
	}
}

// S1: a token minted for /tools/a is refused on /tools/b, including via a
// shared prefix or a dot-dot path.
func TestVerifySSOCallerBoundToTool(t *testing.T) {
	svc, signer := newSSOService()
	tok := mintTo(t, signer, "u1", "wick.example.com", "https://wick.example.com/tools/a/api/mcp", time.Minute)
	if _, err := svc.VerifySSOCaller(tok, "/tools/a"); err != nil {
		t.Fatalf("own tool: %v", err)
	}
	// The tool's exact base path, with or without a trailing slash, is the
	// tool too.
	for _, u := range []string{"https://wick.example.com/tools/a", "https://wick.example.com/tools/a/"} {
		if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", "wick.example.com", u, time.Minute), "/tools/a"); err != nil {
			t.Errorf("wick_url %q refused for /tools/a: %v", u, err)
		}
	}
	for _, base := range []string{"/tools/b", "/tools/a/api/mcp/x", "", "/"} {
		if _, err := svc.VerifySSOCaller(tok, base); err == nil {
			t.Errorf("token for /tools/a accepted on %q", base)
		}
	}
	for _, u := range []string{"https://wick.example.com/tools/ab/mcp", "https://wick.example.com/tools/a/../b/mcp", "https://wick.example.com/tools/b", "https://wick.example.com/tools"} {
		if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", "wick.example.com", u, time.Minute), "/tools/a"); err == nil {
			t.Errorf("wick_url %q accepted for /tools/a", u)
		}
	}
}

func TestVerifySSOCallerRejectsBadTokens(t *testing.T) {
	svc, signer := newSSOService()
	good := mintFor(t, signer, "u1", "wick.example.com", time.Minute)
	if _, err := svc.VerifySSOCaller(good, ssoTestTool); err != nil {
		t.Fatalf("good token: %v", err)
	}
	if _, err := svc.VerifySSOCaller(mintFor(t, signer, "u1", "wick.example.com", -time.Minute), ssoTestTool); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired token: err %v", err)
	}
	other := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}})
	if _, err := svc.VerifySSOCaller(mintFor(t, other, "u1", "wick.example.com", time.Minute), ssoTestTool); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("foreign signature: err %v", err)
	}
	parts := strings.Split(good, ".")
	forged, _ := json.Marshal(map[string]any{"sub": "admin", "aud": "wick.example.com", "iss": ssoTestBase, "wick_url": ssoTestURL, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
	if _, err := svc.VerifySSOCaller(parts[0]+"."+base64.RawURLEncoding.EncodeToString(forged)+"."+parts[2], ssoTestTool); err == nil {
		t.Error("tampered payload accepted")
	}
	tok, _ := signer.Mint(SSOClaims{Subject: "u1", Audience: "wick.example.com", Issuer: "https://other.example", WickURL: ssoTestURL}, time.Minute)
	if _, err := svc.VerifySSOCaller(tok, ssoTestTool); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Errorf("foreign issuer: err %v", err)
	}
	for _, junk := range []string{"", "a.b", "x.y.z"} {
		if _, err := svc.VerifySSOCaller(junk, ssoTestTool); err == nil {
			t.Errorf("junk %q accepted", junk)
		}
	}
}

func TestSSOVerifyRejectsFutureIatAndAlgNone(t *testing.T) {
	s := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}}).(*ssoSigner)
	priv, err := s.seed()
	if err != nil {
		t.Fatal(err)
	}
	sign := func(alg string, claims map[string]any) string {
		h, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
		p, _ := json.Marshal(claims)
		in := b64url(h) + "." + b64url(p)
		return in + "." + b64url(ed25519.Sign(priv, []byte(in)))
	}
	now := time.Now()
	future := sign("EdDSA", map[string]any{"sub": "u1", "iat": now.Add(time.Hour).Unix(), "exp": now.Add(2 * time.Hour).Unix()})
	if _, err := s.Verify(future, now); err == nil || !strings.Contains(err.Error(), "future") {
		t.Errorf("future iat: err %v", err)
	}
	if _, err := s.Verify(sign("none", map[string]any{"sub": "u1", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()}), now); err == nil {
		t.Error("alg none accepted")
	}
	ok := sign("EdDSA", map[string]any{"sub": "u1", "iat": now.Add(10 * time.Second).Unix(), "exp": now.Add(-10 * time.Second).Unix()})
	if _, err := s.Verify(ok, now); err != nil {
		t.Errorf("token within skew: %v", err)
	}
}

// recKeys counts every configs-store call.
type recKeys struct {
	*fakeKeyStore
	calls atomic.Int64
}

func (r *recKeys) GetOwned(o, k string) string { r.calls.Add(1); return r.fakeKeyStore.GetOwned(o, k) }
func (r *recKeys) SetOwned(ctx context.Context, o, k, v string) error {
	r.calls.Add(1)
	return r.fakeKeyStore.SetOwned(ctx, o, k, v)
}
func (r *recKeys) EnsureOwned(ctx context.Context, o string, rows ...entity.Config) error {
	r.calls.Add(1)
	return nil
}
func (r *recKeys) DecryptSecret(tok string) (string, error) {
	r.calls.Add(1)
	return r.fakeKeyStore.DecryptSecret(tok)
}

// S2: Verify never reaches the configs store — not on a wick that never
// minted (would create a row), and not on one whose key is cached. The
// token is a well-formed EdDSA JWT so the path really gets to the key.
func TestSSOVerifyNeverTouchesKeyStore(t *testing.T) {
	minter := NewSSOSigner(&fakeKeyStore{vals: map[string]string{}})
	tok := mintFor(t, minter, "u1", "wick.example.com", time.Minute)

	keys := &recKeys{fakeKeyStore: &fakeKeyStore{vals: map[string]string{}}}
	fresh := NewSSOSigner(keys)
	if _, err := fresh.Verify(tok, time.Now()); err == nil {
		t.Fatal("verify with no key succeeded")
	}
	if n := keys.calls.Load(); n != 0 || len(keys.vals) != 0 {
		t.Fatalf("verify on a never-minted wick hit the store %d times, rows %v", n, keys.vals)
	}

	// Warm (boot) loads an existing seed once; verify then stays off the store.
	keys2 := &recKeys{fakeKeyStore: minter.(*ssoSigner).keys.(*fakeKeyStore)}
	warm := NewSSOSigner(keys2).(*ssoSigner)
	if err := warm.Warm(); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	before := keys2.calls.Load()
	for i := 0; i < 3; i++ {
		if _, err := warm.Verify(tok, time.Now()); err != nil {
			t.Fatalf("verify after warm: %v", err)
		}
	}
	if keys2.calls.Load() != before {
		t.Fatal("verify after warm hit the store")
	}

	// Warm on a never-minted wick creates nothing.
	keys3 := &recKeys{fakeKeyStore: &fakeKeyStore{vals: map[string]string{}}}
	if err := NewSSOSigner(keys3).(*ssoSigner).Warm(); err == nil || len(keys3.vals) != 0 {
		t.Fatalf("Warm without a seed: err %v, rows %v", err, keys3.vals)
	}
}

// Connectors registered at wick's loopback URL mint aud = 127.0.0.1:<port>.
// wick's own listen port is accepted; another loopback port is not.
func TestVerifySSOCallerLoopbackAudience(t *testing.T) {
	svc, signer := newSSOService()
	svc.SetListenPort(9425)
	loop := "http://127.0.0.1:9425/tools/ws/api/mcp"
	for _, aud := range []string{"127.0.0.1:9425", "localhost:9425", "[::1]:9425", "LOCALHOST:9425", loop} {
		if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", aud, loop, time.Minute), ssoTestTool); err != nil {
			t.Errorf("own loopback aud %q refused: %v", aud, err)
		}
	}
	for _, aud := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787", "127.0.0.1", "localhost", "10.0.0.5:9425", "mcp.evil.example"} {
		if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", aud, loop, time.Minute), ssoTestTool); err == nil {
			t.Errorf("foreign aud %q accepted", aud)
		}
	}
	// wick_url on another loopback port is another process.
	if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", "127.0.0.1:9425", "http://127.0.0.1:8787/tools/ws/mcp", time.Minute), ssoTestTool); err == nil {
		t.Error("wick_url on another loopback port accepted")
	}
}

func TestVerifySSOCallerLoopbackNeedsListenPort(t *testing.T) {
	svc, signer := newSSOService()
	if _, err := svc.VerifySSOCaller(mintTo(t, signer, "u1", "127.0.0.1:9425", "http://127.0.0.1:9425/tools/ws/mcp", time.Minute), ssoTestTool); err == nil {
		t.Fatal("loopback aud accepted with no listen port recorded")
	}
}

// The issuer is compared with s.base, the same func mcpClient mints iss
// from.
func TestVerifySSOCallerIssuerFollowsBase(t *testing.T) {
	svc, signer := newSSOService()
	svc.base = func() string { return "" }
	if _, err := svc.VerifySSOCaller(mintFor(t, signer, "u1", "wick.example.com", time.Minute), ssoTestTool); err == nil {
		t.Fatal("iss of another base accepted")
	}
	loop := "http://127.0.0.1:9425/tools/ws/mcp"
	tok, _ := signer.Mint(SSOClaims{Subject: "u1", Audience: "127.0.0.1:9425", WickURL: loop}, time.Minute)
	if _, err := svc.VerifySSOCaller(tok, ssoTestTool); err == nil {
		t.Fatal("no base and no port: token accepted")
	}
	svc.SetListenPort(9425)
	if _, err := svc.VerifySSOCaller(tok, ssoTestTool); err != nil {
		t.Fatalf("no base, own loopback, iss as minted: %v", err)
	}
}

func TestSSOTokenResolver(t *testing.T) {
	svc, signer := newSSOService()
	users := map[string]tool.User{"u1": {ID: "u1", Email: "u1@example.com", IsAdmin: true}}
	loads := 0
	load := func(_ context.Context, id string) (tool.User, bool) {
		loads++
		u, ok := users[id]
		return u, ok
	}
	resolve := SSOTokenResolver(svc.VerifySSOCaller, load)
	call := func(tok, base string) (tool.User, bool) {
		r := httptest.NewRequest("POST", base+"/api/mcp", nil)
		if tok != "" {
			r.Header.Set("X-Wick-User", tok)
		}
		return resolve(r, base)
	}

	if u, ok := call(mintFor(t, signer, "u1", "wick.example.com", time.Minute), ssoTestTool); !ok || u.ID != "u1" || u.Source != tool.TokenSourceSSO || !u.IsAdmin {
		t.Fatalf("approved user: %+v %v", u, ok)
	}
	if _, ok := call(mintFor(t, signer, "u1", "wick.example.com", time.Minute), "/tools/other"); ok {
		t.Fatal("token for /tools/ws recognised on /tools/other")
	}
	if _, ok := call(mintFor(t, signer, "u-pending", "wick.example.com", time.Minute), ssoTestTool); ok {
		t.Fatal("unknown/unapproved user recognised")
	}
	if _, ok := call(mintFor(t, signer, "u1", "mcp.evil.example", time.Minute), ssoTestTool); ok {
		t.Fatal("replayed external-audience token recognised")
	}
	before := loads
	u, ok := call(mintFor(t, signer, tool.SystemUserID, "wick.example.com", time.Minute), ssoTestTool)
	if !ok || u.ID != tool.SystemUserID || u.Source != tool.TokenSourceSystem || u.Email != "" || u.IsAdmin {
		t.Fatalf("system identity: %+v %v", u, ok)
	}
	if loads != before {
		t.Fatal("system identity looked up as a user")
	}
	if _, ok := call("", ssoTestTool); ok {
		t.Fatal("no header recognised")
	}
}
