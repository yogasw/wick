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
	"time"

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
	SessionDir      string `wick:"group=Live sessions|Persistent-browser mode: where sessions are stored and how many may run.|collapsed;desc=Directory where live-session metadata, browser profiles, and downloaded engines (e.g. cloakbrowser) are stored. Live browsers survive plugin restarts via these files. Default: the plugin's persistent data dir under the app tree (~/.<app>/plugins/playwright_browser); set this only to override that location."`
	MaxLiveSessions int    `wick:"default=1;group=Live sessions;desc=Maximum persistent browsers alive at once (session_open cap). Guards RAM. Set 0 for unlimited. Default 1."`
	// MaxTabsPerSession caps tabs within one session. Default 1 (single-tab):
	// extra tabs each hold a live page in RAM, so multi-tab is opt-in. Raise it
	// to allow parallel tabs; 0 = unlimited. tab_new is rejected past the cap.
	MaxTabsPerSession int `wick:"default=1;group=Live sessions;desc=Maximum tabs per live session (tab_new cap). Each open tab costs RAM, so multi-tab is opt-in — default 1. Raise to allow parallel tabs; set 0 for unlimited."`
	// BrowserIdleTimeoutMin reclaims browsers nobody is using. A live session is
	// detached on purpose, so without this it only ends when session_close is
	// called — an agent that crashes or forgets leaves one resident forever.
	// Sessions on a NAMED profile get a proportionally longer leash, since
	// reaping those costs the user their logged-in state.
	BrowserIdleTimeoutMin int `wick:"number;default=1;group=Live sessions;desc=Minutes a live session may sit with no activity before it is closed automatically. Any op touching the session resets the clock. Sessions using a named profile get 8x this value, since closing them ends a logged-in session. Set -1 to never auto-close. Default 1."`
	// BrowserKillOrphans catches what the PID-based reap cannot: children an
	// engine forked on its own (Chrome renderers) and browsers whose recorded
	// PID no longer matches the running one (Firefox re-forks). Ownership is
	// decided by --user-data-dir, so a browser the user launched themselves is
	// never touched. Off by default — it enumerates every process on the host.
	BrowserKillOrphans bool `wick:"bool;group=Live sessions;desc=Also terminate browser processes under the session directory that no live session claims — orphaned children or re-forked PIDs the normal cleanup misses. Recommended on servers. Off by default."`
	// DefaultProfile / ForceDefaultProfile make a named profile the norm instead
	// of an opt-in. Without them every session_open with no profile arg gets a
	// throwaway dir that is swept on close, so a login never survives — the
	// common surprise. Setting DefaultProfile means "no profile arg" resolves to
	// this name; adding ForceDefaultProfile pins EVERY session to it.
	DefaultProfile      string `wick:"group=Live sessions;desc=Named profile used when session_open is called without a profile argument. Set this so logins persist by default instead of being swept on close. Letters, digits, dash, underscore only. Leave empty to keep anonymous throwaway sessions as the default."`
	ForceDefaultProfile bool   `wick:"bool;group=Live sessions;desc=Always use the default profile, ignoring any profile argument passed to session_open. Guarantees every session shares one identity. Requires a default profile to be set."`

	// Custom binary — rarely touched.
	ExecutablePath string `wick:"group=Custom binary|Point at a non-bundled browser build. Most setups leave these empty.|collapsed;desc=Path to a custom browser binary to launch instead of the bundled one. Example: /usr/bin/google-chrome"`
	Channel        string `wick:"group=Custom binary;desc=Branded channel for the chosen browser (chrome, chrome-beta, msedge, ...). Leave empty for the bundled build."`

	// CloakBrowser — stealth Chromium. Two engines share these configs: the free
	// "cloakbrowser" (wick downloads the binary from GitHub) and the pro
	// "cloakbrowser-pro" (binary managed by the official `cloakbrowser` CLI + a
	// license key). Only relevant when one of those engines is selected.
	CloakRepo           string `wick:"group=CloakBrowser|Stealth Chromium engine. The free tier downloads from a GitHub release; the pro tier is managed by the cloakbrowser CLI + a license key.|collapsed;desc=GitHub owner/repo hosting CloakBrowser release assets (free tier). Default: CloakHQ/CloakBrowser."`
	CloakExecutablePath string `wick:"group=CloakBrowser;desc=Path to an already-downloaded CloakBrowser binary. Set this to skip the GitHub download / CLI resolution (e.g. on a platform with no published build). Applies to both cloak engines."`
	// Pro-tier license: passed to the browser at launch, and used by the
	// cloakbrowser-pro engine to install/update the licensed binary via the CLI.
	CloakLicenseKey string `wick:"secret;group=CloakBrowser;desc=CloakBrowser Pro license key (cb_...). Passed as CLOAKBROWSER_LICENSE_KEY at launch so a paid binary runs at its licensed tier. Required by the cloakbrowser-pro engine; leave empty for the free cloakbrowser engine."`
	CloakCLIPath    string `wick:"group=CloakBrowser;desc=Path to the 'cloakbrowser' CLI used by the cloakbrowser-pro engine. Default: resolved on PATH. Only needed for a non-PATH install."`
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
	Tab       int    `wick:"desc=Which tab to act on when session_id is set (0-based, from session_list). Default 0 (first tab). Ignored without session_id."`
	// Network-capture opt-in. Decided up front (before the actions run) so the
	// recorder is attached before any navigation and misses nothing.
	RecordRequest       bool   `wick:"bool;desc=Record every HTTP request the browser makes while this script runs, so they can be inspected or replayed over plain HTTP later. Saved to disk; read back with get_request."`
	RecordName          string `wick:"desc=Name for the saved capture file (letters/digits/dash/underscore). Default 'captured'. When a profile is set, the capture is stored under that profile instead."`
	RecordURLPattern    string `wick:"desc=Optional substring or regex the request URL must match to be recorded. Leave empty to record all XHR/fetch calls (static assets are skipped)."`
	RecordIncludeAssets bool   `wick:"bool;desc=Also record static assets (images, CSS, fonts, JS). Off by default — those are usually noise for replay."`
	Profile             string `wick:"desc=Optional named profile to store the capture under (profiles/<name>/captured.json). Only used with record_request."`
}

