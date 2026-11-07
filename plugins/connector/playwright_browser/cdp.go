package main

import (
	"encoding/json"
	"fmt"
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
	// Keep the payload within what a step may return.
	if b, err := json.Marshal(res); err == nil && len(b) > maxOutputRune {
		return map[string]any{"truncated": true, "json": clip(string(b))}, nil
	}
	return res, nil
}
