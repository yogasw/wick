package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

// itoa is a tiny alias so the HTML builders read cleanly.
func itoa(n int) string { return strconv.Itoa(n) }

// Maintenance ops back the manager's browser-picker widget: report which
// engines are installed (+version) and download a missing one. They are NOT
// meant for the LLM — the connector seeds them AdminOnly so only the manager
// UI (via the /test admin path) drives them. They carry no destructive remote
// effect; the worst case is a large one-time download.

// browserEngines is the fixed set the widget lists. The cloak engines are the
// odd ones out — not Playwright-managed: cloakbrowser downloads from GitHub and
// cloakbrowser-pro is managed by the cloakbrowser CLI + a license key (cloak.go).
var browserEngines = []string{"chromium", "firefox", "webkit", cloakEngine, cloakProEngine}

// engineTitle is the display name for an engine row. strings.Title turns
// "cloakbrowser-pro" into "Cloakbrowser-Pro" which reads awkwardly, so the pro
// engine gets a hand-written label; the rest use strings.Title (ASCII names).
func engineTitle(name string) string {
	if isCloakPro(name) {
		return "Cloakbrowser Pro"
	}
	return strings.Title(name) //nolint:staticcheck // ASCII engine names only
}

// browserStatus renders the browser-picker widget as HTML for the manager's
// generic `html=` config widget. The CORE knows nothing about browsers — it
// just drops this markup in and wires any element carrying data-op / data-arg
// (see the html widget contract). So every bit of layout and per-row logic —
// the selected highlight, the Download button, the version line — lives HERE.
//
// Conventions the widget understands:
//   - data-op="__select" data-arg="<engine>"  → store <engine> as the value
//   - data-op="browser_install" data-arg="<engine>" → run the install op, then
//     re-fetch this HTML (so a finished download flips to "installed")
func browserStatus(c *connector.Ctx) (any, error) {
	pw, err := ensureDriverNoInstall()
	if err != nil {
		return nil, err
	}
	defer pw.Stop()

	// Highlight the engine the UI is CURRENTLY showing, not just the saved config:
	// the picker widget re-fetches this HTML with the just-clicked engine as the
	// `browser` INPUT before that choice is saved. Preferring the input makes the
	// highlight follow a click immediately instead of only after a save+refetch
	// (the "have to click twice / refresh" bug). Falls back to the saved config.
	selected := strings.ToLower(strings.TrimSpace(c.Input("browser")))
	if selected == "" {
		selected = strings.ToLower(strings.TrimSpace(c.Cfg("browser")))
	}

	var b strings.Builder
	b.WriteString(`<div class="flex flex-col gap-2">`)
	for _, name := range browserEngines {
		// A cloak download in flight gets a progress-bar row instead of the usual
		// installed/download row. Only the FREE engine has a progress file — it
		// downloads from GitHub in the background; the pro engine installs via the
		// CLI (blocking) and has no progress file to poll.
		if name == cloakEngine {
			if p, ok := readCloakProgress(c); ok && !p.Done {
				b.WriteString(renderProgressRow(name, p))
				continue
			}
		}
		installed, version := engineState(c, pw, name)
		updateAvail, latest := false, ""
		// Update-available detection only for the FREE cloak engine (wick's own
		// GitHub download): the GitHub tag check doesn't apply to the pro engine's
		// CLI-managed ~/.cloakbrowser binary — its ⋮ "update" just re-runs the CLI.
		if installed && name == cloakEngine {
			updateAvail, latest = cloakUpdateAvailable(c)
		}
		b.WriteString(renderEngineRow(name, version, installed, name == selected, updateAvail, latest))
	}
	b.WriteString(`</div>`)
	return map[string]any{"html": b.String()}, nil
}

