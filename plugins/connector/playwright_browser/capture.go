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
//
// Duplicates are collapsed by (method + URL + body): a page that re-fires the
// exact same request (SPA polling, a retried fetch) records it once, so the
// saved capture is a clean set of distinct calls to replay rather than N copies
// of the same poll. Two calls to the same endpoint with DIFFERENT bodies (e.g.
// paginated POSTs) are kept separately — the body is part of the identity.
type capture struct {
	mu       sync.Mutex
	reqs     []CapturedRequest
	seen     map[string]struct{} // dedup keys (method\x00url\x00body)
	urlRE    *regexp.Regexp      // optional filter; nil = keep all XHR/fetch-ish
	assets   bool                // include static assets (img/css/font/js) when true
	maxItems int
}

// newCapture builds a collector. urlPattern (optional) is a regex/substring the
// request URL must match to be kept; includeAssets keeps static assets that are
// otherwise skipped as noise.
func newCapture(urlPattern string, includeAssets bool) *capture {
	cp := &capture{assets: includeAssets, maxItems: 500, seen: make(map[string]struct{})}
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

// dedupKey identifies a request for duplicate collapsing: same method, URL, and
// body → same key. NUL-joined so no field's content can forge a boundary.
func dedupKey(method, url, body string) string {
	return method + "\x00" + url + "\x00" + body
}

// assetExtRE matches URLs that are almost certainly static assets, skipped by
// default because they're noise for replay (you want the XHR/fetch API calls).
var assetExtRE = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|ico|css|woff2?|ttf|otf|eot|mp4|webm|mp3|wav|js|map)(\?|$)`)

// add records one finished request if it passes the filters. It extracts the
// fields from the live Playwright Request, then hands off to addRecord for the
// (browser-free, unit-tested) filter/dedup/store decision.
//
// CRITICAL: this runs inside the driver's OnRequestFinished callback goroutine.
// It may ONLY touch data cached on the request initializer — URL, Method,
// Headers, PostData all read locally with no driver round-trip. It must NOT call
// Request.Response() or Request.AllHeaders(): those do a channel.Send back INTO
// the driver, and sending from within a driver-dispatched callback re-enters the
// pipe transport and crashes the Node driver with EPIPE (which wedged the whole
// run). Response status is therefore not captured here — method+url+headers+body
// is what a replay needs anyway; the status was never load-bearing.
func (cp *capture) add(req playwright.Request) {
	headers := req.Headers() // cached (provisional headers) — no driver round-trip
	cookies := headers["cookie"]
	// The Cookie header is surfaced separately; drop it from Headers so callers
	// don't double-send it.
	delete(headers, "cookie")

	body := ""
	if pd, err := req.PostData(); err == nil { // cached on initializer
		body = pd
	}

	cp.addRecord(CapturedRequest{
		Method:  req.Method(),
		URL:     req.URL(),
		Headers: headers,
		Cookies: cookies,
		Body:    body,
	})
}

// addRecord applies the keep/dedup/cap rules to one already-extracted request.
// Split from add so the recording logic is testable without a live browser:
//   - URL filter (record_url_pattern) — drop non-matching URLs.
//   - asset filter — drop static assets unless includeAssets.
//   - dedup — collapse identical (method+URL+body) requests to one.
//   - cap — stop at maxItems so a chatty page can't grow memory unbounded.
//
// Cross-domain requests are kept by design: the only URL gate is the caller's
// optional record_url_pattern, so a page that calls its own API plus a third
// party records both unless the caller narrowed the pattern.
func (cp *capture) addRecord(cr CapturedRequest) {
	if cp.urlRE != nil && !cp.urlRE.MatchString(cr.URL) {
		return
	}
	if !cp.assets && assetExtRE.MatchString(cr.URL) {
		return
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	if len(cp.reqs) >= cp.maxItems {
		return // cap to avoid unbounded memory on a chatty page
	}
	key := dedupKey(cr.Method, cr.URL, cr.Body)
	if _, dup := cp.seen[key]; dup {
		return // identical request already recorded
	}
	cp.seen[key] = struct{}{}
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

// detach removes the request listener from a browser context. This matters on
// the LIVE-session path (a CDP-attached, persistent context): the listener must
// be gone before the per-call connection is torn down, otherwise the driver can
// still fire a queued requestfinished event at a pipe that's closing and crash
// with EPIPE. On the ephemeral path the whole context dies with the call so a
// detach is harmless there too.
func (cp *capture) detach(bctx playwright.BrowserContext) {
	bctx.RemoveListener("requestfinished", cp.add)
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
