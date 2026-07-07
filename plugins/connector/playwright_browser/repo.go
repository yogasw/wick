package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

// ── Driver install guard ─────────────────────────────────────────────
//
// playwright-go ships a Node-based driver and downloads browser binaries on
// first use. We install lazily, guarded by installMu so:
//   - a host that has never installed gets a clear error the first time an op
//     runs, not a crash;
//   - concurrent calls don't race to install.
//
// The install is idempotent — if the driver + browsers are already present it
// returns fast — so once it succeeds we flip installed and never pay it again.
// A failure (e.g. no network on first use) is NOT cached: the next call retries,
// so a transient outage doesn't brick the plugin until the process restarts.

var (
	installMu sync.Mutex
	installed bool
)

// fallbackDownloadHost is tried when the default CDN (playwright.azureedge.net)
// fails to resolve/download. playwright-go only hits azureedge unless
// PLAYWRIGHT_DOWNLOAD_HOST is set, and that CDN is blocked or DNS-broken on some
// networks — cdn.playwright.dev is the current, reachable mirror.
const fallbackDownloadHost = "https://cdn.playwright.dev"

// ensureDriver runs the driver + browser install and returns a running
// Playwright handle. The handle is created per call (cheap) but the heavy
// download is attempted only until it succeeds once.
func ensureDriver() (*playwright.Playwright, error) {
	installMu.Lock()
	if !installed {
		if err := installDriver(); err != nil {
			installMu.Unlock()
			return nil, fmt.Errorf(
				"could not install the Playwright driver/browsers: %w\n"+
					"The host running this plugin needs Node.js and network access on first use. "+
					"If the default CDN is blocked, set PLAYWRIGHT_DOWNLOAD_HOST to a reachable mirror. "+
					"You can pre-install manually with: go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps",
				err)
		}
		installed = true
	}
	installMu.Unlock()

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("could not start Playwright: %w", err)
	}
	return pw, nil
}

// installDriver runs playwright.Install and, if it fails while the user has NOT
// pinned a download host, retries once against fallbackDownloadHost. A user-set
// PLAYWRIGHT_DOWNLOAD_HOST is always honored and never overridden.
func installDriver() error {
	err := playwright.Install()
	if err == nil {
		return nil
	}
	if os.Getenv("PLAYWRIGHT_DOWNLOAD_HOST") != "" {
		return err // user pinned a host; don't second-guess it.
	}
	if setErr := os.Setenv("PLAYWRIGHT_DOWNLOAD_HOST", fallbackDownloadHost); setErr != nil {
		return err
	}
	defer os.Unsetenv("PLAYWRIGHT_DOWNLOAD_HOST")
	if retryErr := playwright.Install(); retryErr != nil {
		return fmt.Errorf("default CDN failed (%v); fallback %s also failed: %w", err, fallbackDownloadHost, retryErr)
	}
	return nil
}

// ── Session lifecycle ────────────────────────────────────────────────

// session bundles a live Playwright run, browser, and context for one
// operation. It is created by withSession, used by exactly one goroutine, and
// torn down when withSession returns — no sharing across calls.
type session struct {
	c       *connector.Ctx
	pw      *playwright.Playwright
	browser playwright.Browser
	bctx    playwright.BrowserContext
	pages   int // pages opened so far, checked against maxTab
	maxTab  int
	actMS   float64
	navMS   float64
	chromium bool
}

// withSession launches an isolated browser per Config, runs fn against it, and
// guarantees teardown. Every task op and `run` goes through here, so lifecycle
// (and the maxTab / timeout wiring) lives in exactly one place.
func withSession(c *connector.Ctx, fn func(*session) (any, error)) (any, error) {
	pw, err := ensureDriver()
	if err != nil {
		return nil, err
	}
	defer pw.Stop()

	bt, err := browserType(pw, c.Cfg("browser"))
	if err != nil {
		return nil, err
	}
	browser, err := bt.Launch(launchOptions(c))
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	defer browser.Close()

	ctxOpts, err := contextOptions(pw, c)
	if err != nil {
		return nil, err
	}
	bctx, err := browser.NewContext(ctxOpts)
	if err != nil {
		return nil, fmt.Errorf("new browser context: %w", err)
	}
	defer bctx.Close()

	s := &session{
		c:        c,
		pw:       pw,
		browser:  browser,
		bctx:     bctx,
		maxTab:   maxTab(c),
		actMS:    actionTimeout(c),
		navMS:    navTimeout(c),
		chromium: strings.EqualFold(strings.TrimSpace(c.Cfg("browser")), "") || strings.EqualFold(c.Cfg("browser"), defBrowser),
	}
	return fn(s)
}