// renderProgressRow shows a download/extract progress bar for an engine whose
// install is in flight. The widget keeps polling browser_status (see the
// data-installing marker) until the progress file flips to done.
func renderProgressRow(name string, p cloakProgress) string {
	title := engineTitle(name)
	phase := p.Phase
	if phase == "" {
		phase = "working"
	}
	// Determinate bar when we have a percentage (download with Content-Length);
	// otherwise a full-width striped bar to signal indeterminate progress.
	barInner := `<div class="h-full bg-green-500 transition-all" style="width:` + itoa(p.Pct) + `%"></div>`
	label := phase + " " + itoa(p.Pct) + "%"
	if p.Pct <= 0 {
		barInner = `<div class="h-full w-1/3 bg-green-500 animate-pulse"></div>`
		label = phase + "…"
	}
	return `<div data-installing="1" class="flex flex-col gap-1.5 rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2.5 bg-white-100 dark:bg-navy-800">` +
		`<div class="flex items-center gap-3">` +
		`<span class="font-semibold text-sm text-black-900 dark:text-white-100">` + title + `</span>` +
		`<span class="ml-auto text-[11px] text-black-700 dark:text-black-600">` + label + `</span>` +
		`</div>` +
		`<div class="h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">` + barInner + `</div>` +
		`</div>`
}

