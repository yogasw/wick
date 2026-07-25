package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

// Network capture records the HTTP requests a browser makes during an operation,
// so they can be inspected or replayed (over plain HTTP, no browser) later.
//
// The decision to record is made UP FRONT via the record_request flag, not
// mid-run — so the listener is attached before any navigation and can't miss
// anything. For ephemeral ops (launch → act → close in one call) the whole
// browser context lives for that single call, so a context-level
// OnRequestFinished listener sees every request with no reconnect gap. Captured
// requests are saved to disk (under the connector's session dir) and read back
// with the get_request op.

// CapturedRequest is one recorded HTTP request/response, flattened to exactly
// what a replay needs: method, url, headers, cookies, body, and the response
// status (to filter successful calls). This is the DevTools "Copy as cURL" data.
type CapturedRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Cookies string            `json:"cookies"` // flattened Cookie header value
	Body    string            `json:"body,omitempty"`
	Status  int               `json:"status"` // response status; 0 if none
}

// capture accumulates requests seen during one recording. Playwright fires
// request events from an internal goroutine, so appends are mutex-guarded.
type capture struct {
	mu       sync.Mutex
	reqs     []CapturedRequest
	urlRE    *regexp.Regexp // optional filter; nil = keep all XHR/fetch-ish
	assets   bool           // include static assets (img/css/font/js) when true
	maxItems int
}

// newCapture builds a collector. urlPattern (optional) is a regex/substring the
// request URL must match to be kept; includeAssets keeps static assets that are
// otherwise skipped as noise.
func newCapture(urlPattern string, includeAssets bool) *capture {
	cp := &capture{assets: includeAssets, maxItems: 500}
	if p := strings.TrimSpace(urlPattern); p != "" {
		// Substring or regex — compile as regex, falling back to a literal match
		// via QuoteMeta if it isn't valid regex, so a plain "/api/x" always works.
		re, err := regexp.Compile(p)
		if err != nil {
			re = regexp.MustCompile(regexp.QuoteMeta(p))
		}
		cp.urlRE = re
	}
	return cp
}

// assetExtRE matches URLs that are almost certainly static assets, skipped by
// default because they're noise for replay (you want the XHR/fetch API calls).
var assetExtRE = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|ico|css|woff2?|ttf|otf|eot|mp4|webm|mp3|wav|js|map)(\?|$)`)

// add records one finished request if it passes the filters. Best-effort: any
// per-field error (e.g. PostData on a GET) is ignored so one odd request never
// breaks the whole capture.
func (cp *capture) add(req playwright.Request) {
	url := req.URL()
	if cp.urlRE != nil && !cp.urlRE.MatchString(url) {
		return
	}
	if !cp.assets && assetExtRE.MatchString(url) {
		return
	}

	headers, _ := req.AllHeaders()
	cookies := headers["cookie"]
	// The Cookie header is surfaced separately; drop it from Headers so callers
	// don't double-send it, and normalize away hop-by-hop noise we don't replay.
	delete(headers, "cookie")

	body := ""
	if pd, err := req.PostData(); err == nil {
		body = pd
	}

	status := 0
	if resp, err := req.Response(); err == nil && resp != nil {
		status = resp.Status()
	}

	cr := CapturedRequest{
		Method:  req.Method(),
		URL:     url,
		Headers: headers,
		Cookies: cookies,
		Body:    body,
		Status:  status,
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	if len(cp.reqs) >= cp.maxItems {
		return // cap to avoid unbounded memory on a chatty page
	}
	cp.reqs = append(cp.reqs, cr)
}

// snapshot returns a copy of the captured requests so far.
func (cp *capture) snapshot() []CapturedRequest {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	out := make([]CapturedRequest, len(cp.reqs))
	copy(out, cp.reqs)
	return out
}

// attach wires the collector to a browser context — a context-level listener
// covers every page/tab in that context, so multi-tab flows are captured too.
func (cp *capture) attach(bctx playwright.BrowserContext) {
	bctx.OnRequestFinished(cp.add)
}

// ── persistence ──────────────────────────────────────────────────────

// capturesDir is where non-profile captures are saved: <sessionDir>/captures.
func capturesDir(c *connector.Ctx) string {
	return filepath.Join(sessionDir(c), "captures")
}

// captureSavePath resolves where a capture is written. A named profile keeps its
// captures alongside its login (profiles/<name>/captured.json); otherwise they
// land in captures/<name>.json under the session dir.
func captureSavePath(c *connector.Ctx, profile, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "captured"
	}
	if !validProfileName(name) {
		return "", fmt.Errorf("invalid capture name %q: use letters, digits, dash, underscore", name)
	}
	if p := strings.TrimSpace(profile); p != "" {
		if !validProfileName(p) {
			return "", fmt.Errorf("invalid profile name %q", p)
		}
		return filepath.Join(sessionDir(c), profilePrefix+p, "captured.json"), nil
	}
	return filepath.Join(capturesDir(c), name+".json"), nil
}

// saveCapture writes the requests as pretty JSON, creating parent dirs.
func saveCapture(path string, reqs []CapturedRequest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create capture dir: %w", err)
	}
	b, err := json.MarshalIndent(reqs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// loadCapture reads previously saved requests back. Missing file → empty slice,
// not an error, so callers can poll before anything is recorded.
func loadCapture(path string) ([]CapturedRequest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var reqs []CapturedRequest
	if err := json.Unmarshal(b, &reqs); err != nil {
		return nil, fmt.Errorf("corrupt capture file %s: %w", path, err)
	}
	return reqs, nil
}
