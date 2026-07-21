package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Live sessions are the persistent-browser mode. Unlike the ephemeral task ops
// (launch → act → close), a live session is a Chromium process launched
// DETACHED — its lifetime is independent of this plugin subprocess.
//
// Why detached: wick kills an idle plugin subprocess (default 5min) and evicts
// it when the pool is full. A browser held in this process's memory would die
// with it. So instead we spawn Chromium as its own OS process listening on a
// CDP debug port, and persist just the {pid, cdp endpoint} to a file. Any later
// call — even one served by a freshly respawned plugin process — reads the file
// and reconnects over CDP (playwright.Chromium.ConnectOverCDP). The browser (and
// its open tabs) survive across plugin restarts; only session_close ends it.
//
// This was validated on Windows where a fixed debug port fails (port-exclusion
// ranges + sandbox policy): the fix is --remote-debugging-port=0 (chrome picks a
// free port) read back from the DevToolsActivePort file, plus --no-sandbox.

// sessionMeta is the on-disk record for one live browser, stored at
// <sessionDir>/<id>.json. It is the ONLY state that outlives the plugin
// process; the *playwright.Browser handle is reconnected per call from CDPURL.
type sessionMeta struct {
	ID       string    `json:"id"`
	PID      int       `json:"pid"`
	CDPURL   string    `json:"cdp_url"`
	Browser  string    `json:"browser"`
	Created  time.Time `json:"created"`
	UserData string    `json:"user_data_dir"`
}

// liveConn is a per-call reconnection to a live session: a fresh playwright Run
// plus a CDP-attached Browser. Close disconnects (pw.Stop + browser disconnect)
// WITHOUT killing the detached Chromium — that only happens on session_close.
type liveConn struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	meta    sessionMeta
}

func (lc *liveConn) close() {
	if lc.browser != nil {
		_ = lc.browser.Close() // CDP: disconnect only, chrome process stays
	}
	if lc.pw != nil {
		_ = lc.pw.Stop()
	}
}

// sessionDir is where session metadata AND downloaded browser assets live.
// Resolution order:
//  1. session_dir config — explicit admin override, always wins.
//  2. the plugin's persistent data dir (~/.<app>/plugins/playwright_browser),
//     resolved from the binary's own location by wickplugin.DataDir — no env,
//     no host cooperation. This is where big downloads (cloakbrowser, ~378MB)
//     land so they survive OS temp cleanups.
//
// wickplugin.DataDir itself falls back to <os-temp>/wick-plugins only for
// throwaway dev runs where the binary isn't under an installed plugins tree.
func sessionDir(c *connector.Ctx) string {
	if d := strings.TrimSpace(c.Cfg("session_dir")); d != "" {
		return d
	}
	return wickplugin.DataDir(pluginKey)
}

// maxLiveSessions is the safety cap on concurrently-live browsers.
//
// Config max_live_sessions. The field seeds as 1 (default=1 tag), so a fresh
// instance shows "1" not a confusing "0". An explicit 0 (or negative) means
// UNLIMITED — the admin opted out of the cap. Returns 0 to signal "no limit";
// openSession special-cases it rather than comparing len >= 0.
func maxLiveSessions(c *connector.Ctx) int {
	v := c.CfgInt("max_live_sessions")
	if v <= 0 {
		return 0 // unlimited
	}
	return v
}

// maxTabsPerSession is the per-session tab cap (config max_tabs_per_session).
// Seeds as 1 (default=1) so sessions are single-tab unless explicitly opened up.
// An explicit 0 (or negative) means UNLIMITED. Returns 0 to signal "no limit".
func maxTabsPerSession(c *connector.Ctx) int {
	v := c.CfgInt("max_tabs_per_session")
	if v <= 0 {
		return 0 // unlimited
	}
	return v
}

// ── open ─────────────────────────────────────────────────────────────

