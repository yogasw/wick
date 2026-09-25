package agentmemory

// External access: reaching the memory store from OUTSIDE wick, authenticated.
//
// The one rule this file exists to keep is that the daemon stays on loopback.
// Widening its own bind (or its allowed_hosts) would expose every observation
// captured inside every client session to anything that can route to the host,
// with only the backend's own settings between them. So nothing here touches
// the daemon: what is exposed is a WICK route that authenticates the caller,
// then forwards to 127.0.0.1 — the same shape airouter uses for its /v1 API
// proxy (internal/agents/airouter/manager.go: externalAPIAllowed, proxyAPI,
// publishRejected), with one deliberate difference.
//
// The difference is who authenticates. A router's /v1 proxy hands the request
// straight through and lets the ROUTER's own API key decide, because a router
// is a stateless relay to a model API. A memory store is not: it holds client
// work, its bearer token is optional, and it was configured for a daemon
// nobody outside this machine could reach. So wick checks its OWN token here
// and never lets the caller's credential reach the daemon — the caller's
// Authorization header is dropped and replaced with the daemon's (if any).
//
// Default OFF, per backend. A host that did not ask for this answers 403 to
// every external call, and so does one that turned it on but has no token.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// externalTokenPrefix marks a token minted for this route, so one found in a
// script or a log is recognisable. Deliberately NOT "wick_": that prefix means
// "already an encrypted wick token" to the save path, and a plaintext secret
// wearing it would be stored in the clear.
const externalTokenPrefix = "agmem_"

// externalRecentMax bounds the rejection ring. It is a diagnosis aid — "why
// can't my script reach it?" — not an audit log, so it holds the last few and
// forgets the rest.
const externalRecentMax = 20

// Reasons a request was turned away. They are the whole point of recording:
// "403" alone cannot tell an operator whether the switch is off, the token is
// missing, or the script is sending the wrong one.
const (
	reasonDisabled     = "external-access-off"
	reasonNoToken      = "no-token-created"
	reasonNoCredential = "no-bearer-sent"
	reasonBadToken     = "wrong-token"
	reasonPathClosed   = "path-not-exposed"
)

// externalUpstreamPrefixes is what an authenticated caller may reach on the
// daemon, and nothing else resolves. The store's data surfaces are here; the
// backend's own WEB UI is not, and must not be — PLAN §13.2 keeps that page
// off the wick root, and an external route that served it would be that
// decision reversed by accident.
var externalUpstreamPrefixes = []string{"/api/", "/mcp"}

// ExternalRejection is one refused request, as the panel shows it.
type ExternalRejection struct {
	TimeMS   int64  `json:"time_ms"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	ClientIP string `json:"client_ip"`
	Reason   string `json:"reason"`
	Status   int    `json:"status"`
}

// ExternalState is the external-access block of the Settings tab: the switch,
// whether a token exists (never the token itself), the URL to call, and what
// has been refused lately.
type ExternalState struct {
	Enabled  bool `json:"enabled"`
	HasToken bool `json:"has_token"`
	// URL is the base an outside caller uses, e.g.
	// "https://wick.example/agentmemory/ai-memory". Built from the request
	// so it is the address the operator actually reached wick on.
	URL string `json:"url"`
	// Paths are the daemon subtrees this route exposes, so the panel can
	// say what is reachable instead of implying the whole daemon is.
	Paths         []string            `json:"paths"`
	AllowedTotal  int64               `json:"allowed_total"`
	RejectedTotal int64               `json:"rejected_total"`
	Recent        []ExternalRejection `json:"recent"`
}

// externalStats is the visible half of the gate. A refusal nobody can see
// turns "my script gets a 403" into an unanswerable question, which is why
// airouter publishes its rejections to the Requests tab; there is no such
// stream here, so they are kept in memory and read back by the panel.
type externalStats struct {
	mu       sync.Mutex
	allowed  int64
	rejected int64
	recent   []ExternalRejection
}

func (s *externalStats) recordAllowed() {
	s.mu.Lock()
	s.allowed++
	s.mu.Unlock()
}

func (s *externalStats) recordRejected(rej ExternalRejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected++
	s.recent = append(s.recent, rej)
	if len(s.recent) > externalRecentMax {
		s.recent = s.recent[len(s.recent)-externalRecentMax:]
	}
}

// snapshot returns the counters plus a copy of the ring, newest FIRST — the
// panel shows the last refusal at the top.
func (s *externalStats) snapshot() (allowed, rejected int64, recent []ExternalRejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recent = make([]ExternalRejection, 0, len(s.recent))
	for i := len(s.recent) - 1; i >= 0; i-- {
		recent = append(recent, s.recent[i])
	}
	return s.allowed, s.rejected, recent
}

// ── the proxy ────────────────────────────────────────────────────────

// ExternalProxy returns the handler mounted UNAUTHENTICATED at the wick root
// on /agentmemory/<id>/ (server.go). Unauthenticated by wick's session
// middleware, not unauthenticated in fact: the bearer check below is the
// gate, and a caller outside wick has no session cookie to offer.
//
// 404 when the master switch is off or the id is unknown, so a host that does
// not run the feature reveals nothing about it.
func ExternalProxy(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		be, ok := Get(id)
		if !ok || store == nil || !store.Enabled() {
			http.NotFound(w, r)
			return
		}
		be.Mgr.ExternalHandler().ServeHTTP(w, r)
	})
}

// ExternalMountPath is where ExternalProxy(id) is mounted at the wick root.
// One definition, read by the mount, by the host-allowlist exemption and by
// the URL the panel tells the operator to call.
func ExternalMountPath(id string) string { return "/agentmemory/" + id }

// ExternalHandler is the per-backend half: strip the mount prefix, then gate.
func (m *Manager) ExternalHandler() http.Handler {
	return http.StripPrefix(ExternalMountPath(m.desc.ID), http.HandlerFunc(m.serveExternal))
}

func (m *Manager) serveExternal(w http.ResponseWriter, r *http.Request) {
	if !m.externalAPIAllowed() {
		m.rejectExternal(w, r, http.StatusForbidden, reasonDisabled,
			m.desc.DisplayName+" is not reachable from outside wick — an admin turns it on in the Agent Memory panel's Settings tab")
		return
	}
	// A switched-on route with no token refuses everything rather than
	// falling back to "no auth". The daemon's own token is not a substitute:
	// it is optional by design, and an empty one would mean the switch alone
	// opened the store.
	want := m.externalBearer()
	if want == "" {
		m.rejectExternal(w, r, http.StatusForbidden, reasonNoToken,
			"external access is on but no access token has been created — create one in the Agent Memory panel")
		return
	}
	got := bearerFrom(r)
	if got == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="agent-memory"`)
		m.rejectExternal(w, r, http.StatusUnauthorized, reasonNoCredential,
			"missing Authorization: Bearer <token>")
		return
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		m.rejectExternal(w, r, http.StatusUnauthorized, reasonBadToken, "invalid access token")
		return
	}
	// Authenticated, but only onto the surfaces this route exposes. Recorded
	// like the others: a script hitting the web UI's path and getting a 404
	// deserves the same answer as one sending a bad token.
	if !externalPathAllowed(r.URL.Path) {
		m.rejectExternal(w, r, http.StatusNotFound, reasonPathClosed,
			"only "+strings.Join(externalUpstreamPrefixes, ", ")+" are exposed externally")
		return
	}
	if !m.healthy() {
		writeExternalError(w, http.StatusServiceUnavailable, m.desc.DisplayName+" is not running")
		return
	}
	m.proxyExternal(w, r)
}