// ── Live session inputs ──────────────────────────────────────────────

// sessionOpenInput opens a persistent browser. Browser/headless/proxy come from
// Config; the one optional arg is a named profile to reuse (login/cookies
// persist across sessions under that name) — empty means an anonymous,
// swept-on-close session, the original behavior.
type sessionOpenInput struct {
	Profile string `wick:"desc=Optional named profile to run this session against. A named profile's login/cookies persist across sessions and plugin restarts, so reopening the same name reuses the login without re-auth. Letters, digits, dash, underscore only. Leave empty to use the instance's default profile, or a throwaway anonymous session when no default is configured."`
}

// sessionListInput lists live sessions and their tabs. No arguments.
type sessionListInput struct{}

// profileListInput lists named persistent profiles. No arguments.
type profileListInput struct{}

// profileDeleteInput removes a named profile and its stored login/cookies.
type profileDeleteInput struct {
	Name string `wick:"required;desc=Named profile to delete (from profile_list). Refused while a live session is using it — close that session first."`
}

// getRequestInput reads back a previously recorded capture (from a run with
// record_request=true). Give the profile OR the capture name it was saved under.
type getRequestInput struct {
	Profile string `wick:"desc=Named profile the capture was stored under (profiles/<name>/captured.json). Leave empty to read a non-profile capture by name."`
	Name    string `wick:"desc=Capture name (default 'captured'). Used when no profile is set: reads captures/<name>.json."`
}

// sessionEndpointsInput returns a live session's raw CDP connection details
// (cdp_url + per-tab WebSocket debugger URLs) so the manager's live-browser
// panel can proxy a DevTools stream to it. Not for agent use.
type sessionEndpointsInput struct {
	SessionID string `wick:"required;desc=Live session id from session_open."`
}

