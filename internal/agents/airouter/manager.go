package airouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

// verTTL is how long a resolved package version is trusted before /status
// triggers a background re-check.
const verTTL = time.Hour

// Manager owns the lifecycle of one router's child process and the reverse
// proxy fronting its dashboard. Construct via newManager (done by Register).
type Manager struct {
	desc   Descriptor
	prefix string // "/airouter/<id>" — the wick-root mount

	mu       sync.Mutex
	cmd      *exec.Cmd
	starting bool         // true between spawn and dashboard-ready
	port     atomic.Int32 // bound loopback port (0 until first start)

	proxy    *httputil.ReverseProxy // dashboard proxy (rewrites HTML/JS/CSS bodies)
	apiProxy *httputil.ReverseProxy // /v1 pass-through (byte-perfect JSON/SSE)
	upgrader websocket.Upgrader
	log      zerolog.Logger
	logs     *logBuffer
	bcast    *broadcaster

	// externalAllowed reports whether the /airouter/<id>/v1 API may be reached
	// from off-machine. Injected by the hosting package; nil → false (safe).
	externalAllowed func() bool

	verMu        sync.Mutex
	verCached    string
	verCheckedAt time.Time
	verResolving bool

	// identity-probe cache: confirms an externally-started process on the port
	// is really THIS router (via /manifest.webmanifest). TTL-cached so /status
	// polls don't hit the manifest every time.
	identMu sync.Mutex
	identOK bool
	identAt time.Time
}

// identTTL bounds how long a resolved identity-probe result is trusted.
const identTTL = 30 * time.Second

// forwardClientHeader is an internal per-request sentinel the API handler
// stamps with the real client IP when external access is enabled. The
// apiProxy Rewrite reads it, re-emits it as a trusted X-Forwarded-For, and
// strips it before the request goes upstream so it never leaves wick.
const forwardClientHeader = "X-Wick-Air-Client"

// newManager builds the Manager for a descriptor. The proxies target the
// current bound port dynamically (routers get remapped ports when their
// preferred port is taken), so the target is resolved per request.
func newManager(d Descriptor) *Manager {
	logger := log.With().Str("component", "airouter").Str("router", d.ID).Logger()
	m := &Manager{
		desc:     d,
		prefix:   "/airouter/" + d.ID,
		log:      logger,
		logs:     newLogBuffer(),
		bcast:    newBroadcaster(),
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	rw := rewriter{prefix: m.prefix, id: d.ID, prefixes: rewritePrefixesFor(d.RoutePrefixes)}

	m.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := m.backendURL()
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Ask the backend for plaintext so ModifyResponse can rewrite
			// HTML/JS bodies without gunzip+regzip on every chunk.
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: rw.rewriteResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error().Err(err).Str("path", r.URL.Path).Msg("airouter: proxy error")
			http.Error(w, d.DisplayName+" backend unavailable", http.StatusBadGateway)
		},
	}

	m.apiProxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			target := m.backendURL()
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Drop client-supplied forwarding hints — they are spoofable and
			// the router trusts XFF when the TCP peer is loopback (wick always
			// is). The handler decides per request whether to re-add a trusted
			// client address via forwardClientHeader.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Real-Ip")
			pr.Out.Header.Del("Forwarded")
			if ip := pr.Out.Header.Get(forwardClientHeader); ip != "" {
				pr.Out.Header.Del(forwardClientHeader)
				pr.Out.Header.Set("X-Forwarded-For", ip)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error().Err(err).Str("path", r.URL.Path).Msg("airouter: api proxy error")
			http.Error(w, d.DisplayName+" backend unavailable", http.StatusBadGateway)
		},
	}

	logger.Info().Int("pref_port", d.PrefPort).Str("prefix", m.prefix).Msg("airouter: manager configured")
	return m
}

// MountPrefix is the wick-root path this router's dashboard is proxied under.
func (m *Manager) MountPrefix() string { return m.prefix }

// backendURL resolves the loopback target for the current bound port,
// falling back to the preferred port before the first start.
func (m *Manager) backendURL() *url.URL {
	p := int(m.port.Load())
	if p == 0 {
		p = m.desc.PrefPort
	}
	u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", p))
	return u
}

