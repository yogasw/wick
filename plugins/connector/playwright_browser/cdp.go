package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

// The cdp action sends one raw DevTools protocol command to the page, so a
// method an engine adds (chromium, the cloak engines and moli all speak CDP)
// is usable without a connector release. It reaches the page, not the
// browser: commands that act on the whole browser, open other targets, or
// read stored credentials are refused.

var cdpMethodRE = regexp.MustCompile(`^[A-Z][A-Za-z]*\.[a-z][A-Za-z]*$`)

// cdpBlockedDomains act on the browser process, other targets, the host or
// the profile's stored data.
var cdpBlockedDomains = map[string]string{
	"Browser":    "acts on the whole browser",
	"Target":     "opens or attaches to other targets",
	"Storage":    "reads or clears the profile's stored data",
	"SystemInfo": "reads the host",
	"Tethering":  "binds host ports",
	"Memory":     "reads process memory",
}

// cdpBlockedMethods are single commands in otherwise allowed domains.
var cdpBlockedMethods = map[string]string{
	"Network.getAllCookies":                "reads every cookie of the profile",
	"Network.getCookies":                   "reads cookies, which hold logins",
	"Network.clearBrowserCookies":          "wipes the profile's logins",
	"Network.clearBrowserCache":            "acts on the whole profile",
	"Security.setIgnoreCertificateErrors":  "turns off TLS checks",
	"Page.setDownloadBehavior":             "writes files on the host",
	"Page.close":                           "closes the tab under the run",
	"Page.crash":                           "crashes the tab",
	"Runtime.terminateExecution":           "stops the page's scripts",
	"Emulation.setScriptExecutionDisabled": "breaks the run's own scripts",
	"Input.setInterceptDrags":              "changes how the run's input works",
	"DOMStorage.getDOMStorageItems":        "reads stored site data, which can hold tokens",
	"IndexedDB.requestData":                "reads stored site data, which can hold tokens",
	"CacheStorage.requestEntries":          "reads stored site data",
	"ServiceWorker.deliverPushMessage":     "acts outside the page",
	"Fetch.continueWithAuth":               "answers auth challenges",
}

// cdpBlocked says why a method is refused, nil when it may run.
func cdpBlocked(method string) error {
	method = strings.TrimSpace(method)
	if !cdpMethodRE.MatchString(method) {
		return fmt.Errorf("method must look like Domain.command (e.g. Network.setExtraHTTPHeaders), got %q", method)
	}
	if why, ok := cdpBlockedMethods[method]; ok {
		return fmt.Errorf("%s is not allowed: it %s", method, why)
	}
	domain := method[:strings.IndexByte(method, '.')]
	if why, ok := cdpBlockedDomains[domain]; ok {
		return fmt.Errorf("the %s domain is not allowed: it %s", domain, why)
	}
	return nil
}

// cdpTimeout bounds one command: an engine that never answers must not hold
// the run until its deadline.
const cdpTimeout = 10 * time.Second

func runCDP(page playwright.Page, a action) (any, error) {
	if err := cdpBlocked(a.Method); err != nil {
		return nil, err
	}
	sess, err := page.Context().NewCDPSession(page)
	if err != nil {
		return nil, fmt.Errorf("open a CDP session: %w", err)
	}
	defer func() { _ = sess.Detach() }()
	var params map[string]any
	if len(a.Params) > 0 {
		params = a.Params
	}
	res, err := within(cdpTimeout, func() (any, error) { return sess.Send(a.Method, params) })
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Method, err)
	}
	// Binary payloads (Page.printToPDF, Page.captureScreenshot, …) come back
	// as base64 in "data": with save_dir they go to a file instead.
	if m, ok := res.(map[string]any); ok && a.SaveDir != "" {
		if data, ok := m["data"].(string); ok && data != "" {
			saved, err := saveCDPData(a.SaveDir, a.Method, data)
			if err != nil {
				return nil, err
			}
			m["data"] = saved
		}
	}
	// Keep the payload within what a step may return.
	if b, err := json.Marshal(res); err == nil && len(b) > maxOutputRune {
		return map[string]any{"truncated": true, "json": clip(string(b)),
			"note": "the result is larger than a step may return: for base64 data (a PDF, an image) pass save_dir to get a file instead"}, nil
	}
	return res, nil
}

// saveCDPData decodes a base64 payload into save_dir and returns its path.
// The extension follows the content: PDF, PNG, JPEG or .bin.
func saveCDPData(dir, method, data string) (map[string]any, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("%s: data is not base64, nothing saved: %w", method, err)
	}
	ext := ".bin"
	switch {
	case bytes.HasPrefix(raw, []byte("%PDF")):
		ext = ".pdf"
	case bytes.HasPrefix(raw, []byte("\x89PNG")):
		ext = ".png"
	case bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}):
		ext = ".jpg"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create save_dir: %w", err)
	}
	name := strings.ReplaceAll(method, ".", "-") + "-" + time.Now().Format("20060102-150405.000") + ext
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, fmt.Errorf("save %s: %w", method, err)
	}
	return map[string]any{"path": path, "bytes": len(raw)}, nil
}