// ── Extension inputs (manager UI, not for agent use) ─────────────────

type extensionListInput struct{}

type extensionInstallInput struct {
	ID   string `wick:"required;desc=Extension slug (folder name). Derived from the upload filename or the Web Store id."`
	Data string `wick:"required;desc=Base64 of the extension archive (.zip or .crx)."`
}

type extensionRemoveInput struct {
	ID string `wick:"required;desc=Extension slug to remove."`
}

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
	Browser string `wick:"dropdown=chromium|firefox|webkit|cloakbrowser|cloakbrowser-pro;required;desc=Engine to download."`
}

// Module returns the connector definition served over gRPC by main().
// pluginKey MUST equal the folder name: connector/playwright_browser/ →
// "playwright_browser". Underscore, not hyphen — a hyphen would break the
// <key>-<ver>-<os>-<arch>.zip split. It's also the sub-dir name under the app's
// plugins dir where this plugin's persistent data lives (see sessionDir).
const pluginKey = "playwright_browser"

func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         pluginKey,
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
				// Destructive: eval runs arbitrary JavaScript in the page, so it can
				// click, submit forms, and mutate remote state exactly like run.
				// Kept in the same privilege tier so an admin opts in per row.
				connector.OpDestructive(
					"eval",
					"Evaluate JavaScript",
					"Open {url}, evaluate the given JavaScript expression in the page, and return its JSON-serialized result. Example script: document.querySelectorAll('a').length. Marked destructive because arbitrary JavaScript can submit forms and change remote state.",
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
					"Launch a persistent browser and return its session_id. Pass that id to run/screenshot/scrape/etc to reuse the same live browser. Sessions auto-close after a period with no activity, and the number alive at once is capped — call session_list first to see what is already open, how long each has been idle, and how soon it frees itself. Reuse an existing session rather than opening a second one where you can. Destructive because it holds an OS browser process open.",
					sessionOpenInput{},
					sessionOpen, wickdocs.Docs{},
				),
				connector.Op(
					"session_list",
					"List Live Sessions",
					"List every live session with its open tabs (index, url, title), how long it has been idle (idle_seconds), and how long until it auto-closes (auto_close_in_seconds). Dead sessions are swept automatically. Call this before session_open to reuse an existing session, or to decide between closing one now and waiting for it to expire.",
					sessionListInput{},
					sessionListOp, wickdocs.Docs{},
				),
				// ConfigOnly: raw CDP plumbing for the manager's live-browser
				// panel. An agent has no use for a ws_debugger_url — it drives
				// the session through run/screenshot — so keep it off the tool
				// surface while the panel still reaches it via the /test path.
				connector.OpConfigOnly(
					"session_endpoints",
					"Live Session Endpoints",
					"Return a live session's raw CDP connection details: cdp_url plus one entry per open tab with its target_id and ws_debugger_url. Read-only; backs the manager's live-browser panel, which proxies a DevTools screencast/input stream to these endpoints.",
					sessionEndpointsInput{},
					sessionEndpointsOp, wickdocs.Docs{},
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
				connector.Op(
					"profile_list",
					"List Profiles",
					"List named persistent profiles (login/cookies that survive across sessions). Each entry has its name, created/last-used time, whether a live session is currently using it (live), and that session_id if so. Persistent — profiles with no running browser still appear. Open one with session_open(profile=<name>).",
					profileListInput{},
					profileListOp, wickdocs.Docs{},
				),
				connector.OpDestructive(
					"profile_delete",
					"Delete Profile",
					"Delete a named profile and its stored login/cookies for good. Refused while a live session is using the profile — close that session first. The only way a named profile is removed.",
					profileDeleteInput{},
					profileDeleteOp, wickdocs.Docs{},
				),
				connector.Op(
					"get_request",
					"Get Recorded Requests",
					"Read back the HTTP requests recorded by an earlier run with record_request=true. Returns each request's method, url, headers, cookies, body, and response status — ready to replay over plain HTTP. Give the profile it was stored under, or the capture name.",
					getRequestInput{},
					getRequestOp, wickdocs.Docs{},
				),
			),
			connector.Cat(
				"Extensions",
				"Chrome extensions loaded into live sessions. Backs the manager's Extensions section; not meant for agent use. Any installed extension forces new sessions headed (--load-extension needs a headed browser).",
				// ConfigOnly across the board: installing extensions is an admin
				// act performed in the manager UI, and it changes how EVERY later
				// session launches (headed, with --load-extension). An agent that
				// stumbled into these would silently reconfigure the connector for
				// everyone, so they stay off the tool surface while the manager
				// section still drives them via the /test path.
				connector.OpConfigOnly(
					"extension_list",
					"List Extensions",
					"List installed extensions (id, name, version, size). Read-only; backs the manager Extensions section.",
					extensionListInput{},
					extensionListOp, wickdocs.Docs{},
				),
				connector.OpConfigOnly(
					"extension_install",
					"Install Extension",
					"Unpack a base64 .zip/.crx into the connector's extensions dir. Applies to new sessions. Used by the manager upload / Web Store add.",
					extensionInstallInput{},
					extensionInstallOp, wickdocs.Docs{},
				),
				connector.OpConfigOnly(
					"extension_remove",
					"Remove Extension",
					"Delete an installed extension by id. Applies to new sessions.",
					extensionRemoveInput{},
					extensionRemoveOp, wickdocs.Docs{},
				),
			),
			connector.Cat(
				"Maintenance",
				"Inspect and download the browser engines. Backs the manager's browser picker; not meant for agent use — seed these AdminOnly.",
				// ConfigOnly: this op renders the manager's browser-picker
				// widget (html=browser_status) and returns HTML, not JSON.
				// Registering it as a normal Op leaked it onto the MCP tool
				// surface, where an agent calling it got an HTML document back.
				// OpConfigOnly hides it from wick_list/search/get while the
				// manager widget still runs it via the /test path.
				connector.OpConfigOnly(
					"browser_status",
					"Browser Status",
					"Report which browser engines (chromium/firefox/webkit) are installed and their versions. Read-only; used by the manager UI's browser picker.",
					browserStatusInput{},
					browserStatusOp, wickdocs.Docs{},
				),
				// ConfigOnly like its siblings: an agent has no reason to pull a
				// browser engine, and this blocks on a download that can run to
				// hundreds of megabytes. The manager's Download button drives it.
				connector.OpConfigOnly(
					"browser_install",
					"Install Browser",
					"Download one browser engine's binary (chromium/firefox/webkit). Blocks until the download completes. Idempotent. Used by the manager UI's Download button.",
					browserInstallInput{},
					browserInstallOp, wickdocs.Docs{},
				),
				connector.OpConfigOnly(
					"browser_update",
					"Update Browser",
					"Re-fetch a browser engine's binary to the newest available build (cloakbrowser pulls the latest GitHub release). Used by the manager UI's ⋮ menu.",
					browserInstallInput{},
					browserUpdateOp, wickdocs.Docs{},
				),
				connector.OpConfigOnly(
					"browser_uninstall",
					"Uninstall Browser",
					"Remove a browser engine's downloaded binary so it shows as not installed. Used by the manager UI's ⋮ menu.",
					browserInstallInput{},
					browserUninstallOp, wickdocs.Docs{},
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
		return runLive(c, sid, actions)
	}
	return withSession(c, func(s *session) (any, error) { return s.runActions(actions) })
}