// openSession launches a detached Chromium on a dynamic CDP port and persists
// its metadata. It enforces maxLiveSessions and returns the new session id.
func openSession(c *connector.Ctx) (any, error) {
	dir := sessionDir(c)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	// Enforce the live-session cap (count valid, still-running sessions).
	// listSessions already sweeps dead ones, so a browser that crashed or was
	// killed no longer counts against the cap. cap == 0 means unlimited.
	live, err := listSessions(c)
	if err != nil {
		return nil, err
	}
	if cap := maxLiveSessions(c); cap > 0 && len(live) >= cap {
		return nil, fmt.Errorf("live session limit reached (%d/%d): close one with session_close before opening another (or set max_live_sessions to 0 for unlimited)", len(live), cap)
	}

	// Live sessions rely on the Chromium DevTools protocol (--remote-debugging-port
	// + the DevToolsActivePort file). Only Chromium engines expose that: the stock
	// chromium and cloakbrowser (patched Chromium). Firefox/WebKit don't, so guard
	// early with a clear message instead of hanging until the 20s port timeout.
	b := strings.ToLower(strings.TrimSpace(c.Cfg("browser")))
	if b != "" && b != defBrowser && b != cloakEngine {
		return nil, fmt.Errorf("live sessions require a chromium-based engine (chromium or cloakbrowser); this instance uses %q. Use the ephemeral ops (run/screenshot/...) for firefox/webkit, or set browser=chromium", c.Cfg("browser"))
	}

	// Resolve the browser binary via playwright (respects executable_path too).
	pw, err := driverFor(c)
	if err != nil {
		return nil, err
	}
	defer pw.Stop()
	bt, err := browserType(pw, c.Cfg("browser"))
	if err != nil {
		return nil, err
	}
	// Binary precedence mirrors launchOptions(): explicit executable_path wins;
	// otherwise cloakbrowser uses its downloaded stealth binary, and stock
	// chromium falls back to the Playwright-managed binary. browserType maps
	// cloakbrowser onto pw.Chromium, so bt.ExecutablePath() would point at the
	// wrong (stock chromium) binary for cloak — resolve cloak explicitly.
	chromeBin := strings.TrimSpace(c.Cfg("executable_path"))
	if chromeBin == "" && b == cloakEngine {
		chromeBin = cloakBinaryPath(c)
	}
	if chromeBin == "" {
		chromeBin = bt.ExecutablePath()
	}
	if chromeBin == "" {
		return nil, fmt.Errorf("could not resolve a browser binary for live sessions")
	}

	id := newSessionID(c)
	udd := filepath.Join(dir, "profile-"+id)

	args := []string{
		"--remote-debugging-port=0", // dynamic port — dodges Windows port-exclusion ranges
		"--user-data-dir=" + udd,
		"--no-first-run", "--no-default-browser-check",
		"--no-sandbox", // required where the sandbox helper is blocked
	}
	// Honor the Headless config. Use --headless=new (not classic headless) so
	// that --load-extension still works when extensions are installed — classic
	// headless ignores extensions entirely; the "new" mode loads them (some
	// extensions are still finicky under headless, but this keeps the session
	// invisible as configured instead of forcing a visible window).
	if headless(c) {
		args = append(args, "--headless=new")
	}
	if exts := installedExtensions(c); len(exts) > 0 {
		joined := strings.Join(exts, ",")
		args = append(args, "--load-extension="+joined, "--disable-extensions-except="+joined)
	}
	if px := strings.TrimSpace(c.Cfg("proxy_server")); px != "" {
		args = append(args, "--proxy-server="+px)
	}

	cmd := safeexec.Command(chromeBin, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launch detached browser: %w", err)
	}

	// Chrome writes the chosen port to <udd>/DevToolsActivePort once ready.
	port, ok := readDevToolsPort(udd, 20*time.Second)
	if !ok {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("browser did not expose a CDP port within 20s (check host sandbox/port policy)")
	}
	cdpURL := "http://127.0.0.1:" + port

	meta := sessionMeta{
		ID:       id,
		PID:      cmd.Process.Pid,
		CDPURL:   cdpURL,
		Browser:  c.Cfg("browser"),
		Created:  sessionNow(c),
		UserData: udd,
	}
	if err := writeMeta(dir, meta); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("persist session: %w", err)
	}
	// Detach: we intentionally do not Wait() — the browser outlives this call
	// and this plugin process. session_close reaps it by PID.
	return map[string]any{
		"session_id": id,
		"pid":        meta.PID,
		"cdp_url":    cdpURL,
		"note":       "Live browser started. Pass this session_id to run/screenshot/etc to reuse it. Close it with session_close when done.",
	}, nil
}

// ── connect ──────────────────────────────────────────────────────────

// connectSession reads a session's metadata and reconnects over CDP. The
// returned liveConn.close() disconnects without killing the browser.
func connectSession(c *connector.Ctx, id string) (*liveConn, error) {
	meta, err := readMeta(sessionDir(c), id)
	if err != nil {
		return nil, err
	}
	// Reconnecting to an already-running browser over CDP never launches a new
	// browser, so it only needs the node driver — never the Chromium download.
	pw, err := ensureDriverNoInstall()
	if err != nil {
		return nil, err
	}
	bt, err := browserType(pw, meta.Browser)
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}
	browser, err := bt.ConnectOverCDP(meta.CDPURL)
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("reconnect to session %s: %w (the browser may have been closed; run session_close to clean up)", id, err)
	}
	return &liveConn{pw: pw, browser: browser, meta: meta}, nil
}

