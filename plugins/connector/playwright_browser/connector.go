// Command playwright_browser is a wick connector plugin that drives a real
// browser (Chromium / Firefox / WebKit) via the embedded playwright-go library.
//
// Unlike an HTTP-wrapping connector it does NOT use c.HTTP: each operation
// launches an isolated browser inside THIS plugin subprocess, does its work,
// and closes it — no shared state, safe to run concurrently. Running as a
// plugin (its own process, gRPC to the host) keeps the heavy browser +
// Node-driver footprint out of the wick core process.
//
// The trade-off is a runtime dependency: playwright-go ships a Node-based
// driver and downloads browser binaries on first use. ensureDriver (repo.go)
// guards that install lazily so a host that has never run the install gets a
// clear, actionable error the first time an op runs, not a crash.
//
// Two op flavours:
//
//   - Task ops (screenshot, get_content, pdf, scrape, eval) — high-level,
//     self-contained "open URL → do one thing → return" actions.
//   - run — a script runner: an ordered JSON list of browser actions executed
//     in one live session, returning a result per step. The escape hatch for
//     stateful multi-step flows the task ops can't express.
//
// Per-instance Config maps onto the same knobs the official @playwright/mcp
// server exposes as CLI flags: browser choice, headless, a custom browser
// binary, viewport, user agent, device emulation, proxy, timeouts, a
// storage-state seed, and a per-run tab cap.
//
// File layout mirrors the standard wick connector split (all package main
// here because a plugin is a binary):
//
//   - connector.go — Module(): Meta, Config, per-op Input structs, Operations,
//     and the thin op handlers (this file).
//   - service.go   — pure Go: input validation, launch/context option builders,
//     and the action model for `run`.
//   - repo.go      — everything that touches the browser: driver install guard,
//     session lifecycle, and action execution.
package main

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

// Config is the per-instance browser configuration. Every operation on this
// instance launches a browser using these values; they map 1:1 onto the flags
// the official @playwright/mcp server accepts.
type Config struct {
	// Browser — the essentials, always visible. Everything else is grouped into
	// collapsed cards so the page opens clean; expand a card to tweak.
	Browser  string `wick:"html=browser_status;default=chromium;group=Browser|Pick an engine. Each shows install status; download a missing one right here.;desc=Browser engine to launch."`
	Headless bool   `wick:"bool;default=true;group=Browser;desc=Run without a visible window. Turn off only for local debugging."`

	// Display — viewport + emulation.
	ViewportWidth  int    `wick:"default=1280;group=Display|Viewport size, user agent, and device emulation.|collapsed;desc=Viewport width in pixels. Ignored when a device is set."`
	ViewportHeight int    `wick:"default=800;group=Display;desc=Viewport height in pixels. Ignored when a device is set."`
	UserAgent      string `wick:"group=Display;desc=Override the User-Agent header. Leave empty for the browser default."`
	Device         string `wick:"group=Display;desc=Emulate a named device (e.g. \"iPhone 15\", \"Pixel 7\"). Sets viewport, UA, and touch. Overrides the viewport fields when set."`

	// Network — proxy.
	ProxyServer string `wick:"group=Network|Route browser traffic through a proxy.|collapsed;desc=Proxy for all browser traffic. Example: http://proxy.abc.com:3128 or socks5://proxy.abc.com:1080"`
	ProxyBypass string `wick:"group=Network;desc=Comma-separated domains to bypass the proxy. Example: .abc.com, localhost"`

	// Timeouts + limits.
	ActionTimeoutMs     int `wick:"default=5000;group=Timeouts & limits|Per-action / navigation timeouts and the per-run tab cap.|collapsed;desc=Per-action timeout in milliseconds (click, fill, wait_for)."`
	NavigationTimeoutMs int `wick:"default=30000;group=Timeouts & limits;desc=Page navigation timeout in milliseconds (goto)."`
	MaxTab              int `wick:"default=5;group=Timeouts & limits;desc=Maximum pages (tabs) a single run may open. Guards against a script fanning out unbounded."`

	// Live-session mode (session_open / session_list / tab_* / session_close).
	SessionDir      string `wick:"group=Live sessions|Persistent-browser mode: where sessions are stored and how many may run.|collapsed;desc=Directory where live-session metadata + browser profiles are stored. Live browsers survive plugin restarts via these files. Default: OS temp dir /wick-playwright-sessions."`
	MaxLiveSessions int    `wick:"default=1;group=Live sessions;desc=Maximum persistent browsers alive at once (session_open cap). Guards RAM. Set 0 for unlimited. Default 1."`

	// Custom binary — rarely touched.
	ExecutablePath string `wick:"group=Custom binary|Point at a non-bundled browser build. Most setups leave these empty.|collapsed;desc=Path to a custom browser binary to launch instead of the bundled one. Example: /usr/bin/google-chrome"`
	Channel        string `wick:"group=Custom binary;desc=Branded channel for the chosen browser (chrome, chrome-beta, msedge, ...). Leave empty for the bundled build."`

	// CloakBrowser — stealth Chromium downloaded from GitHub (not a Playwright
	// engine). Only relevant when the cloakbrowser engine is selected.
	CloakRepo           string `wick:"group=CloakBrowser|Stealth Chromium engine. Downloaded from a GitHub release; override the source or point at a local binary.|collapsed;desc=GitHub owner/repo hosting CloakBrowser release assets. Default: CloakHQ/CloakBrowser."`
	CloakExecutablePath string `wick:"group=CloakBrowser;desc=Path to an already-downloaded CloakBrowser binary. Set this to skip the GitHub download (e.g. on a platform with no published build)."`
}