// versionFromPath reads a Playwright engine's version from its install path
// WITHOUT launching the browser. The status widget re-renders on every refresh
// and polls every ~1.2s during a download; the old approach launched each engine
// headless to read Version(), which on Windows flashed a browser window and
// sometimes left the process running (visible pileup of stuck Chromium
// processes). The install dir is named "<engine>-<revision>" (e.g.
// chromium-1228), so the revision is right there in the path — enough to
// identify the build, zero launches, zero flicker.
func versionFromPath(path string) string {
	// Walk up to the "<engine>-<rev>" directory under the browsers root.
	dir := filepath.Dir(path)
	for i := 0; i < 6 && dir != "" && dir != string(filepath.Separator); i++ {
		base := filepath.Base(dir)
		if i, rev, ok := strings.Cut(base, "-"); ok && i != "" && rev != "" {
			// e.g. "chromium-1228" → "r1228"; keep it short + clearly a revision.
			return "r" + rev
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// engineState reports whether one engine is installed and, if so, its version.
// The free cloakbrowser is resolved via cloak.go (GitHub download); the pro
// engine via the cloakbrowser CLI (`info`); the rest via the Playwright-managed
// binary path.
func engineState(c *connector.Ctx, pw *playwright.Playwright, name string) (installed bool, version string) {
	if name == cloakEngine {
		// Free engine: wick's own GitHub download.
		return cloakInstalled(c), cloakVersion(c)
	}
	if name == cloakProEngine {
		// Pro engine: report what `cloakbrowser info` says (installed + version +
		// tier), so the row matches the CLI-managed binary and license tier. Without
		// a license key — or if the CLI can't be reached — treat it as not installed
		// so the row reads "needs license / not installed" instead of a false ready.
		if cloakLicenseKey(c) == "" {
			return false, ""
		}
		if info, err := cloakGetInfoCached(c); err == nil && info.Installed {
			v := info.Version
			if info.Tier != "" {
				v += " (" + info.Tier + ")"
			}
			return true, v
		}
		return false, ""
	}
	bt := browserTypeByName(pw, name)
	if bt == nil {
		return false, ""
	}
	path := bt.ExecutablePath()
	if path == "" || !fileExists(path) {
		return false, ""
	}
	return true, versionFromPath(path)
}

// renderEngineRow is one row of the picker: a selectable, highlighted card when
// installed; an un-selectable card with a Download button when not. Installed
// rows carry a ⋮ menu (Update when a newer build exists, Uninstall) so the row
// stays uncluttered — the actions hide behind the kebab instead of stacking
// buttons. updateAvail/latest drive the Update item (cloakbrowser only today).
func renderEngineRow(name, version string, installed, selected, updateAvail bool, latest string) string {
	title := engineTitle(name)

	// Card shell. Selected installed engine gets a green ring; others a plain
	// border. Installed rows are clickable (data-op=__select).
	ring := "border border-white-400 dark:border-navy-600"
	if selected && installed {
		ring = "border-2 border-green-500 ring-1 ring-green-200 dark:ring-green-800"
	}
	clickAttrs := ""
	cursor := "cursor-default"
	if installed {
		clickAttrs = ` data-op="__select" data-arg="` + name + `"`
		cursor = "cursor-pointer hover:border-green-400"
	}

	var right string
	switch {
	case selected && installed:
		right = `<span class="rounded-full bg-green-50 dark:bg-green-900 px-2 py-0.5 text-[11px] font-semibold text-green-700 dark:text-green-300">selected</span>`
	case installed:
		right = `<span class="text-[11px] text-black-700 dark:text-black-600">click to select</span>`
	default:
		right = `<button type="button" data-op="browser_install" data-arg="` + name +
			`" class="rounded-md bg-green-600 hover:bg-green-500 px-3 py-1 text-xs font-semibold text-white-100">Download</button>`
	}

	badge := `<span class="rounded-full bg-neg-100 px-1.5 py-0.5 text-[10px] font-semibold text-neg-400">not installed</span>`
	if installed {
		badge = `<span class="rounded-full bg-pos-100 px-1.5 py-0.5 text-[10px] font-semibold text-pos-400">installed</span>`
	}
	// An available update gets its own amber pill next to the "installed" badge
	// so it's noticeable without opening the menu.
	if updateAvail {
		badge += `<span class="rounded-full bg-cau-100 px-1.5 py-0.5 text-[10px] font-semibold text-cau-400" title="` + latest + ` available">update</span>`
	}
	sub := ""
	if version != "" {
		sub = `<span class="text-[11px] text-black-700 dark:text-black-600">v` + version + `</span>`
	}

	return `<div class="flex items-center gap-3 rounded-lg ` + ring + ` ` + cursor +
		` px-4 py-2.5 bg-white-100 dark:bg-navy-800 transition-colors"` + clickAttrs + `>` +
		`<span class="font-semibold text-sm text-black-900 dark:text-white-100">` + title + `</span>` +
		badge + sub +
		`<span class="ml-auto flex items-center gap-2">` + right + renderEngineMenu(name, installed, updateAvail, latest) + `</span>` +
		`</div>`
}

// renderEngineMenu is the ⋮ kebab for an installed engine: a native
// <details>/<summary> dropdown (no extra JS) holding Update (when newer) and
// Uninstall. Clicks inside stopPropagation so they don't also fire the row's
// "select" handler. Returns "" for a not-installed engine (nothing to manage).
func renderEngineMenu(name string, installed, updateAvail bool, latest string) string {
	if !installed {
		return ""
	}
	// Inline on* handlers don't run inside the widget's {@html} sink, so behavior
	// rides data-op + the FE's delegated handler instead:
	//   - the SUMMARY carries data-op="__menu" — a UI-only marker the FE ignores
	//     (it won't select the row), letting the native <details> toggle open;
	//   - each item carries a real data-op the delegated handler runs, then the
	//     op's refetch re-renders the row (closing the menu).
	item := func(op, label, cls string) string {
		return `<button type="button" data-op="` + op + `" data-arg="` + name +
			`" class="block w-full px-3 py-1.5 text-left text-xs ` + cls +
			` hover:bg-white-200 dark:hover:bg-navy-700">` + label + `</button>`
	}
	var items string
	if updateAvail {
		items += item("browser_update", "Update to "+latest, "text-cau-400 font-medium")
	} else {
		items += item("browser_update", "Reinstall / update", "text-black-800 dark:text-black-600")
	}
	items += item("browser_uninstall", "Uninstall", "text-neg-400")

	return `<details class="relative">` +
		`<summary data-op="__menu" class="flex h-7 w-7 cursor-pointer list-none items-center justify-center rounded-md text-black-700 hover:bg-white-300 dark:hover:bg-navy-600" title="More">⋮</summary>` +
		`<div class="absolute right-0 z-10 mt-1 w-44 overflow-hidden rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg">` +
		items +
		`</div>` +
		`</details>`
}

// browserInstall downloads one engine's browser binary. It blocks until the
// download finishes (the widget polls browser_status while it runs, since the
// manager transport has no progress stream). Idempotent — installing an already
// present engine returns fast.
func browserInstall(c *connector.Ctx) (any, error) {
	name := strings.ToLower(strings.TrimSpace(c.Input("browser")))
	if name == "" {
		return nil, fmt.Errorf("browser is required (chromium, firefox, webkit, cloakbrowser, or cloakbrowser-pro)")
	}
	if !isKnownEngine(name) {
		return nil, fmt.Errorf("unknown browser %q: use chromium, firefox, webkit, cloakbrowser, or cloakbrowser-pro", name)
	}
	// Pro engine: delegate to `cloakbrowser install` (downloads the licensed
	// tier's binary under ~/.cloakbrowser). Requires a license key. Blocks until
	// done, then refreshes the info cache so the row updates.
	if name == cloakProEngine {
		if cloakLicenseKey(c) == "" {
			return nil, fmt.Errorf("cloakbrowser-pro needs a license key — set cloak_license_key")
		}
		out, err := cloakCLIInstall(c)
		invalidateCloakInfo()
		if err != nil {
			return nil, fmt.Errorf("cloakbrowser install failed: %w\n%s", err, out)
		}
		return map[string]any{"browser": name, "installed": true, "note": "installed via cloakbrowser CLI"}, nil
	}
	// Free engine: cloakbrowser downloads ~200MB from GitHub — too long to block
	// the manager RPC. Kick it off in the background (with its OWN context, since
	// the call context dies when this returns) and report "started"; the widget
	// polls browser_status for the progress bar. Guard against a double-start.
	if name == cloakEngine {
		if cloakInstalled(c) {
			return map[string]any{"browser": name, "installed": true}, nil
		}
		if cloakInstalling(c) {
			return map[string]any{"browser": name, "started": true, "note": "already downloading"}, nil
		}
		startCloakInstall(c)
		return map[string]any{"browser": name, "started": true}, nil
	}
	// Install just this engine's browser (not all of them).
	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{name}}); err != nil {
		return nil, fmt.Errorf("install %s: %w", name, err)
	}
	return map[string]any{"browser": name, "installed": true}, nil
}

// browserUpdate re-fetches an engine's binary to the newest available build.
// For cloakbrowser this removes the cached download and re-installs (picking up
// the latest GitHub release) in the background, with the same progress bar as a
// fresh install. For the Playwright-managed engines the version is pinned by the
// bundled playwright-go, so "update" just re-runs Install to repair/refresh that
// pinned build — there's no newer version to fetch without upgrading the library.
func browserUpdate(c *connector.Ctx) (any, error) {
	name := strings.ToLower(strings.TrimSpace(c.Input("browser")))
	if !isKnownEngine(name) {
		return nil, fmt.Errorf("unknown browser %q", name)
	}
	if name == cloakProEngine {
		// Pro engine: `cloakbrowser update` fetches the newest binary for the tier.
		if cloakLicenseKey(c) == "" {
			return nil, fmt.Errorf("cloakbrowser-pro needs a license key — set cloak_license_key")
		}
		out, err := cloakCLIUpdate(c)
		invalidateCloakInfo()
		if err != nil {
			return nil, fmt.Errorf("cloakbrowser update failed: %w\n%s", err, out)
		}
		return map[string]any{"browser": name, "updated": true, "note": "updated via cloakbrowser CLI"}, nil
	}
	if name == cloakEngine {
		// Free engine: remove the cached download and re-install (picks up the
		// latest GitHub release) in the background, same progress bar as install.
		if cloakInstalling(c) {
			return map[string]any{"browser": name, "started": true, "note": "already downloading"}, nil
		}
		if err := cloakUninstall(c); err != nil {
			return nil, err
		}
		startCloakInstall(c)
		return map[string]any{"browser": name, "started": true, "note": "updating to latest"}, nil
	}
	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{name}}); err != nil {
		return nil, fmt.Errorf("update %s: %w", name, err)
	}
	return map[string]any{"browser": name, "updated": true}, nil
}