func (m *Manager) boundPort() int {
	if p := int(m.port.Load()); p != 0 {
		return p
	}
	return m.desc.PrefPort
}

// ── npm package lifecycle ────────────────────────────────────────────

func (m *Manager) installedVersion(ctx context.Context) string {
	bin, err := safeexec.ResolveBin("npm")
	if err != nil {
		m.log.Warn().Err(err).Msg("airouter: npm not found on PATH")
		return ""
	}
	pkg := m.desc.NpmPackage
	out, _ := safeexec.CommandContext(ctx, bin, "ls", "-g", "--depth=0", pkg).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, pkg+"@"); i >= 0 {
			return strings.TrimSpace(line[i+len(pkg)+1:])
		}
	}
	return ""
}

func (m *Manager) isInstalled(ctx context.Context) bool {
	return m.installedVersion(ctx) != ""
}

// cachedVersion serves the last resolved version without blocking, kicking
// off a background refresh when the cache is empty or stale. checking is true
// only on the first-ever call while the refresh runs.
func (m *Manager) cachedVersion() (version string, checking bool) {
	m.verMu.Lock()
	ver := m.verCached
	checkedAt := m.verCheckedAt
	resolving := m.verResolving
	stale := checkedAt.IsZero() || time.Since(checkedAt) > verTTL
	neverResolved := checkedAt.IsZero()
	if stale && !resolving {
		m.verResolving = true
		go m.refreshVersion()
	}
	m.verMu.Unlock()
	return ver, neverResolved
}

func (m *Manager) refreshVersion() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v := m.installedVersion(ctx)
	m.verMu.Lock()
	m.verCached = v
	m.verCheckedAt = time.Now()
	m.verResolving = false
	m.verMu.Unlock()
}

// install runs `npm install -g <pkg>@latest` (first install and update).
func (m *Manager) install(ctx context.Context) (string, error) {
	bin, err := safeexec.ResolveBin("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found on PATH: %w", err)
	}
	m.log.Info().Str("pkg", m.desc.NpmPackage).Msg("airouter: npm install -g")
	out, err := safeexec.CommandContext(ctx, bin, "install", "-g", m.desc.NpmPackage+"@latest").CombinedOutput()
	if err != nil {
		m.log.Error().Err(err).Str("output", string(out)).Msg("airouter: npm install failed")
		return string(out), fmt.Errorf("npm install failed: %w", err)
	}
	m.log.Info().Msg("airouter: npm install ok")
	return string(out), nil
}

// ── process lifecycle ────────────────────────────────────────────────

// allocPort returns a free loopback port at or after start, so two routers
// that prefer the same port (both default to 20128) don't collide.
func allocPort(start int) int {
	if start <= 0 {
		start = 20128
	}
	for p := start; p < start+128; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			return p
		}
	}
	return start
}

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return nil
	}
	bin, err := safeexec.ResolveBin(m.desc.BinName)
	if err != nil {
		return fmt.Errorf("%s not installed: %w", m.desc.DisplayName, err)
	}
	port := allocPort(m.desc.PrefPort)
	m.port.Store(int32(port))
	m.logs.Reset()

	args, extraEnv := m.desc.Launch(port)

	// The router bin is a Node script with a `#!/usr/bin/env node` shebang.
	// On Termux /usr/bin/env doesn't exist, so exec'ing the script directly
	// fails. Resolve the underlying .js entry and launch it as `node <entry>`
	// to bypass the shebang. Equally valid on Linux/macOS/Windows.
	exe, cmdArgs := bin, args
	if node, script, ok := resolveNodeLauncher(bin); ok {
		exe = node
		cmdArgs = append([]string{script}, args...)
	}
	cmd := safeexec.Command(exe, cmdArgs...)
	cmd.Stdout = m.logs
	cmd.Stderr = m.logs
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	m.log.Info().Str("exe", exe).Str("bin", bin).Int("port", port).Msg("airouter: spawning")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", m.desc.DisplayName, err)
	}
	m.cmd = cmd
	m.log.Info().Int("pid", cmd.Process.Pid).Msg("airouter: spawned")
	return nil
}