// newPage opens a page in the session context, enforcing the tab cap and
// wiring the configured action + navigation timeouts.
func (s *session) newPage() (playwright.Page, error) {
	if s.pages >= s.maxTab {
		return nil, fmt.Errorf("tab limit reached (max_tab=%d): the script tried to open more pages than allowed", s.maxTab)
	}
	page, err := s.bctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("new page: %w", err)
	}
	s.pages++
	page.SetDefaultTimeout(s.actMS)
	page.SetDefaultNavigationTimeout(s.navMS)
	return page, nil
}

// gotoURL opens a fresh page and navigates it to url. Shared by every task op.
func (s *session) gotoURL(url string) (playwright.Page, error) {
	page, err := s.newPage()
	if err != nil {
		return nil, err
	}
	if _, err := page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
	}); err != nil {
		return nil, fmt.Errorf("navigate to %s: %w", url, err)
	}
	return page, nil
}

// waitFor blocks until selector is present, when a wait_for was requested.
func waitFor(page playwright.Page, selector string) error {
	if selector == "" {
		return nil
	}
	if _, err := page.WaitForSelector(selector); err != nil {
		return fmt.Errorf("wait_for %q: %w", selector, err)
	}
	return nil
}

// ── Task op implementations ──────────────────────────────────────────

func (s *session) screenshot(in screenshotInput) (any, error) {
	page, err := s.gotoURL(in.URL)
	if err != nil {
		return nil, err
	}
	if err := waitFor(page, in.WaitFor); err != nil {
		return nil, err
	}

	var raw []byte
	if in.Selector != "" {
		el, err := page.WaitForSelector(in.Selector)
		if err != nil {
			return nil, fmt.Errorf("selector %q: %w", in.Selector, err)
		}
		raw, err = el.Screenshot()
		if err != nil {
			return nil, fmt.Errorf("element screenshot: %w", err)
		}
	} else {
		raw, err = page.Screenshot(playwright.PageScreenshotOptions{
			FullPage: playwright.Bool(in.FullPage),
			Type:     playwright.ScreenshotTypePng,
		})
		if err != nil {
			return nil, fmt.Errorf("screenshot: %w", err)
		}
	}
	return map[string]any{
		"url":       in.URL,
		"format":    "png",
		"full_page": in.FullPage && in.Selector == "",
		"image":     base64.StdEncoding.EncodeToString(raw),
	}, nil
}

func (s *session) getContent(in getContentInput) (any, error) {
	page, err := s.gotoURL(in.URL)
	if err != nil {
		return nil, err
	}
	if err := waitFor(page, in.WaitFor); err != nil {
		return nil, err
	}

	var out string
	switch {
	case in.Selector != "":
		el, err := page.WaitForSelector(in.Selector)
		if err != nil {
			return nil, fmt.Errorf("selector %q: %w", in.Selector, err)
		}
		if out, err = el.InnerText(); err != nil {
			return nil, fmt.Errorf("read element text: %w", err)
		}
	case in.AsText:
		if out, err = page.InnerText("body"); err != nil {
			return nil, fmt.Errorf("read body text: %w", err)
		}
	default:
		if out, err = page.Content(); err != nil {
			return nil, fmt.Errorf("read page HTML: %w", err)
		}
	}
	return map[string]any{
		"url":     in.URL,
		"as_text": in.AsText || in.Selector != "",
		"content": clip(out),
	}, nil
}