// ── Per-operation input structs ──────────────────────────────────────

// screenshotInput is the argument schema for the "screenshot" operation.
type screenshotInput struct {
	URL      string `wick:"url;required;desc=Page URL to open. Example: https://abc.com"`
	FullPage bool   `wick:"bool;desc=Capture the entire scrollable page instead of just the viewport."`
	Selector string `wick:"desc=Optional CSS selector. When set, only that element is captured instead of the page."`
	WaitFor  string `wick:"desc=Optional CSS selector to wait for before capturing. Useful for JS-rendered content."`
}

// getContentInput is the argument schema for the "get_content" operation.
type getContentInput struct {
	URL      string `wick:"url;required;desc=Page URL to open."`
	Selector string `wick:"desc=Optional CSS selector. When set, returns that element's inner text; otherwise the whole page."`
	AsText   bool   `wick:"bool;default=true;desc=Return visible text (default) instead of the rendered HTML."`
	WaitFor  string `wick:"desc=Optional CSS selector to wait for before reading. Useful for JS-rendered content."`
}

// pdfInput is the argument schema for the "pdf" operation.
type pdfInput struct {
	URL     string `wick:"url;required;desc=Page URL to render as PDF. Chromium only."`
	WaitFor string `wick:"desc=Optional CSS selector to wait for before rendering."`
}

// scrapeInput is the argument schema for the "scrape" operation.
type scrapeInput struct {
	URL     string `wick:"url;required;desc=Page URL to open."`
	Fields  string `wick:"textarea;required;desc=JSON object mapping result keys to CSS selectors. Example: {\"title\":\"h1\",\"price\":\".price\"}. Each selector's inner text is returned under its key."`
	WaitFor string `wick:"desc=Optional CSS selector to wait for before scraping."`
}

// evalInput is the argument schema for the "eval" operation.
type evalInput struct {
	URL    string `wick:"url;required;desc=Page URL to open before evaluating."`
	Script string `wick:"textarea;required;desc=JavaScript expression evaluated in the page. The returned value is JSON-serialized. Example: document.title"`
}

// runInput is the argument schema for the "run" script-runner operation.
type runInput struct {
	Actions   string `wick:"textarea;required;desc=JSON array of action objects run in order in one browser session. Each has an \"action\" key. NAVIGATION: goto{url}, go_back, go_forward, reload, wait_for_load_state{state?}, wait_for_url{url}. INTERACTION: click{selector}, dblclick{selector}, hover{selector}, tap{selector}, focus{selector}, fill{selector,value}, type{selector,value}, press{key,selector?}, check{selector}, uncheck{selector}, select_option{selector,value|values}, set_input_files{selector,files}, drag_and_drop{selector,target}, scroll{delta_x?,delta_y?}. WAIT: wait_for{selector}, wait{ms}. READ: screenshot{full_page?,selector?}, content{selector?}, eval{script}, get_attribute{selector,attr}, text_content{selector}, inner_html{selector}, is_visible{selector}, is_checked{selector}, count{selector}, title, url. Returns one result per step; stops at the first failure. Example: [{\"action\":\"goto\",\"url\":\"https://abc.com\"},{\"action\":\"fill\",\"selector\":\"#q\",\"value\":\"hi\"},{\"action\":\"click\",\"selector\":\"button[type=submit]\"},{\"action\":\"wait_for\",\"selector\":\".result\"},{\"action\":\"screenshot\",\"full_page\":true}]"`
	SessionID string `wick:"desc=Optional live session id (from session_open). When set, actions run in that persistent browser and the browser is NOT closed afterwards. Leave empty for a throwaway browser launched and closed for this call."`
}

// ── Live session inputs ──────────────────────────────────────────────

// sessionOpenInput opens a persistent browser. It takes no per-call args today
// (browser/headless/proxy come from Config); the empty struct keeps the schema
// explicit and lets fields be added later without a signature change.
type sessionOpenInput struct{}

// sessionListInput lists live sessions and their tabs. No arguments.
type sessionListInput struct{}