// opDeadline is the hard ceiling on ONE browser op (ephemeral run/screenshot/…
// OR a live-session run). Playwright-go's operations (Launch, Goto,
// WaitForLoadState, ConnectOverCDP, pw.Stop) are blocking pipe calls into the
// Node driver and are NOT bound to any Go context. When the driver dies mid-call
// — e.g. it crashes with EPIPE writing a response — the pending call's result
// never arrives and the goroutine blocks in waitResult FOREVER. Playwright's own
// per-action timeout can't save it: that timer lives in the (now-dead) driver.
// This Go-level deadline is the only thing that guarantees the op returns, so the
// run finalizes (status leaves "running") instead of wedging as a zombie.
//
// Set above the per-action nav timeout (default 30s) plus margin so a legitimate
// slow navigation completes normally; only a truly wedged call trips it.
const opDeadline = 45 * time.Second

// withDeadline runs work on a worker goroutine and returns its result, or a
// timeout error if it doesn't finish within opDeadline. On timeout the worker is
// abandoned (a wedged driver call can't be force-killed from Go); it unblocks and
// exits when the driver process finally dies and its pipe closes. teardown, if
// set, is fired in the background on timeout to hasten that (e.g. force-close the
// connection so the pending call errors out). label names the op in the error.
func withDeadline(label string, teardown func(), work func() (any, error)) (any, error) {
	type outcome struct {
		res any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := work()
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(opDeadline):
		if teardown != nil {
			go teardown()
		}
		return nil, fmt.Errorf("%s exceeded %s (browser unresponsive; the driver may have crashed — close and reopen the session)", label, opDeadline)
	}
}

