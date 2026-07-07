package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

// Defaults applied when a Config field is left at its zero value. Kept in one
// place so the values quoted in the `desc=` help text and the runtime match.
const (
	defViewportW  = 1280
	defViewportH  = 800
	defActionMS   = 5000
	defNavMS      = 30000
	defMaxTab     = 5
	defBrowser    = "chromium"
	maxOutputRune = 200000 // cap eval/content payloads so a huge page can't blow the gRPC frame
)

// ── Input parsing + validation (pure Go, no browser) ─────────────────

func parseScreenshot(c *connector.Ctx) (screenshotInput, error) {
	url, err := requireURL(c)
	if err != nil {
		return screenshotInput{}, err
	}
	return screenshotInput{
		URL:      url,
		FullPage: c.InputBool("full_page"),
		Selector: strings.TrimSpace(c.Input("selector")),
		WaitFor:  strings.TrimSpace(c.Input("wait_for")),
	}, nil
}

func parseGetContent(c *connector.Ctx) (getContentInput, error) {
	url, err := requireURL(c)
	if err != nil {
		return getContentInput{}, err
	}
	// as_text defaults to true when the field is absent (the LLM omitted it).
	asText := true
	if v := strings.TrimSpace(c.Input("as_text")); v != "" {
		asText = c.InputBool("as_text")
	}
	return getContentInput{
		URL:      url,
		Selector: strings.TrimSpace(c.Input("selector")),
		AsText:   asText,
		WaitFor:  strings.TrimSpace(c.Input("wait_for")),
	}, nil
}

func parsePDF(c *connector.Ctx) (pdfInput, error) {
	url, err := requireURL(c)
	if err != nil {
		return pdfInput{}, err
	}
	return pdfInput{URL: url, WaitFor: strings.TrimSpace(c.Input("wait_for"))}, nil
}

func parseScrape(c *connector.Ctx) (scrapeInput, error) {
	url, err := requireURL(c)
	if err != nil {
		return scrapeInput{}, err
	}
	fields := strings.TrimSpace(c.Input("fields"))
	if fields == "" {
		return scrapeInput{}, fmt.Errorf("fields is required: a JSON object mapping result keys to CSS selectors")
	}
	if _, err := parseFieldMap(fields); err != nil {
		return scrapeInput{}, err
	}
	return scrapeInput{URL: url, Fields: fields, WaitFor: strings.TrimSpace(c.Input("wait_for"))}, nil
}

func parseEval(c *connector.Ctx) (evalInput, error) {
	url, err := requireURL(c)
	if err != nil {
		return evalInput{}, err
	}
	script := strings.TrimSpace(c.Input("script"))
	if script == "" {
		return evalInput{}, fmt.Errorf("script is required: a JavaScript expression to evaluate in the page")
	}
	return evalInput{URL: url, Script: script}, nil
}

// requireURL validates the shared url input every task op takes.
func requireURL(c *connector.Ctx) (string, error) {
	url := strings.TrimSpace(c.Input("url"))
	if url == "" {
		return "", fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://, got %q", url)
	}
	return url, nil
}

// parseFieldMap decodes the scrape `fields` JSON into an ordered-agnostic map
// of resultKey → cssSelector. Shared by validation and execution.
func parseFieldMap(raw string) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("fields must be a JSON object of {\"key\":\"css selector\"}: %w", err)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("fields is empty: provide at least one {\"key\":\"selector\"} pair")
	}
	return m, nil
}

// ── run: action model ────────────────────────────────────────────────

// action is one step in a `run` script. Only the fields relevant to its
// Action are read; the rest stay zero. Kept flat (rather than a per-action
// type) so the LLM emits one predictable object shape.
type action struct {
	Action   string   `json:"action"`
	URL      string   `json:"url,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Target   string   `json:"target,omitempty"` // drag_and_drop destination selector
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"` // select_option (multi)
	Attr     string   `json:"attr,omitempty"`   // get_attribute name
	Key      string   `json:"key,omitempty"`
	Files    []string `json:"files,omitempty"` // set_input_files paths
	Script   string   `json:"script,omitempty"`
	State    string   `json:"state,omitempty"` // wait_for_load_state: load|domcontentloaded|networkidle
	FullPage bool     `json:"full_page,omitempty"`
	MS       int      `json:"ms,omitempty"`
	DeltaX   float64  `json:"delta_x,omitempty"` // scroll
	DeltaY   float64  `json:"delta_y,omitempty"` // scroll
}

