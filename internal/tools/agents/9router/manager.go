// Package router9 manages the embedded 9router dashboard. 9router ships
// as an npm package (`npm install -g 9router`) and serves a web
// dashboard on a local port (20128). This package installs/updates the
// package on demand, starts/stops/restarts the process, and
// reverse-proxies its dashboard so it can be embedded in an iframe —
// the host never exposes the underlying port to the user.
//
// The package is self-contained: it exposes pure http.Handler methods
// and knows nothing about the Agents shell. The agents package wires
// these handlers under /tools/agents/9router/* and renders the page
// chrome around the iframe.
package router9

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/safeexec"
)

// pkgName is the npm package providing the `9router` command.
const pkgName = "9router"

// port is 9router's built-in dashboard port. Hardcoded — the process
// binds it on loopback only and wick proxies to it, so the value never
// leaks to the user.
const port = 20128

// Manager owns the lifecycle of the 9router child process and the
// reverse proxy fronting its dashboard. Construct once with New and
// share across requests.
type Manager struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	starting bool // true between spawn and dashboard-ready (drives "Starting" status)
	prefix   string
	proxy    *httputil.ReverseProxy
	// apiProxy forwards the OpenAI-compatible API subtree (/v1/*) to the
	// backend WITHOUT the HTML/JS/CSS body rewrite the dashboard proxy
	// applies — API responses are JSON and must pass through byte-for-byte.
	apiProxy *httputil.ReverseProxy
	upgrader websocket.Upgrader
	log      zerolog.Logger
	logs     *logBuffer
}

// MountPrefix is the wick-root path the 9router dashboard is proxied
// under. It MUST be a top-level path (not nested under /tools/...) so
// that 9router's root-absolute URLs (/login, /dashboard, /_next/...)
// rewrite cleanly to a single prefix. The iframe points here.
const MountPrefix = "/9router"

// New returns a Manager whose proxy strips MountPrefix before forwarding
// to the local 9router process and rewrites the response so the
// root-absolute Next.js app works under the subpath.
func New() *Manager {
	logger := log.With().Str("component", "9router").Logger()

	backendURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		// Ask the backend for plaintext so ModifyResponse can rewrite
		// HTML/JS bodies without gunzip+regzip on every chunk.
		req.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = rewriteResponse
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error().Err(err).Str("path", r.URL.Path).Msg("9router: proxy error")
		http.Error(w, "9router backend unavailable", http.StatusBadGateway)
	}

	// apiProxy is a plain pass-through to the same backend: no
	// Accept-Encoding stripping, no body rewrite. Used for /v1/* so the
	// OpenAI-compatible JSON (and SSE streams) reach clients untouched.
	//
	// Uses Rewrite (not the deprecated Director): Rewrite runs AFTER
	// hop-by-hop header stripping and does NOT auto-forward X-Forwarded-*
	// from the inbound request. That is exactly what we need — 9router gates
	// its API by origin (loopback = no key, remote = key required), and by
	// deliberately NOT calling pr.SetXForwarded() the proxied request reaches
	// 9router looking local, so wick's own /9router/v1 mount is the trust
	// boundary and 9router's local-auth passthrough applies. Director would
	// have preserved client-supplied X-Forwarded-For (spoofable → 401).
	apiProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backendURL)
			pr.Out.Host = backendURL.Host
			// Strip any inbound forwarding hints; do NOT SetXForwarded().
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("Forwarded")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error().Err(err).Str("path", r.URL.Path).Msg("9router: api proxy error")
			http.Error(w, "9router backend unavailable", http.StatusBadGateway)
		},
	}

	logger.Info().Int("port", port).Str("prefix", MountPrefix).Msg("9router: manager configured")
	return &Manager{
		prefix:   MountPrefix,
		proxy:    proxy,
		apiProxy: apiProxy,
		log:      logger,
		logs:     newLogBuffer(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// ── npm package lifecycle ────────────────────────────────────────────

// installedVersion returns the globally-installed 9router version, or
// "" when not installed. Parses `npm ls -g` output rather than trusting
// the exit code (npm ls exits non-zero on unrelated peer warnings).
func (m *Manager) installedVersion(ctx context.Context) string {
	bin, err := safeexec.ResolveBin("npm")
	if err != nil {
		m.log.Warn().Err(err).Msg("9router: npm not found on PATH")
		return ""
	}
	out, _ := safeexec.CommandContext(ctx, bin, "ls", "-g", "--depth=0", pkgName).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, pkgName+"@"); i >= 0 {
			return strings.TrimSpace(line[i+len(pkgName)+1:])
		}
	}
	return ""
}

func (m *Manager) isInstalled(ctx context.Context) bool {
	return m.installedVersion(ctx) != ""
}