// resolveNodeLauncher inspects the bin and, when it is a Node script, returns
// (nodePath, scriptPath, true) so the caller can spawn `node <script>` and
// sidestep the `#!/usr/bin/env node` shebang (unresolvable on Termux).
func resolveNodeLauncher(bin string) (node, script string, ok bool) {
	node, err := safeexec.ResolveBin("node")
	if err != nil {
		return "", "", false
	}
	script = bin
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		script = resolved
	}
	if !strings.HasSuffix(strings.ToLower(script), ".js") {
		f, err := os.Open(script)
		if err != nil {
			return "", "", false
		}
		defer f.Close()
		head := make([]byte, 64)
		n, _ := f.Read(head)
		if !strings.Contains(string(head[:n]), "node") {
			return "", "", false
		}
	}
	return node, script, true
}

func (m *Manager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return
	}
	pid := m.cmd.Process.Pid
	_ = m.cmd.Process.Kill()
	_ = m.cmd.Wait()
	m.cmd = nil
	m.log.Info().Int("pid", pid).Msg("airouter: stopped")
}

func (m *Manager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// backendReachable reports whether the router dashboard port answers a TCP
// connect — true even for a process started externally or one that outlived a
// wick restart, which is what the proxies actually care about.
func (m *Manager) backendReachable() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", m.boundPort()), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// runningNow reports whether THIS router is the process answering on the port.
// A process wick itself spawned (m.cmd != nil) is trusted outright. A process
// found listening that wick did NOT spawn is trusted only if its manifest
// identity matches — so a 9router externally started on the shared default
// port 20128 doesn't make the OmniRoute tile falsely report "running".
func (m *Manager) runningNow() bool {
	if !m.backendReachable() {
		return false
	}
	m.mu.Lock()
	spawned := m.cmd != nil
	m.mu.Unlock()
	if spawned {
		return true
	}
	return m.identityMatches()
}

// identityMatches confirms the process on the port is this router, via a
// TTL-cached probe of /manifest.webmanifest. Returns true when no signature is
// configured (nothing to distinguish on) so behaviour degrades to port-only.
func (m *Manager) identityMatches() bool {
	if m.desc.IdentitySubstr == "" {
		return true
	}
	m.identMu.Lock()
	if !m.identAt.IsZero() && time.Since(m.identAt) < identTTL {
		ok := m.identOK
		m.identMu.Unlock()
		return ok
	}
	m.identMu.Unlock()

	ok := m.probeIdentity()
	m.identMu.Lock()
	m.identOK = ok
	m.identAt = time.Now()
	m.identMu.Unlock()
	return ok
}

// probeIdentity fetches the backend's web manifest and checks whether its
// name/short_name carries this router's IdentitySubstr (case-insensitive).
func (m *Manager) probeIdentity() bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/manifest.webmanifest", m.boundPort())
	client := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var mf struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&mf); err != nil {
		return false
	}
	sub := strings.ToLower(m.desc.IdentitySubstr)
	return strings.Contains(strings.ToLower(mf.Name), sub) ||
		strings.Contains(strings.ToLower(mf.ShortName), sub)
}

func (m *Manager) isStarting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.starting
}

func (m *Manager) setStarting(v bool) {
	m.mu.Lock()
	m.starting = v
	m.mu.Unlock()
}

// StartAndWait spawns the process (if not running) and blocks until the
// dashboard answers or ctx expires.
func (m *Manager) StartAndWait(ctx context.Context) error {
	m.setStarting(true)
	defer m.setStarting(false)
	if err := m.start(); err != nil {
		return err
	}
	return m.waitReady(ctx)
}

// StopProcess kills the process. Exposed for shutdown hooks.
func (m *Manager) StopProcess() { m.stop() }

// Installed reports whether the npm package is present.
func (m *Manager) Installed(ctx context.Context) bool { return m.isInstalled(ctx) }

func (m *Manager) waitReady(ctx context.Context) error {
	checkURL := fmt.Sprintf("http://127.0.0.1:%d/", m.boundPort())
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become ready in time", m.desc.DisplayName)
		case <-time.After(200 * time.Millisecond):
			resp, err := http.Get(checkURL)
			if err == nil {
				resp.Body.Close()
				return nil
			}
		}
	}
}

// ── HTTP handlers (pure; caller does auth gating) ────────────────────

