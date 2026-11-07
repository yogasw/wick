package main

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	in := screenshotInput{
		URL:      url,
		FullPage: c.InputBool("full_page"),
		Selector: strings.TrimSpace(c.Input("selector")),
		WaitFor:  strings.TrimSpace(c.Input("wait_for")),
		Clip:     c.Input("clip"),
		Type:     strings.ToLower(strings.TrimSpace(c.Input("type"))),
		Quality:  c.InputInt("quality"),
		SaveDir:  strings.TrimSpace(c.Input("save_dir")),
	}
	if _, err := in.shotOpts(); err != nil {
		return screenshotInput{}, err
	}
	if err := checkSaveDir(in.SaveDir); err != nil {
		return screenshotInput{}, err
	}
	return in, nil
}

// shotOpts turns the op's flat clip/type/quality inputs into shotOpts and
// validates them against the selector.
func (in screenshotInput) shotOpts() (shotOpts, error) {
	clip, err := parseClip(in.Clip)
	if err != nil {
		return shotOpts{}, err
	}
	o := shotOpts{Clip: clip, Format: in.Type, Quality: in.Quality}
	return o, o.validate(in.Selector)
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

func parseGetMarkdown(c *connector.Ctx) (getMarkdownInput, error) {
	if !isMoliEngine(c.Cfg("browser")) {
		return getMarkdownInput{}, fmt.Errorf("get_markdown needs browser=moli: it runs `moli fetch`, which the %q engine does not have; use get_content instead", c.Cfg("browser"))
	}
	url, err := requireURL(c)
	if err != nil {
		return getMarkdownInput{}, err
	}
	format := strings.TrimSpace(c.Input("format"))
	switch format {
	case "":
		format = "markdown"
	case "markdown", "semantic_tree_text":
	default:
		return getMarkdownInput{}, fmt.Errorf("format must be markdown or semantic_tree_text, got %q", format)
	}
	return getMarkdownInput{URL: url, Format: format, WaitSelector: strings.TrimSpace(c.Input("wait_selector"))}, nil
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
	// mouse_move / mouse_click / mouse_wheel: viewport coordinates, so a click
	// lands on whatever is painted there — cross-origin iframes included.
	X       *float64 `json:"x,omitempty"`
	Y       *float64 `json:"y,omitempty"`
	Steps   int      `json:"steps,omitempty"`    // path points (move) / wheel ticks
	Button  string   `json:"button,omitempty"`   // left|right|middle
	DelayMS int      `json:"delay_ms,omitempty"` // press-to-release hold (click)
	// HoverDwellMS is the pause on the target before pressing (click).
	HoverDwellMS int `json:"hover_dwell_ms,omitempty"`
	// frame_*: which frame to act in (url substring or index into page.Frames()).
	FrameURLContains string `json:"frame_url_contains,omitempty"`
	FrameIndex       *int   `json:"frame_index,omitempty"`
	Text             string `json:"text,omitempty"` // frame_click by visible text
	// Nth picks which visible match frame_click / frame_type use: 0 = first
	// (default), 1 = second, -1 = last, -2 = second to last.
	Nth *int `json:"nth,omitempty"`
	// Warning is set by parseActions when it adjusted the step (a clamped
	// timeout); it is reported on the step result, not read from input.
	Warning string `json:"-"`
	// Frame is an alias of frame_url_contains; "main" means the page itself.
	Frame string `json:"frame,omitempty"`
	// frame_click: Menu is opened first when the target is not on screen
	// yet; ExpectURL is the address the click must lead to.
	Menu      string `json:"menu,omitempty"`
	ExpectURL string `json:"expect_url,omitempty"`
	// pinned is the exact match openMenu found; locate returns it as is.
	pinned playwright.Locator
	// frame_type / frame_fill: [min,max] ms between keys.
	PerCharDelayMS []int `json:"per_char_delay_ms,omitempty"`
	// frame_text / observe size knobs.
	MaxItems        int  `json:"max_items,omitempty"`
	OnlyInteractive bool `json:"only_interactive,omitempty"`
	OnlyAdded       bool `json:"only_added,omitempty"`
	Compact         bool `json:"compact,omitempty"`
	// wait_for_text: Fresh ignores matches already on screen at the start;
	// IncludeValues (also wait_for_change / observe) adds field values.
	Fresh bool `json:"fresh,omitempty"`
	// Baseline picks what fresh compares against: trigger (default),
	// run_start or call_start.
	Baseline      string `json:"baseline,omitempty"`
	IncludeValues bool   `json:"include_values,omitempty"`
	// wait_for_text / wait_for_change / observe.
	Pattern       string `json:"pattern,omitempty"` // regex (wait_for_text)
	TimeoutMS     int    `json:"timeout_ms,omitempty"`
	UntilStableMS int    `json:"until_stable_ms,omitempty"` // observe
	// set_viewport.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// SaveDir writes a screenshot to this directory and returns its path
	// instead of base64.
	SaveDir string `json:"save_dir,omitempty"`
	// screenshot framing + encoding.
	Clip    *clipRect `json:"clip,omitempty"`
	Type    string    `json:"type,omitempty"` // png|jpeg
	Quality int       `json:"quality,omitempty"`
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
	"mouse_move": true, "mouse_click": true, "mouse_wheel": true,
	"frame_eval": true, "frame_text": true, "frame_click": true,
	"set_viewport": true, "frame_type": true, "frame_fill": true,
	"wait_for_text": true, "wait_for_change": true, "observe": true,
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
		clampWait(&actions[i])
		if err := validateActionArgs(i, actions[i]); err != nil {
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
	case "wait_for_text", "wait_for_change", "observe":
		if a.Action == "wait_for_text" && a.Text == "" && a.Pattern == "" {
			return fmt.Errorf("action[%d] wait_for_text: text or pattern is required", i)
		}
		if a.Pattern != "" {
			if _, err := regexp.Compile(a.Pattern); err != nil {
				return fmt.Errorf("action[%d] %s: pattern: %w", i, a.Action, err)
			}
		}
		if a.TimeoutMS < 0 || a.TimeoutMS > maxWaitMS {
			return fmt.Errorf("action[%d] %s: timeout_ms must be between 0 and %d", i, a.Action, maxWaitMS)
		}
		switch a.Baseline {
		case "", "trigger", "run_start", "call_start":
		default:
			return fmt.Errorf("action[%d] %s: baseline must be trigger, run_start or call_start", i, a.Action)
		}
		if a.UntilStableMS < 0 || a.UntilStableMS > maxWaitMS {
			return fmt.Errorf("action[%d] %s: until_stable_ms must be between 0 and %d", i, a.Action, maxWaitMS)
		}
	case "set_viewport":
		if a.Width < 200 || a.Height < 200 || a.Width > 7680 || a.Height > 4320 {
			return fmt.Errorf("action[%d] set_viewport: width and height must be between 200 and 7680x4320", i)
		}
	case "frame_type", "frame_fill":
		if strings.TrimSpace(a.Selector) == "" && strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("action[%d] %s: selector or text is required", i, a.Action)
		}
		if a.Value == "" {
			return fmt.Errorf("action[%d] %s: value is required", i, a.Action)
		}
		if d := a.PerCharDelayMS; d != nil && (len(d) != 2 || d[0] < 0 || d[1] < d[0] || d[1] > 2000) {
			return fmt.Errorf("action[%d] %s: per_char_delay_ms must be [min,max] with 0 <= min <= max <= 2000", i, a.Action)
		}
	case "frame_eval":
		return need("script", a.Script)
	case "frame_click":
		if strings.TrimSpace(a.Selector) == "" && strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("action[%d] frame_click: selector or text is required", i)
		}
		if a.HoverDwellMS < 0 || a.HoverDwellMS > maxClickDelayMS {
			return fmt.Errorf("action[%d] frame_click: hover_dwell_ms must be between 0 and %d", i, maxClickDelayMS)
		}
	case "screenshot":
		if err := a.shotOpts().validate(a.Selector); err != nil {
			return fmt.Errorf("action[%d] screenshot: %w", i, err)
		}
		if err := checkSaveDir(a.SaveDir); err != nil {
			return fmt.Errorf("action[%d] screenshot: %w", i, err)
		}
	case "press":
		return need("key", a.Key)
	case "eval":
		return need("script", a.Script)
	case "wait":
		if a.MS <= 0 {
			return fmt.Errorf("action[%d] wait: ms must be a positive integer", i)
		}
	case "scroll", "mouse_wheel":
		if a.DeltaX == 0 && a.DeltaY == 0 {
			return fmt.Errorf("action[%d] %s: delta_x or delta_y must be non-zero", i, a.Action)
		}
	case "mouse_move", "mouse_click":
		if a.X == nil || a.Y == nil {
			return fmt.Errorf("action[%d] %s: x and y are required (viewport pixels)", i, a.Action)
		}
		if *a.X < 0 || *a.Y < 0 {
			return fmt.Errorf("action[%d] %s: x and y must be >= 0", i, a.Action)
		}
		if a.Steps < 0 || a.Steps > maxMouseSteps {
			return fmt.Errorf("action[%d] %s: steps must be between 0 and %d", i, a.Action, maxMouseSteps)
		}
		if a.DelayMS < 0 || a.DelayMS > maxClickDelayMS {
			return fmt.Errorf("action[%d] %s: delay_ms must be between 0 and %d", i, a.Action, maxClickDelayMS)
		}
		if a.HoverDwellMS < 0 || a.HoverDwellMS > maxClickDelayMS {
			return fmt.Errorf("action[%d] %s: hover_dwell_ms must be between 0 and %d", i, a.Action, maxClickDelayMS)
		}
		switch a.Button {
		case "", "left", "right", "middle":
		default:
			return fmt.Errorf("action[%d] %s: button must be left, right or middle", i, a.Action)
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
	case cloakEngine, cloakProEngine:
		// CloakBrowser (free or pro) is patched Chromium — drive it via the
		// Chromium BrowserType with a custom ExecutablePath (set in launchOptions).
		return pw.Chromium, nil
	case moliEngine:
		// Moli speaks CDP, so Playwright attaches to it as Chromium
		// (ConnectOverCDP). It is never Launch()ed — see startMoli.
		return pw.Chromium, nil
	default:
		return nil, fmt.Errorf("unknown browser %q: use chromium, firefox, webkit, cloakbrowser, cloakbrowser-pro, or moli", cfg)
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
	engine := strings.TrimSpace(c.Cfg("browser"))
	isCloak := isCloakEngine(engine)
	if isCloak {
		// Free variant → the binary wick downloaded; Pro variant → the CLI-managed
		// (~/.cloakbrowser) binary resolved from `cloakbrowser info`.
		opts.ExecutablePath = playwright.String(resolveCloakBinary(c, engine))
		opts.IgnoreDefaultArgs = cloakLaunchArgs.IgnoreDefaultArgs
		opts.Args = cloakArgs(c)
		// Headless is left ENTIRELY to Playwright's own Headless flag (set above),
		// exactly like the cloakbrowser wrapper — it never adds a --headless flag
		// itself. Playwright's new-headless launch is what avoids the Windows
		// window flash, and a launch matrix confirmed plain Headless:true + the
		// stealth args navigates cleanly (adding --headless=old OR
		// --ignore-gpu-blocklist is what caused "target closed").
		// Pass the license key into the launched browser's env so a Pro binary
		// activates its licensed tier. Without it a Pro binary fails its license
		// check and exits immediately — Playwright then reports "target closed"
		// and the window flashes open-then-shut. Playwright's Env REPLACES the
		// process env, so cloakLaunchEnv merges the current env in.
		if env := cloakLaunchEnv(c); env != nil {
			opts.Env = env
		}
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
	// Log the resolved launch shape so a "blink" / flash is diagnosable: which
	// binary, headless flag, and args actually reach Chrome.
	if isCloak {
		exe := ""
		if opts.ExecutablePath != nil {
			exe = *opts.ExecutablePath
		}
		hl := false
		if opts.Headless != nil {
			hl = *opts.Headless
		}
		clog.Info().Str("engine", engine).Str("binary", exe).
			Bool("pw_headless", hl).Strs("args", opts.Args).
			Bool("has_license", cloakLaunchEnv(c) != nil).Msg("cloak: launch options resolved")
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
		applyDevice(&opts, desc, c.Cfg("browser"))
	} else if isCloakEngine(c.Cfg("browser")) {
		// CloakBrowser viewport handling mirrors the wrapper's
		// _resolve_context_viewport (browser.py):
		//   - headed → no_viewport (the page tracks the real window).
		//   - headless on a newer binary (>=148, e.g. Pro v150) → no_viewport too:
		//     it reports coherent screen dimensions natively, so an emulated
		//     viewport would make outerWidth < innerWidth (an impossible-window
		//     bot tell that makes the binary drop the page — "target closed").
		//   - headless on an OLDER binary (free v146, <148) → a fixed 1920x947
		//     viewport (DEFAULT_VIEWPORT) keeps dimensions coherent + deterministic;
		//     that binary does NOT report coherent headless dimensions on its own.
		// A user UA override is honored in all cases.
		if !headless(c) || cloakSupportsNoViewport(c) {
			opts.NoViewport = playwright.Bool(true)
		} else {
			opts.Viewport = &playwright.Size{Width: 1920, Height: 947}
		}
		if ua := strings.TrimSpace(c.Cfg("user_agent")); ua != "" {
			opts.UserAgent = playwright.String(ua)
		}
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

// applyDevice copies a device descriptor onto the context options. Moli
// rejects Emulation.setDeviceMetricsOverride with mobile=true, which would fail
// every page, so for it IsMobile is left unset and the rest still applies.
func applyDevice(opts *playwright.BrowserNewContextOptions, desc *playwright.DeviceDescriptor, engine string) {
	opts.UserAgent = playwright.String(desc.UserAgent)
	opts.Viewport = desc.Viewport
	opts.DeviceScaleFactor = playwright.Float(desc.DeviceScaleFactor)
	if !isMoliEngine(engine) {
		opts.IsMobile = playwright.Bool(desc.IsMobile)
	}
	opts.HasTouch = playwright.Bool(desc.HasTouch)
}

// headless resolves the headless config, defaulting to true when unset (the
// only sane default for a server-side connector).
func headless(c *connector.Ctx) bool {
	if v := strings.TrimSpace(c.Cfg("headless")); v == "" {
		return true
	}
	return c.CfgBool("headless")
}

// windowSize is the size a browser window is locked to: viewport_width x
// viewport_height from Config, 1280x800 when left unset. A live session opens
// at this size and every run puts the window back to it (or to the size a
// set_viewport chose), so the live view never swings between two shapes.
func windowSize(c *connector.Ctx) (int, int) {
	w, h := c.CfgInt("viewport_width"), c.CfgInt("viewport_height")
	if w <= 0 {
		w = defViewportW
	}
	if h <= 0 {
		h = defViewportH
	}
	return w, h
}

// setWindowSize resizes the REAL browser window over CDP. Unlike
// Page.SetViewportSize, which is a device-metrics override that lapses when the
// CDP connection closes (the page then falls back to the window's own size),
// the window keeps its size between runs, so the live view stays one shape. It
// does nothing when the window is already that size, and returns an error when
// the browser refuses (the caller falls back to the emulation).
func setWindowSize(ctx playwright.BrowserContext, page playwright.Page, w, h int) error {
	cdp, err := ctx.NewCDPSession(page)
	if err != nil {
		return err
	}
	defer func() { _ = cdp.Detach() }()
	res, err := cdp.Send("Browser.getWindowForTarget", nil)
	if err != nil {
		return err
	}
	m, _ := res.(map[string]any)
	id, ok := m["windowId"].(float64)
	if !ok {
		return fmt.Errorf("browser returned no window id")
	}
	if b, ok := m["bounds"].(map[string]any); ok {
		cw, _ := b["width"].(float64)
		ch, _ := b["height"].(float64)
		if state, _ := b["windowState"].(string); int(cw) == w && int(ch) == h && (state == "" || state == "normal") {
			return nil
		}
	}
	_, err = cdp.Send("Browser.setWindowBounds", map[string]any{
		"windowId": int(id),
		"bounds":   map[string]any{"windowState": "normal", "width": w, "height": h},
	})
	return err
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

// shotOpts reads a screenshot action's clip/type/quality.
func (a action) shotOpts() shotOpts {
	return shotOpts{Clip: a.Clip, Format: strings.ToLower(strings.TrimSpace(a.Type)), Quality: a.Quality}
}

// withFrameAlias folds the "frame" shorthand into frame_url_contains, and
// frame:"main" into frame_index 0 (the page itself).
func (a action) withFrameAlias() action {
	f := strings.TrimSpace(a.Frame)
	switch {
	case f == "":
	case strings.EqualFold(f, "main"):
		if a.FrameIndex == nil && a.FrameURLContains == "" {
			zero := 0
			a.FrameIndex = &zero
		}
	case a.FrameURLContains == "":
		a.FrameURLContains = f
	}
	return a
}

// clampWait caps timeout_ms and until_stable_ms at maxWaitMS instead of
// refusing the run: one over-long wait used to cancel every step before
// anything ran. The step still runs and carries a warning.
func clampWait(a *action) {
	var notes []string
	if a.TimeoutMS > maxWaitMS {
		notes = append(notes, fmt.Sprintf("timeout_ms %d clamped to %d", a.TimeoutMS, maxWaitMS))
		a.TimeoutMS = maxWaitMS
	}
	if a.UntilStableMS > maxWaitMS {
		notes = append(notes, fmt.Sprintf("until_stable_ms %d clamped to %d", a.UntilStableMS, maxWaitMS))
		a.UntilStableMS = maxWaitMS
	}
	a.Warning = strings.Join(notes, "; ")
}