// tabNewInput opens a new tab in a live session.
type tabNewInput struct {
	SessionID string `wick:"required;desc=Live session id from session_open."`
	URL       string `wick:"url;desc=Optional URL to navigate the new tab to. Leave empty for a blank tab."`
}

// tabCloseInput closes one tab in a live session.
type tabCloseInput struct {
	SessionID string `wick:"required;desc=Live session id from session_open."`
	Index     int    `wick:"desc=Zero-based tab index (from session_list). Default 0 (first tab)."`
}

// sessionCloseInput ends a live session (kills the browser).
type sessionCloseInput struct {
	SessionID string `wick:"required;desc=Live session id to close. Kills the browser and frees its resources."`
}

// ── Maintenance inputs ───────────────────────────────────────────────

// browserStatusInput reports install state for every engine. No arguments.
type browserStatusInput struct{}

// browserInstallInput downloads one engine's browser binary.
type browserInstallInput struct {
	Browser string `wick:"dropdown=chromium|firefox|webkit|cloakbrowser;required;desc=Engine to download."`
}

// Module returns the connector definition served over gRPC by main().
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			// Key MUST equal the folder name: connector/playwright_browser/ →
			// "playwright_browser". Underscore, not hyphen — a hyphen would break
			// the <key>-<ver>-<os>-<arch>.zip split.
			Key:         "playwright_browser",
			Name:        "Playwright Browser",
			Description: "Drive a real browser (Chromium/Firefox/WebKit) to screenshot, scrape, render PDFs, evaluate JS, and run scripted interaction flows. Runs an isolated browser per call inside the plugin process.",
			Icon:        "🎭",
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.Browser},
		},
		Configs: entity.StructToConfigs(Config{}),
		Operations: []connector.Category{
			connector.Cat(
				"Page tasks",
				"Open a URL and perform one self-contained action. Each op launches and closes its own browser.",
				connector.Op(
					"screenshot",
					"Screenshot Page",
					"Open {url} and return a PNG screenshot as base64. Set full_page to capture the whole scrollable page, or selector to capture one element. Use wait_for to delay until JS-rendered content appears.",
					screenshotInput{},
					screenshot, wickdocs.Docs{},
				),
				connector.Op(
					"get_content",
					"Get Page Content",
					"Open {url} and return its rendered content after JavaScript runs. Returns visible text by default, or the HTML when as_text is false. Scope to one element with selector.",
					getContentInput{},
					getContent, wickdocs.Docs{},
				),
				connector.Op(
					"pdf",
					"Render Page as PDF",
					"Open {url} and return the page rendered to PDF as base64. Chromium only; errors on firefox/webkit instances.",
					pdfInput{},
					renderPDF, wickdocs.Docs{},
				),
				connector.Op(
					"scrape",
					"Scrape Fields",
					"Open {url} and extract structured data: fields is a JSON map of result keys to CSS selectors, and each selector's inner text is returned under its key. A selector that matches nothing returns an empty string for that key.",
					scrapeInput{},
					scrape, wickdocs.Docs{},
				),
				connector.Op(
					"eval",
					"Evaluate JavaScript",
					"Open {url}, evaluate the given JavaScript expression in the page, and return its JSON-serialized result. Example script: document.querySelectorAll('a').length",
					evalInput{},
					evalJS, wickdocs.Docs{},
				),
			),
			connector.Cat(
				"Scripted flow",
				"Run a multi-step interaction in a single live browser session.",
				// Destructive: a script can click, submit forms, and mutate
				// remote state. Defaults off on every new instance so an admin
				// opts in per row.
				connector.OpDestructive(
					"run",
					"Run Script",
					"Execute an ordered list of browser actions (goto, click, fill, type, press, wait_for, wait, screenshot, content, eval, and more) in one session and return a result per step. Pass session_id to run against a persistent live session (kept open); omit it for a throwaway browser. The escape hatch for stateful flows the task ops can't express. Marked destructive because a script can submit forms and change remote state.",
					runInput{},
					run, wickdocs.Docs{},
				),
			),
			connector.Cat(
				"Live session",
				"Persistent browsers that survive across calls (and plugin restarts) until you close them. Open one, reuse it from run/screenshot/etc via session_id, inspect its tabs, then close it. Respects the max_live_sessions cap.",
				connector.OpDestructive(
					"session_open",
					"Open Live Session",
					"Launch a persistent browser and return its session_id. The browser stays alive across calls until session_close (or the host reboots). Pass the session_id to run/screenshot/scrape/etc to reuse the same live browser. Destructive because it holds an OS browser process open.",
					sessionOpenInput{},
					sessionOpen, wickdocs.Docs{},
				),
				connector.Op(
					"session_list",
					"List Live Sessions",
					"List every live session and its open tabs (index, url, title). Dead sessions are swept automatically. Use this to see what's currently open before reusing or closing a session.",
					sessionListInput{},
					sessionListOp, wickdocs.Docs{},
				),
				connector.Op(
					"tab_new",
					"Open Tab",
					"Open a new tab in a live session, optionally navigating it to {url}. Returns the new tab index.",
					tabNewInput{},
					tabNewOp, wickdocs.Docs{},
				),
				connector.OpDestructive(
					"tab_close",
					"Close Tab",
					"Close the tab at {index} in a live session. Get indices from session_list.",
					tabCloseInput{},
					tabCloseOp, wickdocs.Docs{},
				),
				connector.OpDestructive(
					"session_close",
					"Close Live Session",
					"Kill a live session's browser and free its resources. Always close sessions you opened — an abandoned session holds a browser process open until closed or host reboot.",
					sessionCloseInput{},
					sessionCloseOp, wickdocs.Docs{},
				),
			),
			connector.Cat(
				"Maintenance",
				"Inspect and download the browser engines. Backs the manager's browser picker; not meant for agent use — seed these AdminOnly.",
				connector.Op(
					"browser_status",
					"Browser Status",
					"Report which browser engines (chromium/firefox/webkit) are installed and their versions. Read-only; used by the manager UI's browser picker.",
					browserStatusInput{},
					browserStatusOp, wickdocs.Docs{},
				),
				connector.OpDestructive(
					"browser_install",
					"Install Browser",
					"Download one browser engine's binary (chromium/firefox/webkit). Blocks until the download completes. Idempotent. Used by the manager UI's Download button.",
					browserInstallInput{},
					browserInstallOp, wickdocs.Docs{},
				),
			),
		},
	}
}