func (s *session) pdf(in pdfInput) (any, error) {
	if !s.chromium {
		return nil, fmt.Errorf("pdf is only supported on chromium instances; this instance uses %q", s.c.Cfg("browser"))
	}
	page, err := s.gotoURL(in.URL)
	if err != nil {
		return nil, err
	}
	if err := waitFor(page, in.WaitFor); err != nil {
		return nil, err
	}
	raw, err := page.PDF()
	if err != nil {
		return nil, fmt.Errorf("render pdf: %w", err)
	}
	return map[string]any{
		"url":    in.URL,
		"format": "pdf",
		"pdf":    base64.StdEncoding.EncodeToString(raw),
	}, nil
}

func (s *session) scrape(in scrapeInput) (any, error) {
	fields, err := parseFieldMap(in.Fields)
	if err != nil {
		return nil, err
	}
	page, err := s.gotoURL(in.URL)
	if err != nil {
		return nil, err
	}
	if err := waitFor(page, in.WaitFor); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(fields))
	for key, selector := range fields {
		// A missing selector yields an empty string rather than aborting the
		// whole scrape — partial results are more useful to the LLM than none.
		el, err := page.QuerySelector(selector)
		if err != nil || el == nil {
			out[key] = ""
			continue
		}
		txt, err := el.InnerText()
		if err != nil {
			out[key] = ""
			continue
		}
		out[key] = clip(strings.TrimSpace(txt))
	}
	return map[string]any{"url": in.URL, "fields": out}, nil
}

func (s *session) eval(in evalInput) (any, error) {
	page, err := s.gotoURL(in.URL)
	if err != nil {
		return nil, err
	}
	val, err := page.Evaluate(in.Script)
	if err != nil {
		return nil, fmt.Errorf("evaluate script: %w", err)
	}
	if str, ok := val.(string); ok {
		val = clip(str)
	}
	return map[string]any{"url": in.URL, "result": val}, nil
}

// ── run: scripted flow ───────────────────────────────────────────────

// stepResult is one entry in the `run` response — the step index, the action
// name, whether it succeeded, and any per-action output (screenshot base64,
// content text, eval result).
type stepResult struct {
	Step   int    `json:"step"`
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Output any    `json:"output,omitempty"`
}

// runActions executes the validated action list on a throwaway page in this
// (ephemeral) session.
func (s *session) runActions(actions []action) (any, error) {
	page, err := s.newPage()
	if err != nil {
		return nil, err
	}
	return runActionLoop(page, actions), nil
}

// runActionsInContext runs the action list on a NEW tab inside an existing live
// browser context (the session_id path). The tab is left open — it belongs to
// the persistent session and shows up in session_list.
func runActionsInContext(_ *connector.Ctx, ctx playwright.BrowserContext, actions []action) (any, error) {
	page, err := ctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("open tab in live session: %w", err)
	}
	return runActionLoop(page, actions), nil
}

// runActionLoop drives the validated action list in order on one page, stopping
// at the first failing step (later steps almost always depend on earlier ones,
// so continuing would produce misleading errors). Shared by the ephemeral and
// live-session paths.
func runActionLoop(page playwright.Page, actions []action) map[string]any {
	results := make([]stepResult, 0, len(actions))
	for i, a := range actions {
		out, err := execAction(page, a)
		res := stepResult{Step: i, Action: a.Action, OK: err == nil, Output: out}
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			return map[string]any{"steps": results, "completed": false, "failed_at": i}
		}
		results = append(results, res)
	}
	return map[string]any{"steps": results, "completed": true}
}