type statusResp struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Running   bool   `json:"running"`
	State     string `json:"state"` // "not-installed"|"checking"|"starting"|"running"|"stopped"
	Checking  bool   `json:"checking"`
}

// Status reports install + run state as JSON. Non-blocking.
func (m *Manager) Status(w http.ResponseWriter, r *http.Request) {
	version, checking := m.cachedVersion()
	running := m.runningNow()
	installed := version != "" || running

	state := "stopped"
	switch {
	case checking && !running:
		state = "checking"
	case !installed:
		state = "not-installed"
	case m.isStarting():
		state = "starting"
	case running:
		state = "running"
	}
	writeJSON(w, http.StatusOK, statusResp{
		Installed: installed,
		Version:   version,
		Running:   running,
		State:     state,
		Checking:  checking,
	})
}

// Install installs or updates the router package.
func (m *Manager) Install(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if out, err := m.install(ctx); err != nil {
		http.Error(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	v := m.installedVersion(ctx)
	m.verMu.Lock()
	m.verCached = v
	m.verCheckedAt = time.Now()
	m.verMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"version": v})
}

// Start spawns the process and waits for the dashboard to answer.
func (m *Manager) Start(w http.ResponseWriter, r *http.Request) {
	if !m.isInstalled(r.Context()) {
		http.Error(w, m.desc.DisplayName+" is not installed — install it first", http.StatusPreconditionFailed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if err := m.StartAndWait(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

// Logs returns the retained tail of the process output.
func (m *Manager) Logs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":    m.logs.Snapshot(),
		"running": m.isRunning(),
	})
}

// Stop kills the process.
func (m *Manager) Stop(w http.ResponseWriter, r *http.Request) {
	m.stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// Restart stops then starts, waiting for readiness.
func (m *Manager) Restart(w http.ResponseWriter, r *http.Request) {
	m.stop()
	if err := m.start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if err := m.waitReady(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

// ── reverse proxy (iframe content) ───────────────────────────────────

// ProxyHandler serves the dashboard subtree by proxying to the local router
// process, handling WebSocket upgrades for live updates. It strips the
// per-router mount prefix before forwarding.
func (m *Manager) ProxyHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.backendReachable() {
			http.Error(w, m.desc.DisplayName+" not running — start it first", http.StatusServiceUnavailable)
			return
		}
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			if !headerHasToken(r.Header, "Connection", "upgrade") {
				r.Header.Set("Connection", "Upgrade")
			}
			m.proxyWebSocket(w, r)
			return
		}
		m.proxy.ServeHTTP(w, r)
	})
	return http.StripPrefix(m.prefix, h)
}

// APIProxyHandler serves the OpenAI-compatible API subtree, mounted
// UNAUTHENTICATED so local AI CLIs reach the router without a wick session
// cookie — auth is the router's own API key. Body pass-through (no rewrite).
func (m *Manager) APIProxyHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.backendReachable() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": m.desc.DisplayName + " not running — start it first",
			})
			return
		}
		external := !isLoopbackHost(r.RemoteAddr)
		if external && !m.externalAPIAllowed() {
			m.publishRejected(r, external)
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": m.desc.DisplayName + " API is not exposed externally — enable it in Settings",
			})
			return
		}
		if external {
			if ip := clientIP(r); ip != "" {
				r.Header.Set(forwardClientHeader, ip)
			}
		}
		m.proxyAPI(w, r, external)
	})
	return http.StripPrefix(m.prefix, h)
}

// proxyAPI forwards the request, capturing + broadcasting full bodies only
// while a browser watches the Requests tab. No watcher → pure pass-through.
func (m *Manager) proxyAPI(w http.ResponseWriter, r *http.Request, external bool) {
	if !m.bcast.hasSubscribers() {
		m.apiProxy.ServeHTTP(w, r)
		return
	}

	var reqBody []byte
	if r.Body != nil {
		reqBody, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
		r.ContentLength = int64(len(reqBody))
		r.Header.Del("Content-Length")
	}

	start := time.Now()
	cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
	m.apiProxy.ServeHTTP(cw, r)
	dur := time.Since(start)

	m.bcast.publish(ReqEvent{
		Time:       nowClock(),
		Method:     r.Method,
		Path:       r.URL.Path,
		Host:       r.Host,
		RemoteAddr: r.RemoteAddr,
		ClientIP:   clientIP(r),
		External:   external,
		Auth:       authFromRequest(r),
		UserAgent:  r.Header.Get("User-Agent"),
		Model:      sniffModel(reqBody),
		Status:     cw.status,
		DurationMS: dur.Milliseconds(),
		ReqBody:    string(reqBody),
		RespBody:   string(cw.body),
	})
}

