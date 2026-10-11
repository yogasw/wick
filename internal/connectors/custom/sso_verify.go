package custom

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// ── SSO verification ─────────────────────────────────────────────────
//
// A custom MCP connector with the sso scheme can point at wick itself — a
// tool plugin serving MCP under /tools/{key}/… — and then wick receives
// its own X-Wick-User JWT. These helpers let wick recognise it instead of
// leaving every such call anonymous.

// ssoClockSkew is the slack allowed on exp/iat between minting and
// verifying hosts.
const ssoClockSkew = 30 * time.Second

// maxSSOTTL caps an SSO token's lifetime: Mint clamps to it, a server form
// asking for more is refused, and Verify rejects a longer exp-iat. A
// leaked token — above all the system one, which has no user to revoke —
// is only good for minutes.
const maxSSOTTL = 15 * time.Minute

// Verify checks token is an EdDSA JWT signed with this wick's SSO key,
// unexpired, not issued in the future (both ± ssoClockSkew) and no longer
// than maxSSOTTL. It reads only the cached key (Mint/Warm fill it): an
// unauthenticated request never reaches the configs store. aud, iss and
// wick_url depend on who is verifying and are not checked here.
func (s *ssoSigner) Verify(token string, now time.Time) (SSOClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return SSOClaims{}, errors.New("sso token: not a JWT")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SSOClaims{}, errors.New("sso token: bad header")
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "EdDSA" {
		return SSOClaims{}, errors.New("sso token: alg must be EdDSA")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return SSOClaims{}, errors.New("sso token: bad signature encoding")
	}
	pub := s.pub.Load()
	if pub == nil {
		return SSOClaims{}, errNoSSOKey
	}
	if !ed25519.Verify(*pub, []byte(parts[0]+"."+parts[1]), sig) {
		return SSOClaims{}, errors.New("sso token: bad signature")
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SSOClaims{}, errors.New("sso token: bad payload")
	}
	var p struct {
		SSOClaims
		IssuedAt  int64 `json:"iat"`
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(pb, &p); err != nil {
		return SSOClaims{}, errors.New("sso token: bad payload")
	}
	if p.ExpiresAt == 0 || now.After(time.Unix(p.ExpiresAt, 0).Add(ssoClockSkew)) {
		return SSOClaims{}, errors.New("sso token: expired")
	}
	if p.IssuedAt <= 0 || time.Unix(p.IssuedAt, 0).After(now.Add(ssoClockSkew)) {
		return SSOClaims{}, errors.New("sso token: issued in the future or no iat")
	}
	if time.Duration(p.ExpiresAt-p.IssuedAt)*time.Second > maxSSOTTL+ssoClockSkew {
		return SSOClaims{}, errors.New("sso token: lifetime longer than 15 minutes")
	}
	if p.Subject == "" {
		return SSOClaims{}, errors.New("sso token: no subject")
	}
	return p.SSOClaims, nil
}

// VerifySSOCaller authenticates an X-Wick-User JWT sent to the tool mounted
// at toolBase ("/tools/{key}") on THIS wick:
//
//   - signed by wick's key, at most maxSSOTTL long and unexpired (Verify);
//   - iss equal to what this wick mints with (s.base — the same func
//     mcpClient.issuer reads);
//   - aud one of wick's own hosts (ownAudiences);
//   - wick_url — the URL wick actually sent the token to, set by wick
//     itself — on one of wick's own hosts and equal to toolBase or under
//     toolBase + "/".
//
// aud alone proves nothing: whoever registers a connector may set its
// audience to wick's host while pointing the URL at their own server, and
// wick would hand them a token "for wick". wick_url is the binding: a token
// sent elsewhere always names elsewhere, and a token for /tools/a never
// works on /tools/b. A token without wick_url (minted before it existed)
// is refused; tokens live minutes, so no grace period.
//
// The host set comes from server config, never the request's Host header,
// which the client chooses.
func (s *Service) VerifySSOCaller(token, toolBase string) (SSOClaims, error) {
	claims, err := s.sso.Verify(token, time.Now())
	if err != nil {
		return SSOClaims{}, err
	}
	if strings.TrimRight(claims.Issuer, "/") != strings.TrimRight(strings.TrimSpace(s.base()), "/") {
		return SSOClaims{}, fmt.Errorf("sso token: issuer %q is not this wick", claims.Issuer)
	}
	own := s.ownAudiences()
	if aud := audienceHost(claims.Audience); aud == "" || !own[aud] {
		return SSOClaims{}, fmt.Errorf("sso token: audience %q is not this wick", claims.Audience)
	}
	if claims.WickURL == "" {
		return SSOClaims{}, errors.New("sso token: no wick_url")
	}
	u, err := url.Parse(claims.WickURL)
	if err != nil || !own[audienceHost(u.Host)] {
		return SSOClaims{}, fmt.Errorf("sso token: minted for %q, not this wick", claims.WickURL)
	}
	base := strings.TrimRight(toolBase, "/")
	if p := path.Clean("/" + u.Path); base == "" || (p != base && !strings.HasPrefix(p, base+"/")) {
		return SSOClaims{}, fmt.Errorf("sso token: minted for %q, not tool %q", claims.WickURL, toolBase)
	}
	return claims, nil
}

// validateSSO refuses an sso server form wick would mint unsafe tokens
// for: a TTL above maxSSOTTL, or an audience naming wick on a URL that is
// not wick (defense in depth — wick_url already makes such tokens useless
// against wick, see VerifySSOCaller).
func (s *Service) validateSSO(f *ServerForm) error {
	if f.AuthScheme != "sso" {
		return nil
	}
	if f.SSO.TTLSeconds < 0 || time.Duration(f.SSO.TTLSeconds)*time.Second > maxSSOTTL {
		return fmt.Errorf("sso token lifetime must be 1-%d seconds (0 = default 300)", int(maxSSOTTL/time.Second))
	}
	aud := f.SSO.Audience
	if aud == "" {
		aud = hostOf(f.URL)
	}
	return checkSSOAudience(f.URL, aud, s.ownAudiences())
}

// checkSSOAudience: an audience naming one of wick's own hosts is only
// allowed when the server URL is wick too.
func checkSSOAudience(srvURL, aud string, own map[string]bool) error {
	if own[audienceHost(aud)] && !own[audienceHost(srvURL)] {
		return fmt.Errorf("sso audience %q names this wick but the MCP URL %q is not this wick", aud, srvURL)
	}
	return nil
}

// WarmSSOKey loads an existing SSO seed into the signer's verification
// cache (see ssoSigner.Warm). Called once at boot. A wick that never
// minted has nothing to warm and returns nil; any other error (DB,
// decrypt) is a real failure the caller should surface.
func (s *Service) WarmSSOKey() error {
	if w, ok := s.sso.(interface{ Warm() error }); ok {
		if err := w.Warm(); err != nil && !errors.Is(err, errNoSSOKey) {
			return err
		}
	}
	return nil
}

// SetListenPort records the TCP port wick serves on, so a connector that
// reaches wick over loopback (http://127.0.0.1:<port>/tools/…, which mints
// aud = 127.0.0.1:<port>) is recognised. Called by the server's Run.
func (s *Service) SetListenPort(port int) { s.listenPort.Store(int64(port)) }

// ownAudiences is every aud wick accepts as addressed to itself: the host
// of its base URL plus 127.0.0.1, localhost and [::1] on its own listen
// port. Another loopback port is another process and stays refused.
func (s *Service) ownAudiences() map[string]bool {
	out := map[string]bool{}
	if h := audienceHost(s.base()); h != "" {
		out[h] = true
	}
	if p := s.listenPort.Load(); p > 0 {
		for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
			out[audienceHost(fmt.Sprintf("%s:%d", host, p))] = true
		}
	}
	return out
}