// execAction runs one action against the live page and returns its output.
func execAction(page playwright.Page, a action) (any, error) {
	switch a.Action {
	case "goto":
		if _, err := page.Goto(a.URL, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateLoad}); err != nil {
			return nil, fmt.Errorf("navigate to %s: %w", a.URL, err)
		}
		return nil, nil
	case "click":
		return nil, page.Click(a.Selector)
	case "fill":
		return nil, page.Fill(a.Selector, a.Value)
	case "type":
		return nil, page.Type(a.Selector, a.Value)
	case "press":
		// Playwright's Press needs a selector; default to the body so the LLM
		// can send global keys (Enter, Escape) without naming a target.
		selector := a.Selector
		if selector == "" {
			selector = "body"
		}
		return nil, page.Press(selector, a.Key)
	case "wait_for":
		_, err := page.WaitForSelector(a.Selector)
		return nil, err
	case "wait":
		page.WaitForTimeout(float64(a.MS))
		return nil, nil
	case "screenshot":
		var raw []byte
		var err error
		if a.Selector != "" {
			el, e := page.WaitForSelector(a.Selector)
			if e != nil {
				return nil, e
			}
			raw, err = el.Screenshot()
		} else {
			raw, err = page.Screenshot(playwright.PageScreenshotOptions{
				FullPage: playwright.Bool(a.FullPage),
				Type:     playwright.ScreenshotTypePng,
			})
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"format": "png", "image": base64.StdEncoding.EncodeToString(raw)}, nil
	case "content":
		if a.Selector != "" {
			el, err := page.WaitForSelector(a.Selector)
			if err != nil {
				return nil, err
			}
			txt, err := el.InnerText()
			if err != nil {
				return nil, err
			}
			return clip(txt), nil
		}
		html, err := page.Content()
		if err != nil {
			return nil, err
		}
		return clip(html), nil
	case "eval":
		val, err := page.Evaluate(a.Script)
		if err != nil {
			return nil, err
		}
		if str, ok := val.(string); ok {
			return clip(str), nil
		}
		return val, nil

	// ── navigation ──────────────────────────────────────────────────
	case "go_back":
		_, err := page.GoBack()
		return nil, err
	case "go_forward":
		_, err := page.GoForward()
		return nil, err
	case "reload":
		_, err := page.Reload()
		return nil, err
	case "wait_for_load_state":
		if st := loadState(a.State); st != nil {
			return nil, page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: st})
		}
		return nil, page.WaitForLoadState()
	case "wait_for_url":
		return nil, page.WaitForURL(a.URL)

	// ── interaction ─────────────────────────────────────────────────
	case "dblclick":
		return nil, page.Dblclick(a.Selector)
	case "hover":
		return nil, page.Hover(a.Selector)
	case "tap":
		return nil, page.Tap(a.Selector)
	case "focus":
		return nil, page.Focus(a.Selector)
	case "check":
		return nil, page.Check(a.Selector)
	case "uncheck":
		return nil, page.Uncheck(a.Selector)
	case "select_option":
		values := a.Values
		if len(values) == 0 {
			values = []string{a.Value}
		}
		selected, err := page.SelectOption(a.Selector, playwright.SelectOptionValues{ValuesOrLabels: &values})
		if err != nil {
			return nil, err
		}
		return map[string]any{"selected": selected}, nil
	case "set_input_files":
		return nil, page.SetInputFiles(a.Selector, a.Files)
	case "drag_and_drop":
		return nil, page.DragAndDrop(a.Selector, a.Target)
	case "scroll":
		return nil, page.Mouse().Wheel(a.DeltaX, a.DeltaY)

	// ── read / extract ──────────────────────────────────────────────
	case "get_attribute":
		val, err := page.GetAttribute(a.Selector, a.Attr)
		if err != nil {
			return nil, err
		}
		return clip(val), nil
	case "text_content":
		val, err := page.TextContent(a.Selector)
		if err != nil {
			return nil, err
		}
		return clip(val), nil
	case "inner_html":
		val, err := page.InnerHTML(a.Selector)
		if err != nil {
			return nil, err
		}
		return clip(val), nil
	case "is_visible":
		return page.IsVisible(a.Selector)
	case "is_checked":
		return page.IsChecked(a.Selector)
	case "count":
		return page.Locator(a.Selector).Count()
	case "title":
		return page.Title()
	case "url":
		return page.URL(), nil

	default:
		// Unreachable: parseActions rejects unknown actions up front.
		return nil, fmt.Errorf("unknown action %q", a.Action)
	}
}

// loadState maps the wait_for_load_state string to the Playwright enum, or nil
// when unset/unknown (WaitForLoadState then uses its default "load").
func loadState(s string) *playwright.LoadState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "domcontentloaded":
		return playwright.LoadStateDomcontentloaded
	case "networkidle":
		return playwright.LoadStateNetworkidle
	case "load":
		return playwright.LoadStateLoad
	default:
		return nil
	}
}