// ── Operation handlers ───────────────────────────────────────────────
//
// Handlers stay thin: validate inputs via service.go, then hand off to
// repo.go, which owns the browser session. Each op runs inside a fresh
// session that withSession opens and closes.

func screenshot(c *connector.Ctx) (any, error) {
	in, err := parseScreenshot(c)
	if err != nil {
		return nil, err
	}
	return withSession(c, func(s *session) (any, error) { return s.screenshot(in) })
}

func getContent(c *connector.Ctx) (any, error) {
	in, err := parseGetContent(c)
	if err != nil {
		return nil, err
	}
	return withSession(c, func(s *session) (any, error) { return s.getContent(in) })
}

func renderPDF(c *connector.Ctx) (any, error) {
	in, err := parsePDF(c)
	if err != nil {
		return nil, err
	}
	return withSession(c, func(s *session) (any, error) { return s.pdf(in) })
}

func scrape(c *connector.Ctx) (any, error) {
	in, err := parseScrape(c)
	if err != nil {
		return nil, err
	}
	return withSession(c, func(s *session) (any, error) { return s.scrape(in) })
}

func evalJS(c *connector.Ctx) (any, error) {
	in, err := parseEval(c)
	if err != nil {
		return nil, err
	}
	return withSession(c, func(s *session) (any, error) { return s.eval(in) })
}

func run(c *connector.Ctx) (any, error) {
	actions, err := parseActions(c)
	if err != nil {
		return nil, err
	}
	// When a live session_id is supplied, run the actions in that persistent
	// browser (reconnect over CDP, don't close). Otherwise launch a throwaway
	// browser for this call only.
	if sid := strings.TrimSpace(c.Input("session_id")); sid != "" {
		lc, err := connectSession(c, sid)
		if err != nil {
			return nil, err
		}
		defer lc.close() // disconnect only — browser stays alive
		ctx, err := lc.firstContext()
		if err != nil {
			return nil, err
		}
		return runActionsInContext(c, ctx, actions)
	}
	return withSession(c, func(s *session) (any, error) { return s.runActions(actions) })
}

// ── Live session handlers ────────────────────────────────────────────

func sessionOpen(c *connector.Ctx) (any, error) { return openSession(c) }

func sessionListOp(c *connector.Ctx) (any, error) { return sessionList(c) }

func tabNewOp(c *connector.Ctx) (any, error) {
	sid := strings.TrimSpace(c.Input("session_id"))
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return tabNew(c, sid, strings.TrimSpace(c.Input("url")))
}

func tabCloseOp(c *connector.Ctx) (any, error) {
	sid := strings.TrimSpace(c.Input("session_id"))
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return tabClose(c, sid, c.InputInt("index"))
}

func sessionCloseOp(c *connector.Ctx) (any, error) {
	sid := strings.TrimSpace(c.Input("session_id"))
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return closeSession(c, sid)
}

// ── Maintenance handlers ─────────────────────────────────────────────

func browserStatusOp(c *connector.Ctx) (any, error)  { return browserStatus(c) }
func browserInstallOp(c *connector.Ctx) (any, error) { return browserInstall(c) }