// install runs `npm install -g 9router@latest` (covers first install
// and update). Returns combined output for surfacing on failure.
func (m *Manager) install(ctx context.Context) (string, error) {
	bin, err := safeexec.ResolveBin("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found on PATH: %w", err)
	}
	m.log.Info().Msg("9router: npm install -g 9router@latest")
	out, err := safeexec.CommandContext(ctx, bin, "install", "-g", pkgName+"@latest").CombinedOutput()
	if err != nil {
		m.log.Error().Err(err).Str("output", string(out)).Msg("9router: npm install failed")
		return string(out), fmt.Errorf("npm install failed: %w", err)
	}
	m.log.Info().Msg("9router: npm install ok")
	return string(out), nil
}

// ── process lifecycle ────────────────────────────────────────────────

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return nil
	}
	bin, err := safeexec.ResolveBin(pkgName)
	if err != nil {
		return fmt.Errorf("9router not installed: %w", err)
	}
	m.logs.Reset()
	// 9router takes CLI flags, not env vars. Bind loopback only (default
	// is 0.0.0.0), never open a browser, show logs so we can capture
	// them, and skip the interactive update check (which would otherwise
	// make the detached process exit early).
	cmd := safeexec.Command(bin,
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"--no-browser",
		"--log",
		"--skip-update",
	)
	cmd.Stdout = m.logs
	cmd.Stderr = m.logs

	m.log.Info().Str("bin", bin).Int("port", port).Msg("9router: spawning")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start 9router: %w", err)
	}
	m.cmd = cmd
	m.log.Info().Int("pid", cmd.Process.Pid).Msg("9router: spawned")
	return nil
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
	m.log.Info().Int("pid", pid).Msg("9router: stopped")
}

func (m *Manager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// backendReachable reports whether the 9router dashboard port answers a
// TCP connect. Unlike isRunning (which only knows about a process wick
// itself spawned via m.cmd), this also returns true for a 9router started
// externally or one that survived a wick restart — which is what the API
// proxy actually cares about. Cheap: a 300ms loopback dial.
func (m *Manager) backendReachable() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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

// StartAndWait spawns the process (if not already running) and blocks
// until the dashboard answers or ctx expires. While waiting, status
// reports "starting" so the UI can show a spinner instead of "Stopped".
// Used by both the HTTP Start handler and the boot auto-start hook.
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
	checkURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("9router did not become ready in time")
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
	// State is the single source of truth for the UI badge:
	// "not-installed" | "starting" | "running" | "stopped".
	State string `json:"state"`
}

// Status reports install + run state as JSON.
func (m *Manager) Status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	installed := m.isInstalled(ctx)
	running := m.isRunning()
	state := "stopped"
	switch {
	case !installed:
		state = "not-installed"
	case m.isStarting():
		state = "starting"
	case running:
		state = "running"
	}
	writeJSON(w, http.StatusOK, statusResp{
		Installed: installed,
		Version:   m.installedVersion(ctx),
		Running:   running,
		State:     state,
	})
}

// Install installs or updates 9router (npm install -g 9router@latest).
func (m *Manager) Install(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if out, err := m.install(ctx); err != nil {
		http.Error(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": m.installedVersion(ctx)})
}

// Start spawns the process and waits for the dashboard to answer.
func (m *Manager) Start(w http.ResponseWriter, r *http.Request) {
	if !m.isInstalled(r.Context()) {
		http.Error(w, "9router is not installed — install it first", http.StatusPreconditionFailed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := m.StartAndWait(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

// Logs returns the retained tail of the 9router process output as JSON
// { "logs": "...", "running": bool }.
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := m.waitReady(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

// ── reverse proxy (iframe content) ───────────────────────────────────

// ProxyHandler serves the dashboard subtree by proxying to the local
// 9router process, handling WebSocket upgrades for live updates.
func (m *Manager) ProxyHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.isRunning() {
			http.Error(w, "9router not running — start it first", http.StatusServiceUnavailable)
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
	if m.prefix != "" {
		return http.StripPrefix(m.prefix, h)
	}
	return h
}

// APIProxyHandler serves the OpenAI-compatible API subtree by proxying
// to the local 9router process. Unlike ProxyHandler it does NOT rewrite
// bodies (API responses are JSON / SSE) and is mounted UNAUTHENTICATED
// so codex/claude subprocesses (and other local clients) can reach it
// without a wick session cookie — auth is 9router's own API key.
//
// Path handling mirrors ProxyHandler: the wick-root prefix ("/9router")
// is stripped, so /9router/v1/models forwards to /v1/models on the
// backend. When the process is not running it returns 503 so callers get
// a clear signal instead of a connection error.
func (m *Manager) APIProxyHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Probe the backend port rather than m.cmd: a 9router started
		// externally (or one that outlived a wick restart) is still a valid
		// target for the API proxy even though wick didn't spawn it.
		if !m.backendReachable() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "9router not running — start it first",
			})
			return
		}
		m.apiProxy.ServeHTTP(w, r)
	})
	if m.prefix != "" {
		return http.StripPrefix(m.prefix, h)
	}
	return h
}

func (m *Manager) proxyWebSocket(w http.ResponseWriter, r *http.Request) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
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
		m.log.Error().Err(err).Str("target", targetURL).Msg("9router: ws dial failed")
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
		m.log.Error().Err(err).Msg("9router: ws upgrade failed")
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
