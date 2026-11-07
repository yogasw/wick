package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// launch_args passes Chromium command-line flags to a live session's browser,
// so a flag the browser adds is usable without a connector release. Chromium
// has thousands of flags and gains new ones every release, so this is a
// denylist: any --flag or --flag=value passes unless it touches what the
// connector owns (CDP port, profile dir, sandbox, headless, extensions,
// proxy/network, TLS, host files, helper processes).

const maxLaunchArgs = 20

// launchDenied are refused by exact name.
var launchDenied = map[string]bool{
	"--remote-debugging-port": true, "--remote-debugging-pipe": true, "--remote-debugging-address": true,
	"--remote-allow-origins": true, "--user-data-dir": true, "--profile-directory": true,
	"--no-sandbox": true, "--disable-setuid-sandbox": true, "--headless": true,
	"--load-extension": true, "--disable-extensions-except": true,
	"--disable-web-security": true, "--allow-file-access-from-files": true, "--allow-file-access": true,
	"--allow-running-insecure-content": true, "--unsafely-treat-insecure-origin-as-secure": true,
	"--host-rules": true, "--host-resolver-rules": true, "--enable-logging": true,
	"--js-flags": true, "--single-process": true, "--no-zygote": true, "--disable-gpu-sandbox": true,
}

// launchDeniedParts are refused anywhere in the flag name.
var launchDeniedParts = []string{
	"proxy", "certificate", "ssl", "-cmd-prefix", "-launcher", "-path", "-dir", "-file",
	"net-log", "log-net", "remote-debugging", "sandbox", "extension",
}

// parseLaunchArgs validates a JSON array of Chromium flags.
func parseLaunchArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("launch_args must be a JSON array of strings: %w", err)
	}
	if len(args) > maxLaunchArgs {
		return nil, fmt.Errorf("launch_args: at most %d flags", maxLaunchArgs)
	}
	out := make([]string, 0, len(args))
	for _, flag := range args {
		flag = strings.TrimSpace(flag)
		if !strings.HasPrefix(flag, "--") || len(flag) < 3 {
			return nil, fmt.Errorf("launch_args: %q is not a --flag (Chromium takes values as --flag=value)", flag)
		}
		name := strings.ToLower(flag)
		if eq := strings.IndexByte(name, '='); eq > 0 {
			name = name[:eq]
		}
		if launchDenied[name] {
			return nil, fmt.Errorf("launch_args: %s is set by the connector and cannot be passed", name)
		}
		for _, part := range launchDeniedParts {
			if strings.Contains(name, part) {
				return nil, fmt.Errorf("launch_args: %s is not allowed (flags touching %q stay under the connector's control)", name, part)
			}
		}
		out = append(out, flag)
	}
	return out, nil
}