// knownActions is the closed set `run` dispatches on. Validated up front so a
// typo fails before a browser is ever launched. Grouped by intent to match the
// op description.
var knownActions = map[string]bool{
	// navigation
	"goto": true, "go_back": true, "go_forward": true, "reload": true,
	"wait_for_load_state": true, "wait_for_url": true,
	// interaction
	"click": true, "dblclick": true, "hover": true, "tap": true,
	"fill": true, "type": true, "press": true, "focus": true,
	"check": true, "uncheck": true, "select_option": true,
	"set_input_files": true, "drag_and_drop": true, "scroll": true,
	// wait
	"wait_for": true, "wait": true,
	// read / extract
	"screenshot": true, "content": true, "eval": true,
	"get_attribute": true, "text_content": true, "inner_html": true,
	"is_visible": true, "is_checked": true, "count": true, "title": true, "url": true,
}

// parseActions decodes and validates the `run` action list without touching a
// browser: unknown action names, empty lists, and the required field per
// action are all caught here so the error is returned before launch.
func parseActions(c *connector.Ctx) ([]action, error) {
	raw := strings.TrimSpace(c.Input("actions"))
	if raw == "" {
		return nil, fmt.Errorf("actions is required: a JSON array of action objects")
	}
	var actions []action
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil, fmt.Errorf("actions must be a JSON array of action objects: %w", err)
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("actions is empty: provide at least one action")
	}
	for i, a := range actions {
		name := strings.TrimSpace(a.Action)
		if name == "" {
			return nil, fmt.Errorf("action[%d]: missing \"action\" key", i)
		}
		if !knownActions[name] {
			return nil, fmt.Errorf("action[%d]: unknown action %q", i, name)
		}
		if err := validateActionArgs(i, a); err != nil {
			return nil, err
		}
	}
	return actions, nil
}

// validateActionArgs checks the field each action requires is present.
func validateActionArgs(i int, a action) error {
	need := func(field, val string) error {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("action[%d] %s: %s is required", i, a.Action, field)
		}
		return nil
	}
	switch a.Action {
	case "goto", "wait_for_url":
		return need("url", a.URL)
	case "click", "dblclick", "hover", "tap", "wait_for", "focus",
		"check", "uncheck", "text_content", "inner_html", "is_visible",
		"is_checked", "count":
		return need("selector", a.Selector)
	case "fill", "type":
		if err := need("selector", a.Selector); err != nil {
			return err
		}
		return need("value", a.Value)
	case "select_option":
		if err := need("selector", a.Selector); err != nil {
			return err
		}
		if strings.TrimSpace(a.Value) == "" && len(a.Values) == 0 {
			return fmt.Errorf("action[%d] select_option: value or values is required", i)
		}
	case "get_attribute":
		if err := need("selector", a.Selector); err != nil {
			return err
		}
		return need("attr", a.Attr)
	case "set_input_files":
		if err := need("selector", a.Selector); err != nil {
			return err
		}
		if len(a.Files) == 0 {
			return fmt.Errorf("action[%d] set_input_files: files is required (array of paths)", i)
		}
	case "drag_and_drop":
		if err := need("selector", a.Selector); err != nil {
			return err
		}
		return need("target", a.Target)
	case "press":
		return need("key", a.Key)
	case "eval":
		return need("script", a.Script)
	case "wait":
		if a.MS <= 0 {
			return fmt.Errorf("action[%d] wait: ms must be a positive integer", i)
		}
	case "scroll":
		if a.DeltaX == 0 && a.DeltaY == 0 {
			return fmt.Errorf("action[%d] scroll: delta_x or delta_y must be non-zero", i)
		}
	}
	return nil
}

// ── Config → Playwright option builders (pure Go) ────────────────────

// browserType maps the configured browser string to the corresponding
// playwright BrowserType, defaulting to chromium.
func browserType(pw *playwright.Playwright, cfg string) (playwright.BrowserType, error) {
	switch strings.ToLower(strings.TrimSpace(cfg)) {
	case "", defBrowser:
		return pw.Chromium, nil
	case "firefox":
		return pw.Firefox, nil
	case "webkit":
		return pw.WebKit, nil
	case cloakEngine:
		// CloakBrowser is patched Chromium — drive it via the Chromium
		// BrowserType with a custom ExecutablePath (set in launchOptions).
		return pw.Chromium, nil
	default:
		return nil, fmt.Errorf("unknown browser %q: use chromium, firefox, webkit, or cloakbrowser", cfg)
	}
}

