package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

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

// browserEngines is the fixed set the widget lists. cloakbrowser is the odd one
// out — not a Playwright-managed engine; it downloads from GitHub (see cloak.go).
var browserEngines = []string{"chromium", "firefox", "webkit", cloakEngine}

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

	selected := strings.ToLower(strings.TrimSpace(c.Cfg("browser")))

	var b strings.Builder
	b.WriteString(`<div class="flex flex-col gap-2">`)
	for _, name := range browserEngines {
		// A cloak download in flight gets a progress-bar row instead of the
		// usual installed/download row.
		if name == cloakEngine {
			if p, ok := readCloakProgress(c); ok && !p.Done {
				b.WriteString(renderProgressRow(name, p))
				continue
			}
		}
		installed, version := engineState(c, pw, name)
		b.WriteString(renderEngineRow(name, version, installed, name == selected))
	}
	b.WriteString(`</div>`)
	return map[string]any{"html": b.String()}, nil
}

// renderProgressRow shows a download/extract progress bar for an engine whose
// install is in flight. The widget keeps polling browser_status (see the
// data-installing marker) until the progress file flips to done.
func renderProgressRow(name string, p cloakProgress) string {
	title := strings.Title(name) //nolint:staticcheck // ASCII engine names only
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

// versionCache memoizes probeVersion by executable path. probeVersion LAUNCHES
// the browser headless just to read its version — ~1s per engine. The picker
// polls browser_status every ~1.2s while a download runs, so re-probing 3
// browsers each tick meant 3-4s per poll and a visibly stuttering UI. A
// browser's version can't change without its binary changing, so caching by
// path is safe and makes every poll after the first near-instant.
var (
	versionMu    sync.Mutex
	versionCache = map[string]string{}
)

func cachedProbeVersion(bt playwright.BrowserType, path string) string {
	versionMu.Lock()
	if v, ok := versionCache[path]; ok {
		versionMu.Unlock()
		return v
	}
	versionMu.Unlock()

	v := probeVersion(bt)

	versionMu.Lock()
	versionCache[path] = v
	versionMu.Unlock()
	return v
}

// engineState reports whether one engine is installed and, if so, its version.
// cloakbrowser is resolved via cloak.go (GitHub download); the rest via the
// Playwright-managed binary path.
func engineState(c *connector.Ctx, pw *playwright.Playwright, name string) (installed bool, version string) {
	if name == cloakEngine {
		return cloakInstalled(c), cloakVersion(c)
	}
	bt := browserTypeByName(pw, name)
	if bt == nil {
		return false, ""
	}
	path := bt.ExecutablePath()
	if path == "" || !fileExists(path) {
		return false, ""
	}
	return true, cachedProbeVersion(bt, path)
}

// renderEngineRow is one row of the picker: a selectable, highlighted card when
// installed; an un-selectable card with a Download button when not.
func renderEngineRow(name, version string, installed, selected bool) string {
	title := strings.Title(name) //nolint:staticcheck // ASCII engine names only

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
	sub := ""
	if version != "" {
		sub = `<span class="text-[11px] text-black-700 dark:text-black-600">v` + version + `</span>`
	}

	return `<div class="flex items-center gap-3 rounded-lg ` + ring + ` ` + cursor +
		` px-4 py-2.5 bg-white-100 dark:bg-navy-800 transition-colors"` + clickAttrs + `>` +
		`<span class="font-semibold text-sm text-black-900 dark:text-white-100">` + title + `</span>` +
		badge + sub +
		`<span class="ml-auto flex items-center gap-2">` + right + `</span>` +
		`</div>`
}

// browserInstall downloads one engine's browser binary. It blocks until the
// download finishes (the widget polls browser_status while it runs, since the
// manager transport has no progress stream). Idempotent — installing an already
// present engine returns fast.
func browserInstall(c *connector.Ctx) (any, error) {
	name := strings.ToLower(strings.TrimSpace(c.Input("browser")))
	if name == "" {
		return nil, fmt.Errorf("browser is required (chromium, firefox, webkit, or cloakbrowser)")
	}
	if !isKnownEngine(name) {
		return nil, fmt.Errorf("unknown browser %q: use chromium, firefox, webkit, or cloakbrowser", name)
	}
	// cloakbrowser downloads ~200MB from GitHub — too long to block the manager
	// RPC. Kick it off in the background (with its OWN context, since the call
	// context dies when this returns) and report "started"; the widget polls
	// browser_status for the progress bar. Guard against a double-start.
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

// probeVersion launches the browser headless just long enough to read its
// version string, then closes it. Best-effort: returns "" on any error so a
// present-but-unlaunchable browser still reports installed.
func probeVersion(bt playwright.BrowserType) string {
	b, err := bt.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		return ""
	}
	defer b.Close()
	return b.Version()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