// browserUninstall removes an engine's binary so the row flips to "not
// installed". cloakbrowser removes its managed cache dir; the Playwright engines
// remove the specific browser's install directory (playwright-go has no
// per-engine uninstall API, so we delete the resolved binary's folder).
func browserUninstall(c *connector.Ctx) (any, error) {
	name := strings.ToLower(strings.TrimSpace(c.Input("browser")))
	if !isKnownEngine(name) {
		return nil, fmt.Errorf("unknown browser %q", name)
	}
	if name == cloakProEngine {
		// Pro engine: the CLI owns the binary — clear its cache instead of deleting
		// wick's own download dir.
		out, err := runCloakCLI(c, 2*time.Minute, "clear-cache")
		invalidateCloakInfo()
		if err != nil {
			return nil, fmt.Errorf("cloakbrowser clear-cache failed: %w\n%s", err, out)
		}
		return map[string]any{"browser": name, "uninstalled": true, "note": "cleared via cloakbrowser CLI"}, nil
	}
	if name == cloakEngine {
		if err := cloakUninstall(c); err != nil {
			return nil, err
		}
		return map[string]any{"browser": name, "uninstalled": true}, nil
	}
	pw, err := ensureDriverNoInstall()
	if err != nil {
		return nil, err
	}
	defer pw.Stop()
	bt := browserTypeByName(pw, name)
	if bt == nil {
		return nil, fmt.Errorf("unknown engine %q", name)
	}
	path := bt.ExecutablePath()
	if path == "" || !fileExists(path) {
		return map[string]any{"browser": name, "uninstalled": true, "note": "was not installed"}, nil
	}
	// Playwright lays a browser out as <browsers-root>/<engine>-<rev>/... — the
	// binary sits a few levels deep. Remove the engine-revision root (the dir
	// directly under the browsers root) so the whole build goes, not just the exe.
	if dir := playwrightEngineRoot(path); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return map[string]any{"browser": name, "uninstalled": true}, nil
}