// audienceHost reduces an aud value or base URL to a comparable host:
// lower-case, scheme/path dropped, default ports stripped. The mint side
// defaults aud to the MCP URL's host (with port when non-default), while
// an admin may type a full URL as the audience — both compare equal here.
func audienceHost(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil {
			return ""
		}
		v = u.Host
	} else if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSuffix(v, ":443")
	v = strings.TrimSuffix(v, ":80")
	return v
}

// SSOUserLoader turns a verified subject into the wick user behind it; ok
// is false for an unknown or not-approved user.
type SSOUserLoader func(ctx context.Context, userID string) (tool.User, bool)

// SSOTokenResolver is the tool.HeaderTokenResolver wick installs: it reads
// X-Wick-User, verifies it with verify (Service.VerifySSOCaller) and maps
// the subject — tool.SystemUserID to wick's system identity, anything else
// to the approved user load returns.
func SSOTokenResolver(verify func(token, toolBase string) (SSOClaims, error), load SSOUserLoader) tool.HeaderTokenResolver {
	return func(r *http.Request, toolBase string) (tool.User, bool) {
		tok := strings.TrimSpace(r.Header.Get("X-Wick-User"))
		if tok == "" {
			return tool.User{}, false
		}
		claims, err := verify(tok, toolBase)
		if err != nil {
			return tool.User{}, false
		}
		if claims.Subject == tool.SystemUserID {
			return tool.User{ID: tool.SystemUserID, Name: "wick", Source: tool.TokenSourceSystem}, true
		}
		u, ok := load(r.Context(), claims.Subject)
		if !ok {
			return tool.User{}, false
		}
		u.Source = tool.TokenSourceSSO
		return u, true
	}
}