// runLive executes actions against a live session under the op deadline. The
// connect+run happens on a worker goroutine; if it doesn't finish in time,
// runLive returns a timeout error and force-disconnects in the background (a
// wedged pw.Stop must not block the return).
func runLive(c *connector.Ctx, sid string, actions []action) (any, error) {
	// lcCh hands the live connection to the timeout path so it can force a
	// teardown even while the worker is still blocked mid-run.
	lcCh := make(chan *liveConn, 1)
	teardown := func() {
		select {
		case lc := <-lcCh:
			lc.close()
		default:
		}
	}
	return withDeadline("live run", teardown, func() (any, error) {
		lc, err := connectSession(c, sid)
		if err != nil {
			return nil, err
		}
		lcCh <- lc
		bctx, err := lc.firstContext()
		if err != nil {
			lc.close()
			return nil, err
		}
		res, err := runActionsInContext(c, bctx, actions, c.InputInt("tab"))
		// Quiesce before disconnecting so the live-view screencast / in-flight CDP
		// events drain — disconnecting with a frame still queued is what crashed
		// the Node driver with EPIPE.
		lc.quiesce()
		lc.close() // disconnect only — the detached browser stays alive
		return res, err
	})
}

// ── Live session handlers ────────────────────────────────────────────

func sessionOpen(c *connector.Ctx) (any, error) { return openSession(c) }

func sessionListOp(c *connector.Ctx) (any, error) { return sessionList(c) }

func profileListOp(c *connector.Ctx) (any, error) { return profileList(c) }

func profileDeleteOp(c *connector.Ctx) (any, error) { return profileDelete(c) }

func getRequestOp(c *connector.Ctx) (any, error) {
	path, err := captureSavePath(c, c.Input("profile"), c.Input("name"))
	if err != nil {
		return nil, err
	}
	reqs, err := loadCapture(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"requests": reqs, "count": len(reqs)}, nil
}

func sessionEndpointsOp(c *connector.Ctx) (any, error) {
	sid := strings.TrimSpace(c.Input("session_id"))
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return sessionEndpoints(c, sid)
}

func extensionListOp(c *connector.Ctx) (any, error) { return extensionList(c) }

func extensionInstallOp(c *connector.Ctx) (any, error) {
	id := strings.TrimSpace(c.Input("id"))
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	return extensionInstall(c, id, c.Input("data"))
}

func extensionRemoveOp(c *connector.Ctx) (any, error) {
	id := strings.TrimSpace(c.Input("id"))
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	return extensionRemove(c, id)
}

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

func browserStatusOp(c *connector.Ctx) (any, error)    { return browserStatus(c) }
func browserInstallOp(c *connector.Ctx) (any, error)   { return browserInstall(c) }
func browserUpdateOp(c *connector.Ctx) (any, error)    { return browserUpdate(c) }
func browserUninstallOp(c *connector.Ctx) (any, error) { return browserUninstall(c) }