// playwrightEngineRoot walks up from a browser executable path to the
// engine-revision directory that sits directly under the Playwright browsers
// root (…/ms-playwright/<engine>-<rev>/…). Returns "" if the layout is
// unrecognized, so the caller skips the delete rather than removing something
// unexpected.
func playwrightEngineRoot(exePath string) string {
	dir := filepath.Dir(exePath)
	for i := 0; i < 6 && dir != "" && dir != string(filepath.Separator); i++ {
		parent := filepath.Dir(dir)
		base := strings.ToLower(filepath.Base(parent))
		// The parent of the engine-rev dir is the browsers root, named
		// "ms-playwright" (or a custom PLAYWRIGHT_BROWSERS_PATH). We detect the
		// canonical name; on a custom root we bail (return "") to stay safe.
		if strings.Contains(base, "ms-playwright") || strings.Contains(base, "playwright") {
			return dir
		}
		dir = parent
	}
	return ""
}

// ── helpers ──────────────────────────────────────────────────────────

// ensureDriverNoInstall starts a Playwright handle WITHOUT triggering the
// browser download. browser_status must not download — it only inspects — so it
// skips the install guard and only needs the (already-present) node driver. If
// the driver itself is missing it falls back to the full ensureDriver so the
// first status call still self-heals the driver (not the browsers).
func ensureDriverNoInstall() (*playwright.Playwright, error) {
	if pw, err := playwright.Run(); err == nil {
		return pw, nil
	}
	// Driver not present yet — install just the driver (skip browsers), then run.
	if err := playwright.Install(&playwright.RunOptions{SkipInstallBrowsers: true}); err != nil {
		return nil, fmt.Errorf("install playwright driver: %w", err)
	}
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("start playwright: %w", err)
	}
	return pw, nil
}

// browserTypeByName maps an engine name to its BrowserType, or nil if unknown.
func browserTypeByName(pw *playwright.Playwright, name string) playwright.BrowserType {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chromium":
		return pw.Chromium
	case "firefox":
		return pw.Firefox
	case "webkit":
		return pw.WebKit
	default:
		return nil
	}
}

func isKnownEngine(name string) bool {
	for _, e := range browserEngines {
		if e == name {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