// launchOptions builds the BrowserType.Launch options from Config. For the
// cloakbrowser engine it points ExecutablePath at the downloaded stealth binary
// and applies the anti-automation flags (IgnoreDefaultArgs / --no-sandbox); the
// admin's explicit executable_path still wins if set.
func launchOptions(c *connector.Ctx) playwright.BrowserTypeLaunchOptions {
	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless(c)),
	}
	isCloak := strings.EqualFold(strings.TrimSpace(c.Cfg("browser")), cloakEngine)
	if isCloak {
		opts.ExecutablePath = playwright.String(cloakBinaryPath(c))
		opts.IgnoreDefaultArgs = cloakLaunchArgs.IgnoreDefaultArgs
		opts.Args = append([]string{}, cloakLaunchArgs.Args...)
	}
	if p := strings.TrimSpace(c.Cfg("executable_path")); p != "" {
		opts.ExecutablePath = playwright.String(p)
	}
	if ch := strings.TrimSpace(c.Cfg("channel")); ch != "" && !isCloak {
		opts.Channel = playwright.String(ch)
	}
	if px := strings.TrimSpace(c.Cfg("proxy_server")); px != "" {
		proxy := &playwright.Proxy{Server: px}
		if bp := strings.TrimSpace(c.Cfg("proxy_bypass")); bp != "" {
			proxy.Bypass = playwright.String(bp)
		}
		opts.Proxy = proxy
	}
	return opts
}

// contextOptions builds the BrowserContext options from Config: device
// emulation (which overrides viewport/UA), else explicit viewport + UA, plus a
// storage-state seed. pw is needed to resolve the device descriptor.
func contextOptions(pw *playwright.Playwright, c *connector.Ctx) (playwright.BrowserNewContextOptions, error) {
	opts := playwright.BrowserNewContextOptions{}

	if dev := strings.TrimSpace(c.Cfg("device")); dev != "" {
		desc, ok := pw.Devices[dev]
		if !ok {
			return opts, fmt.Errorf("unknown device %q: see the Playwright device registry (e.g. \"iPhone 15\", \"Pixel 7\")", dev)
		}
		opts.UserAgent = playwright.String(desc.UserAgent)
		opts.Viewport = desc.Viewport
		opts.DeviceScaleFactor = playwright.Float(desc.DeviceScaleFactor)
		opts.IsMobile = playwright.Bool(desc.IsMobile)
		opts.HasTouch = playwright.Bool(desc.HasTouch)
	} else {
		w := c.CfgInt("viewport_width")
		if w <= 0 {
			w = defViewportW
		}
		h := c.CfgInt("viewport_height")
		if h <= 0 {
			h = defViewportH
		}
		opts.Viewport = &playwright.Size{Width: w, Height: h}
		if ua := strings.TrimSpace(c.Cfg("user_agent")); ua != "" {
			opts.UserAgent = playwright.String(ua)
		}
	}

	if ss := strings.TrimSpace(c.Cfg("storage_state")); ss != "" {
		opts.StorageStatePath = playwright.String(ss)
	}
	return opts, nil
}

// headless resolves the headless config, defaulting to true when unset (the
// only sane default for a server-side connector).
func headless(c *connector.Ctx) bool {
	if v := strings.TrimSpace(c.Cfg("headless")); v == "" {
		return true
	}
	return c.CfgBool("headless")
}

// actionTimeout / navTimeout / maxTab read their config with the documented
// defaults substituted for a zero value.
func actionTimeout(c *connector.Ctx) float64 {
	if v := c.CfgInt("action_timeout_ms"); v > 0 {
		return float64(v)
	}
	return defActionMS
}

func navTimeout(c *connector.Ctx) float64 {
	if v := c.CfgInt("navigation_timeout_ms"); v > 0 {
		return float64(v)
	}
	return defNavMS
}

func maxTab(c *connector.Ctx) int {
	if v := c.CfgInt("max_tab"); v > 0 {
		return v
	}
	return defMaxTab
}

// clip caps a string result so a pathological page can't produce a payload too
// large for the plugin gRPC frame.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxOutputRune {
		return s
	}
	return string(r[:maxOutputRune]) + "…[truncated]"
}