// firstContext returns the browser's default context, creating a page-less one
// only if somehow none exists (a CDP-attached chrome always has one).
func (lc *liveConn) firstContext() (playwright.BrowserContext, error) {
	ctxs := lc.browser.Contexts()
	if len(ctxs) == 0 {
		return nil, fmt.Errorf("session %s has no browser context", lc.meta.ID)
	}
	return ctxs[0], nil
}

// ── list / tabs / close ──────────────────────────────────────────────

// listSessions returns metadata for every session file whose browser is still
// reachable. Stale files (dead PID / refused CDP) are swept so the cap and the
// list stay accurate.
func listSessions(c *connector.Ctx) ([]sessionMeta, error) {
	dir := sessionDir(c)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session dir: %w", err)
	}
	var out []sessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		meta, err := readMeta(dir, id)
		if err != nil {
			continue
		}
		if !cdpAlive(meta.CDPURL) {
			// Browser gone: sweep the stale metadata + profile dir.
			removeSession(dir, meta)
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

// sessionList is the session_list op: every live session plus its open tabs.
func sessionList(c *connector.Ctx) (any, error) {
	metas, err := listSessions(c)
	if err != nil {
		return nil, err
	}
	sessions := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		tabs := describeTabs(c, m.ID)
		sessions = append(sessions, map[string]any{
			"session_id": m.ID,
			"pid":        m.PID,
			"browser":    m.Browser,
			"created":    m.Created,
			"tabs":       tabs,
		})
	}
	// max_tabs lets the UI disable "new tab" at the cap (0 = unlimited).
	return map[string]any{"sessions": sessions, "count": len(sessions), "max_tabs": maxTabsPerSession(c)}, nil
}

// describeTabs lists a session's open pages (index, url, title). Best-effort:
// returns nil on any connect error rather than failing the whole list.
func describeTabs(c *connector.Ctx, id string) []map[string]any {
	lc, err := connectSession(c, id)
	if err != nil {
		return nil
	}
	defer lc.close()
	ctx, err := lc.firstContext()
	if err != nil {
		return nil
	}
	pages := ctx.Pages()
	tabs := make([]map[string]any, 0, len(pages))
	for i, p := range pages {
		title, _ := p.Title()
		tabs = append(tabs, map[string]any{"index": i, "url": p.URL(), "title": title})
	}
	return tabs
}

// cdpTarget is one row of Chrome's GET <cdp>/json — a debuggable target (a tab,
// worker, etc). We keep only page targets and expose the WebSocket debugger URL
// the live-browser panel's core-side proxy dials for screencast + input.
type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// sessionEndpoints returns the raw CDP connection details for a live session so
// the CORE process can proxy a DevTools WebSocket to it (screencast + input) —
// core reaches the loopback CDP port directly; the plugin only discovers it.
//
// Output: the session's cdp_url plus one entry per page target with its
// ws_debugger_url. Read straight from Chrome's GET <cdp>/json (not playwright),
// because that endpoint is the source of the per-target debugger WebSocket URLs.
// This is a maintenance/UI read — not meant for agent use — hence seeded
// AdminOnly like the other manager-backing ops.
func sessionEndpoints(c *connector.Ctx, id string) (any, error) {
	meta, err := readMeta(sessionDir(c), id)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(meta.CDPURL + "/json")
	if err != nil {
		return nil, fmt.Errorf("reach CDP endpoint for session %s: %w (the browser may have been closed; run session_close to clean up)", id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDP /json returned %d for session %s", resp.StatusCode, id)
	}

	var targets []cdpTarget
	if err := json.Unmarshal(raw, &targets); err != nil {
		return nil, fmt.Errorf("decode CDP targets: %w", err)
	}

	tabs := make([]map[string]any, 0, len(targets))
	index := 0
	for _, t := range targets {
		if t.Type != "page" {
			continue // skip workers, service workers, iframes, etc.
		}
		tabs = append(tabs, map[string]any{
			"index":           index,
			"target_id":       t.ID,
			"ws_debugger_url": t.WebSocketDebuggerURL,
			"url":             t.URL,
			"title":           t.Title,
		})
		index++
	}
	return map[string]any{
		"session_id": id,
		"cdp_url":    meta.CDPURL,
		"tabs":       tabs,
		"count":      len(tabs),
	}, nil
}

// tabNew opens a new tab (optionally navigating to url) in a live session.
func tabNew(c *connector.Ctx, id, url string) (any, error) {
	lc, err := connectSession(c, id)
	if err != nil {
		return nil, err
	}
	defer lc.close()
	ctx, err := lc.firstContext()
	if err != nil {
		return nil, err
	}
	// Enforce the per-session tab cap. Default 1 (single-tab) — multi-tab is
	// opt-in via MaxTabsPerSession because each tab holds a page in RAM. 0 =
	// unlimited. Applies to both the manager UI "+" and agent (MCP) calls.
	if cap := maxTabsPerSession(c); cap > 0 && len(ctx.Pages()) >= cap {
		return nil, fmt.Errorf(
			"tab limit reached: this session already has %d/%d tab(s). Multi-tab is disabled by default to save memory (each tab keeps a live page in RAM). "+
				"To open more tabs, an operator must raise \"Max tabs per session\" on this connector's settings (config max_tabs_per_session; 0 = unlimited). "+
				"Alternatively, reuse the current tab (run with the existing tab index) or close a tab (tab_close) before opening a new one",
			len(ctx.Pages()), cap)
	}
	page, err := ctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("open tab: %w", err)
	}
	if url != "" {
		if _, err := page.Goto(url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateLoad}); err != nil {
			return nil, fmt.Errorf("navigate new tab to %s: %w", url, err)
		}
	}
	return map[string]any{"session_id": id, "index": len(ctx.Pages()) - 1, "url": page.URL()}, nil
}