func (m *Manager) publishRejected(r *http.Request, external bool) {
	if !m.bcast.hasSubscribers() {
		return
	}
	m.bcast.publish(ReqEvent{
		Time:       nowClock(),
		Method:     r.Method,
		Path:       r.URL.Path,
		Host:       r.Host,
		RemoteAddr: r.RemoteAddr,
		ClientIP:   clientIP(r),
		External:   external,
		Auth:       authFromRequest(r),
		UserAgent:  r.Header.Get("User-Agent"),
		Status:     http.StatusForbidden,
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func nowClock() string { return time.Now().Format("15:04:05") }

func (m *Manager) externalAPIAllowed() bool {
	return m.externalAllowed != nil && m.externalAllowed()
}

// SetExternalAllowed wires the getter backing the external-API decision.
func (m *Manager) SetExternalAllowed(fn func() bool) { m.externalAllowed = fn }

// ReqStream is the SSE endpoint the Requests tab connects to. It subscribes
// the caller to live request events until the client disconnects.
func (m *Manager) ReqStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		m.log.Warn().Err(err).Msg("airouter: reqstream flush unsupported")
		return
	}

	ch, unsubscribe := m.bcast.subscribe()
	defer unsubscribe()

	// The Requests tab can sit idle for minutes between calls. Send a comment
	// every 15s so an intermediary (Cloudflare, a tunnel, nginx) doesn't reap
	// the idle stream and force a reconnect loop. Mirrors the conversation
	// /stream keepalive. The leading ": connected" confirms liveness at once.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	fmt.Fprintf(w, ": connected\n\n")
	_ = rc.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

// LogStream is the SSE endpoint tailing live process output: a snapshot on
// connect, then incremental chunks, plus a reset sentinel on (re)start.
func (m *Manager) LogStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		m.log.Warn().Err(err).Msg("airouter: logstream flush unsupported")
		return
	}

	initial, ch, unsubscribe := m.logs.subscribe()
	defer unsubscribe()

	send := func(chunk string) bool {
		b, err := json.Marshal(chunk)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		_ = rc.Flush()
		return true
	}

	if initial != "" && !send(initial) {
		return
	}

	// Keepalive comment every 15s so an idle log stream (a stopped or quiet
	// router) isn't reaped by an intermediary into a reconnect loop.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if !send(chunk) {
				return
			}
		}
	}
}

func (m *Manager) proxyWebSocket(w http.ResponseWriter, r *http.Request) {
	addr := fmt.Sprintf("127.0.0.1:%d", m.boundPort())
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	targetURL := "ws://" + addr + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	reqProtos := websocket.Subprotocols(r)
	header := http.Header{"Origin": {fmt.Sprintf("http://%s", addr)}}
	if len(reqProtos) > 0 {
		header["Sec-WebSocket-Protocol"] = reqProtos
	}

	backend, resp, err := websocket.DefaultDialer.Dial(targetURL, header)
	if err != nil {
		m.log.Error().Err(err).Str("target", targetURL).Msg("airouter: ws dial failed")
		http.Error(w, "ws backend unavailable", http.StatusBadGateway)
		return
	}
	defer backend.Close()

	var respHeader http.Header
	if resp != nil {
		if p := resp.Header.Get("Sec-WebSocket-Protocol"); p != "" {
			respHeader = http.Header{"Sec-WebSocket-Protocol": {p}}
		}
	}

	client, err := m.upgrader.Upgrade(w, r, respHeader)
	if err != nil {
		m.log.Error().Err(err).Msg("airouter: ws upgrade failed")
		return
	}
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			mt, msg, err := client.ReadMessage()
			if err != nil {
				return
			}
			if err := backend.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}()
	go func() {
		for {
			mt, msg, err := backend.ReadMessage()
			if err != nil {
				return
			}
			if err := client.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}()
	<-done
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