// proxyExternal forwards to the loopback daemon. The caller's credential is
// wick's to check and the daemon's never to see — it is dropped and replaced
// with the daemon's own token, so a caller cannot probe the backend's auth
// through this route or inherit whatever the daemon happens to accept.
func (m *Manager) proxyExternal(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(m.BaseURL())
	if err != nil {
		writeExternalError(w, http.StatusBadGateway, "the daemon's address is unusable: "+err.Error())
		return
	}
	upstream := m.upstreamBearer()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Authorization")
			if upstream != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+upstream)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			m.log.Warn().Err(err).Msg("agentmemory: external proxy failed")
			writeExternalError(w, http.StatusBadGateway, "the daemon did not answer: "+err.Error())
		},
	}
	m.ext.recordAllowed()
	proxy.ServeHTTP(w, r)
}

// rejectExternal answers AND records. Both halves matter: the caller needs to
// know which of the three gates stopped them, and the operator needs to see
// that anything was stopped at all.
func (m *Manager) rejectExternal(w http.ResponseWriter, r *http.Request, status int, reason, msg string) {
	m.ext.recordRejected(ExternalRejection{
		TimeMS:   time.Now().UnixMilli(),
		Method:   r.Method,
		Path:     r.URL.Path,
		ClientIP: externalClientIP(r),
		Reason:   reason,
		Status:   status,
	})
	m.log.Warn().
		Str("reason", reason).Str("path", r.URL.Path).Str("client_ip", externalClientIP(r)).
		Int("status", status).Msg("agentmemory: external request refused")
	writeExternalError(w, status, msg)
}

// writeExternalError answers in JSON. The callers of this route are scripts,
// and an HTML error page is not something a script can read.
func writeExternalError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// externalPathAllowed reports whether path (already stripped of the mount
// prefix) is one of the exposed daemon subtrees.
func externalPathAllowed(path string) bool {
	for _, p := range externalUpstreamPrefixes {
		if path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// bearerFrom reads the token out of an Authorization header, "" when there is
// none or the scheme is not Bearer.
func bearerFrom(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func externalClientIP(r *http.Request) string {
	if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return fwd
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// ── the wiring the hosting package supplies ──────────────────────────

func (m *Manager) externalAPIAllowed() bool {
	return m.externalAllowed != nil && m.externalAllowed()
}

// externalBearer is the plaintext token wick checks the caller against.
func (m *Manager) externalBearer() string {
	if m.externalToken == nil {
		return ""
	}
	return strings.TrimSpace(m.externalToken())
}

// upstreamBearer is the daemon's OWN token, sent on the forwarded request.
func (m *Manager) upstreamBearer() string {
	if m.daemonToken == nil {
		return ""
	}
	return strings.TrimSpace(m.daemonToken())
}

// SetExternalAllowed wires the getter backing the external-access decision.
// Unwired = closed, like every other gate in this package.
func (m *Manager) SetExternalAllowed(fn func() bool) { m.externalAllowed = fn }

// SetExternalToken wires the getter for the plaintext token a caller must
// present. It is a getter rather than a value because the token is minted and
// revoked while the process runs, and a revocation has to bite immediately.
func (m *Manager) SetExternalToken(fn func() string) { m.externalToken = fn }

// SetDaemonToken wires the getter for the daemon's own bearer token, put on
// the forwarded request in place of the caller's.
func (m *Manager) SetDaemonToken(fn func() string) { m.daemonToken = fn }

// ExternalSnapshot reports what the gate has done, for the panel.
func (m *Manager) ExternalSnapshot() (allowed, rejected int64, recent []ExternalRejection) {
	return m.ext.snapshot()
}

// newExternalToken mints a token. 32 bytes of crypto/rand, base64url — the
// same strength every other wick-issued credential uses, and long enough that
// the constant-time compare above is the only thing standing between a
// guesser and the store.
func newExternalToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return externalTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}