// tabClose closes the tab at index in a live session.
func tabClose(c *connector.Ctx, id string, index int) (any, error) {
	lc, err := connectSession(c, id)
	if err != nil {
		return nil, err
	}
	defer lc.close()
	ctx, err := lc.firstContext()
	if err != nil {
		return nil, err
	}
	pages := ctx.Pages()
	if index < 0 || index >= len(pages) {
		return nil, fmt.Errorf("tab index %d out of range (session has %d tabs)", index, len(pages))
	}
	if err := pages[index].Close(); err != nil {
		return nil, fmt.Errorf("close tab %d: %w", index, err)
	}
	return map[string]any{"session_id": id, "closed": index, "remaining": len(ctx.Pages())}, nil
}

// closeSession kills the detached browser and removes its files.
func closeSession(c *connector.Ctx, id string) (any, error) {
	dir := sessionDir(c)
	meta, err := readMeta(dir, id)
	if err != nil {
		return nil, err
	}
	killPID(meta.PID)
	removeSession(dir, meta)
	return map[string]any{"session_id": id, "closed": true}, nil
}

// ── file + process helpers ───────────────────────────────────────────

// sessionIDRE matches the ids minted by newSessionID (inst-pid-nano). Anything
// else — path separators, "..", empty — is rejected so a caller-supplied
// session_id can't traverse out of the session dir into arbitrary files.
var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validSessionID(id string) bool { return sessionIDRE.MatchString(id) }

func metaPath(dir, id string) string { return filepath.Join(dir, id+".json") }

func writeMeta(dir string, m sessionMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(dir, m.ID), b, 0o644)
}

func readMeta(dir, id string) (sessionMeta, error) {
	var m sessionMeta
	if !validSessionID(id) {
		return m, fmt.Errorf("invalid session id %q", id)
	}
	b, err := os.ReadFile(metaPath(dir, id))
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("no live session %q (already closed or never opened)", id)
		}
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("corrupt session file for %q: %w", id, err)
	}
	return m, nil
}

// removeSession deletes the metadata file and the browser's profile dir.
func removeSession(dir string, m sessionMeta) {
	_ = os.Remove(metaPath(dir, m.ID))
	if m.UserData != "" {
		_ = os.RemoveAll(m.UserData)
	}
}

// readDevToolsPort reads <udd>/DevToolsActivePort. Chrome writes the port it
// bound (first line) once the CDP endpoint is ready — the reliable way to learn
// the port when launched with --remote-debugging-port=0.
func readDevToolsPort(udd string, wait time.Duration) (string, bool) {
	f := filepath.Join(udd, "DevToolsActivePort")
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(f); err == nil {
			if line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]); line != "" {
				return line, true
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return "", false
}

// cdpAlive reports whether a CDP endpoint answers /json/version — the liveness
// probe used to sweep dead sessions.
func cdpAlive(cdpURL string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(cdpURL + "/json/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
