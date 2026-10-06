package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/pkg/upgrade"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// User is the signed-in wick user of a request.
type User struct {
	ID, Email, Name string
	Admin           bool
}

// Service is one loaded service plugin.
type Service struct {
	Key, Version string
	Manifest     wickplugin.ServiceModule
	Sup          *Supervisor
}

// Host owns every service plugin: supervision, the /x/{key}/* proxy with
// per-route auth, access tokens, and the callback token API.
type Host struct {
	Tokens *Tokens
	// SessionUser resolves the wick session of a request (nil = none).
	SessionUser func(*http.Request) *User
	// BaseURL is wick's public base URL handed to plugins as WICK_BASE_URL.
	BaseURL func() string
	// Configs stores each service's manifest config rows (owner
	// ConfigOwner(key)); secrets are encrypted at rest by the store.
	Configs ConfigStore
	// Config overrides the values pushed to the plugin after spawn (tests);
	// nil reads them from Configs.
	Config func(key string) map[string]string
	// Audit records an admin change of a service setting (actor, service
	// key, what changed); nil = log only.
	Audit func(actor, key, detail string)

	sockDir  string
	spawn    spawnFn
	mu       sync.RWMutex
	services map[string]*Service
	cb       map[string]callbackRoute
	inflight atomic.Int64
	once     sync.Once
	started  atomic.Bool
}

var (
	defMu sync.RWMutex
	def   *Host
)

// SetDefault records the server's host for packages that only need to look
// services up (the Team remote_source adapter and wizard).
func SetDefault(h *Host) {
	defMu.Lock()
	def = h
	defMu.Unlock()
}

// Default returns the server's host (nil before the server built it).
func Default() *Host {
	defMu.RLock()
	defer defMu.RUnlock()
	return def
}

// NewHost returns an empty host whose processes put sockets in sockDir.
func NewHost(tokens *Tokens, sockDir string) *Host {
	if sockDir == "" {
		sockDir = connplugin.RunDir()
	}
	return &Host{Tokens: tokens, sockDir: sockDir, spawn: spawnProcess, services: map[string]*Service{}}
}

// Load registers every enabled, verified service plugin under dir.
func (h *Host) Load(dir string, enabled func(string) bool, record func(key, kind, version string) error) int {
	found, err := connplugin.ScanKind(dir, wickplugin.KindService)
	if err != nil {
		log.Warn().Err(err).Msg("service plugins: scan failed")
		return 0
	}
	n := 0
	for _, f := range found {
		if h.loadFound(f, enabled, record) != nil {
			n++
		}
	}
	return n
}

// Install (re)loads service key from dir after a plugin install or update,
// so a new service runs without a wick reload and an update picks up its
// new manifest (routes, configs, scopes). The previous process, if any, is
// stopped first. Once the host has started, the loaded service is started
// too. Returns false when key is missing, disabled or fails verification.
func (h *Host) Install(dir, key string, enabled func(string) bool, record func(key, kind, version string) error) bool {
	found, err := connplugin.ScanKind(dir, wickplugin.KindService)
	if err != nil {
		log.Warn().Err(err).Msg("service plugins: scan failed")
		return false
	}
	for _, f := range found {
		if f.Key != key {
			continue
		}
		if old, ok := h.Get(key); ok {
			old.Sup.Stop()
			h.mu.Lock()
			delete(h.services, key)
			h.mu.Unlock()
		}
		s := h.loadFound(f, enabled, record)
		if s == nil {
			return false
		}
		if h.started.Load() {
			s.Sup.Start()
		}
		return true
	}
	return false
}

// Remove stops service key and forgets it, ahead of an uninstall deleting
// its files. Reports whether it was loaded.
func (h *Host) Remove(key string) bool {
	s, ok := h.Get(key)
	if !ok {
		return false
	}
	s.Sup.Stop()
	h.mu.Lock()
	delete(h.services, key)
	h.mu.Unlock()
	return true
}

// loadFound validates one scanned plugin and registers it (not started).
func (h *Host) loadFound(f connplugin.Found, enabled func(string) bool, record func(key, kind, version string) error) *Service {
	if err := wickplugin.ValidateKey(f.Key); err != nil {
		log.Warn().Str("plugin", f.Key).Err(err).Msg("service plugin: skipped (invalid key)")
		return nil
	}
	if enabled != nil && !enabled(f.Key) {
		return nil
	}
	if f.Manifest.Service == nil {
		log.Warn().Str("plugin", f.Key).Msg("service plugin: skipped (manifest has no service section)")
		return nil
	}
	if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
		log.Warn().Str("plugin", f.Key).Err(err).Msg("service plugin: skipped (verification failed)")
		return nil
	}
	s := h.Add(f.Key, f.Manifest.Version, *f.Manifest.Service, f.BinaryPath)
	if record != nil {
		if err := record(f.Key, wickplugin.KindService, f.Manifest.Version); err != nil {
			log.Warn().Str("plugin", f.Key).Err(err).Msg("service plugin: state record failed")
		}
	}
	return s
}

// Add registers one service (not started).
func (h *Host) Add(key, version string, sm wickplugin.ServiceModule, binary string) *Service {
	sup := newSupervisor(key, binary, h.sockDir, h.spawn)
	sup.env = func() []string { return h.processEnv(key, sm.CallbackScopes) }
	sup.cfg = func() map[string]string { return h.configValues(key) }
	sup.autoOff = h.autoOffFunc(key)
	if h.Configs != nil && len(sm.Configs) > 0 {
		if err := h.Configs.EnsureOwned(context.Background(), ConfigOwner(key), sm.Configs...); err != nil {
			log.Warn().Str("service", key).Err(err).Msg("service plugin: config seed failed")
		}
	}
	s := &Service{Key: key, Version: version, Manifest: sm, Sup: sup}
	h.mu.Lock()
	h.services[key] = s
	h.mu.Unlock()
	return s
}

// processEnv is the callback env of a new process: a fresh scoped token
// (unless revoked) and wick's base URL. The token is never logged.
func (h *Host) processEnv(key string, scopes []string) []string {
	var env []string
	if h.BaseURL != nil {
		env = append(env, wickplugin.EnvBaseURL+"="+h.BaseURL())
	}
	if h.Tokens != nil {
		if tok, ok := h.Tokens.IssueCallback(key, scopes); ok {
			env = append(env, wickplugin.EnvPluginToken+"="+tok)
		}
	}
	return env
}

// Get returns service key.
func (h *Host) Get(key string) (*Service, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.services[key]
	return s, ok
}

// List returns every service, sorted by key.
func (h *Host) List() []*Service {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Service, 0, len(h.services))
	for _, s := range h.services {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Start starts every service (always-on) once.
func (h *Host) Start() {
	h.once.Do(func() {
		upgrade.Register("service plugin requests", func() int { return int(h.inflight.Load()) })
		for _, s := range h.List() {
			s.Sup.Start()
		}
		h.started.Store(true)
	})
}

// StopAll stops every service.
func (h *Host) StopAll() {
	for _, s := range h.List() {
		s.Sup.Stop()
	}
}

// Shutdown stops every service process in parallel (SIGTERM, then SIGKILL
// after StopGrace) and waits for them. Called on wick shutdown and before a
// reload hands over, so the successor spawns its own processes and no plugin
// outlives the wick that started it.
func (h *Host) Shutdown() {
	var wg sync.WaitGroup
	for _, s := range h.List() {
		wg.Add(1)
		go func(s *Service) { defer wg.Done(); s.Sup.Stop() }(s)
	}
	wg.Wait()
}

// ServeHTTP serves /x/{key}/*.
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/x/")
	key, sub, _ := strings.Cut(rest, "/")
	sub = "/" + sub
	if key == callbackKey {
		h.serveCallbackAPI(w, r, sub)
		return
	}
	s, ok := h.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// wick-only RPC paths are never reachable from outside.
	if strings.HasPrefix(sub, wickplugin.RemotePrefix) {
		http.NotFound(w, r)
		return
	}
	route, ok := s.Manifest.MatchRoute(sub)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var user *User
	switch route.Auth {
	case wickplugin.AuthPublic:
	case wickplugin.AuthToken:
		if h.Tokens == nil || !h.Tokens.Verify(key, bearer(r)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="wick"`)
			http.Error(w, "invalid or missing token", http.StatusUnauthorized)
			return
		}
		r.Header.Del("Authorization") // the plugin never sees wick tokens
	case wickplugin.AuthSession:
		if h.SessionUser != nil {
			user = h.SessionUser(r)
		}
		if user == nil {
			http.Error(w, "sign in to wick first", http.StatusUnauthorized)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	h.proxy(w, r, s, sub, user)
}

func bearer(r *http.Request) string {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// proxy forwards r (path sub) to s, flushing every write (SSE).
func (h *Host) proxy(w http.ResponseWriter, r *http.Request, s *Service, sub string, u *User) {
	rt, err := s.Sup.Transport()
	if err != nil {
		http.Error(w, fmt.Sprintf("Service %q is not running (%s).", s.Key, s.Sup.Status().State), http.StatusServiceUnavailable)
		return
	}
	h.inflight.Add(1)
	defer h.inflight.Add(-1)
	base := "/x/" + s.Key
	rp := &httputil.ReverseProxy{
		Transport:     rt,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "plugin", "plugin"
			pr.Out.URL.Path, pr.Out.URL.RawPath = sub, ""
			injectHeaders(pr.Out.Header, base, u)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Warn().Str("service", s.Key).Err(err).Msg("service plugin: proxy error")
			http.Error(w, fmt.Sprintf("Service %q did not answer.", s.Key), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// injectHeaders drops client X-Wick-* headers and sets the trusted ones.
func injectHeaders(hd http.Header, base string, u *User) {
	for k := range hd {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), wickplugin.HeaderPrefix) {
			delete(hd, k)
		}
	}
	hd.Set(wickplugin.HeaderBase, base)
	if u == nil {
		return
	}
	role := "user"
	if u.Admin {
		role = "admin"
	}
	hd.Set(wickplugin.HeaderUserID, u.ID)
	hd.Set(wickplugin.HeaderUserEmail, u.Email)
	hd.Set(wickplugin.HeaderUserName, u.Name)
	hd.Set(wickplugin.HeaderUserRole, role)
}
