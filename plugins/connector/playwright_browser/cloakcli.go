package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/safeexec"
)

// cloakcli.go is the passthrough to the official `cloakbrowser` CLI. It backs
// the "cloakbrowser-pro" engine (see cloak.go's cloakProEngine): while the free
// "cloakbrowser" engine downloads its binary from GitHub (cloak.go), the pro
// engine delegates install/update/info to the CLI so it can honor a license key.
//
// Why the CLI path exists: the free binary is one concurrent session; a paid
// license unlocks more. The CLI (`cloakbrowser login/install/info`) manages the
// license + downloads the matching tier's binary under ~/.cloakbrowser. Wick
// can't know a key's tier on its own, so for the pro engine we let the CLI own
// the binary + tier and just read its `info` output. This also avoids the
// "updated via CLI but wick shows the old version" mismatch — the pro engine
// reads the CLI's binary/version directly instead of its own cache.

// cloakLicenseKey returns the configured CloakBrowser license key (secret), or
// "". Passed to the browser at launch as CLOAKBROWSER_LICENSE_KEY so a paid
// binary runs at its licensed tier.
func cloakLicenseKey(c *connector.Ctx) string {
	return strings.TrimSpace(c.Cfg("cloak_license_key"))
}

// isCloakFree reports whether the selected engine is CloakBrowser running on the
// free tier, used to hard-cap concurrent sessions to 1 (CloakBrowser's free
// plan). Keyed off the configured engine name, not whether a key is set (the
// free plan also issues a key):
//   - "cloakbrowser" (free engine) → wick runs its own downloaded free binary,
//     so it's always the free tier → locked to 1.
//   - "cloakbrowser-pro" → read the real tier from `cloakbrowser info` (cached);
//     "pro" lifts the lock, anything else (incl. unknown / unresolvable) stays
//     free-locked as the safe default.
//   - a non-cloak engine is never locked.
func isCloakFree(c *connector.Ctx) bool {
	engine := strings.TrimSpace(c.Cfg("browser"))
	if !isCloakEngine(engine) {
		return false
	}
	if !isCloakPro(engine) {
		return true // free engine: wick's own download is the free binary
	}
	info, err := cloakGetInfoCached(c)
	if err != nil {
		return true // can't confirm pro → stay safe with the free lock
	}
	return !strings.EqualFold(strings.TrimSpace(info.Tier), "pro")
}

// cloakCLIPath is the `cloakbrowser` executable to invoke. Overridable via
// cloak_cli_path for a non-PATH install; defaults to "cloakbrowser" (resolved on
// PATH). Returns "" only if an explicit override is set but missing.
func cloakCLIPath(c *connector.Ctx) string {
	if p := strings.TrimSpace(c.Cfg("cloak_cli_path")); p != "" {
		return p
	}
	return "cloakbrowser"
}

// cloakCLIAvailable reports whether the cloakbrowser CLI can be found.
func cloakCLIAvailable(c *connector.Ctx) bool {
	p := cloakCLIPath(c)
	if strings.ContainsAny(p, `/\`) {
		return fileExists(p)
	}
	_, err := exec.LookPath(p)
	return err == nil
}

// cloakCLIEnv returns the environment additions (KEY=VAL slice, for exec.Cmd.Env)
// for a CLI/browser invocation: the license key when set, so both `cloakbrowser`
// commands and the launched browser pick up the paid tier.
func cloakCLIEnv(c *connector.Ctx) []string {
	if key := cloakLicenseKey(c); key != "" {
		return []string{"CLOAKBROWSER_LICENSE_KEY=" + key}
	}
	return nil
}

// cloakLaunchEnv returns the full env map for a Playwright launch of CloakBrowser
// (Playwright REPLACES the process env, so we start from os.Environ and add the
// license key). Returns nil when there's no key — Playwright then keeps its
// default (process env), so the free binary launches unchanged.
func cloakLaunchEnv(c *connector.Ctx) map[string]string {
	key := cloakLicenseKey(c)
	if key == "" {
		return nil
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	env["CLOAKBROWSER_LICENSE_KEY"] = key
	return env
}

// cloakInfo is the parsed output of `cloakbrowser info`.
type cloakInfo struct {
	Version   string // e.g. "146.0.7680.177.5"
	Tier      string // "free" | "pro" (from the "(free)" suffix on Version)
	Binary    string // absolute path to the chrome executable
	Installed bool
	Cache     string
	raw       string
}

// runCloakCLI runs one cloakbrowser subcommand with the license env and a
// timeout, returning combined output. Errors carry the output so a licensing
// failure is visible. Every invocation is logged (command + duration + exit +
// output snippet) so a "not installed" / hung op is diagnosable from the logs.
func runCloakCLI(c *connector.Ctx, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()
	bin := cloakCLIPath(c)
	clog.Info().Str("cli", bin).Strs("args", args).Dur("timeout", timeout).
		Bool("has_license", cloakLicenseKey(c) != "").Msg("cloak cli: running")
	start := time.Now()
	cmd := safeexec.CommandContext(ctx, bin, args...)
	cmd.Env = append(cmd.Environ(), cloakCLIEnv(c)...)
	out, err := cmd.CombinedOutput()
	snippet := string(out)
	if len(snippet) > 400 {
		snippet = snippet[:400] + "…"
	}
	ev := clog.Info()
	if err != nil {
		ev = clog.Warn().Err(err)
	}
	ev.Str("cli", bin).Strs("args", args).Dur("took", time.Since(start)).
		Int("out_bytes", len(out)).Str("output", snippet).Msg("cloak cli: done")
	return string(out), err
}

// cloakInfoCache memoizes `cloakbrowser info` for a short TTL. The status widget
// polls every ~1.2s; shelling out to the CLI each render would make it crawl.
// The TTL is short so an install/update via the ⋮ menu reflects quickly.
var (
	cloakInfoMu    sync.Mutex
	cloakInfoVal   cloakInfo
	cloakInfoOK    bool
	cloakInfoWhen  time.Time
	cloakInfoTTL   = 2 * time.Minute
)

// invalidateCloakInfo drops the cached info so the next read re-runs the CLI —
// called right after an install/update so the widget shows the new state.
func invalidateCloakInfo() {
	cloakInfoMu.Lock()
	cloakInfoOK = false
	cloakInfoMu.Unlock()
}

// cloakGetInfoCached returns the parsed `cloakbrowser info`, cached for a short
// TTL. Errors are not cached (so a transient CLI failure retries next call).
func cloakGetInfoCached(c *connector.Ctx) (cloakInfo, error) {
	cloakInfoMu.Lock()
	if cloakInfoOK && time.Since(cloakInfoWhen) < cloakInfoTTL {
		v := cloakInfoVal
		cloakInfoMu.Unlock()
		return v, nil
	}
	cloakInfoMu.Unlock()

	info, err := cloakGetInfo(c)
	if err != nil {
		return info, err
	}
	cloakInfoMu.Lock()
	cloakInfoVal = info
	cloakInfoOK = true
	cloakInfoWhen = time.Now()
	cloakInfoMu.Unlock()
	return info, nil
}

// cloakGetInfo runs `cloakbrowser info --no-launch` and parses it. Best-effort: a
// parse miss leaves the field empty rather than erroring, so a slightly different
// CLI version doesn't break the widget.
//
// --no-launch is critical: a plain `cloakbrowser info` runs a LAUNCH TEST — it
// actually spawns the stealth Chromium to prove it executes — which flashed a
// browser window (and could leave a process running) every time the manager
// rendered the pro engine's status row. --no-launch skips that test but still
// reports Version + Installed + tier + Binary (read from the cache/manifest, no
// download, no launch), so the widget gets everything it needs with zero flicker.
// It also drops the info call from ~12s to ~0.4s. Mirrors what the widget needs
// from the wrapper's cmd_info(quick=True) path.
func cloakGetInfo(c *connector.Ctx) (cloakInfo, error) {
	out, err := runCloakCLI(c, 30*time.Second, "info", "--no-launch")
	// Parse the output even on a non-zero exit: `cloakbrowser info` can print a
	// benign warning (e.g. a Windows console encoding error for a ✗ glyph) and
	// still emit the full diagnostics, or exit non-zero while the diagnostics are
	// valid. If the parsed output actually says Installed, trust it — treating a
	// noisy-but-informative run as "not installed" is the bug we're fixing.
	info := parseCloakInfo(out)
	if info.Installed || info.Version != "" {
		return info, nil
	}
	return info, err
}

// parseCloakInfo parses `cloakbrowser info` output into cloakInfo. Pure (no
// shell) so it's unit-testable. Best-effort: an unrecognized line is ignored, so
// a slightly different CLI version doesn't break the widget.
func parseCloakInfo(out string) cloakInfo {
	info := cloakInfo{raw: out}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "version":
			// "146.0.7680.177.5 (free)" → version + tier.
			info.Version = val
			if i := strings.Index(val, "("); i >= 0 {
				info.Version = strings.TrimSpace(val[:i])
				info.Tier = strings.ToLower(strings.Trim(val[i:], "() "))
			}
		case "binary":
			info.Binary = val
		case "installed":
			info.Installed = strings.EqualFold(val, "true")
		case "cache":
			info.Cache = val
		}
	}
	return info
}

// cloakCLIBinary returns the browser binary path the CLI reports, or "" if the
// CLI is unavailable / reports nothing installed. Used by launch resolution for
// the pro engine so wick runs the SAME binary the CLI manages.
func cloakCLIBinary(c *connector.Ctx) string {
	info, err := cloakGetInfo(c)
	if err != nil || !info.Installed {
		return ""
	}
	if info.Binary != "" && fileExists(info.Binary) {
		return info.Binary
	}
	return ""
}

// cloakCLIInstall runs `cloakbrowser install` (downloads the tier's binary). Its
// output/progress is returned as text — the manager widget shows it as a note.
func cloakCLIInstall(c *connector.Ctx) (string, error) {
	return runCloakCLI(c, 10*time.Minute, "install")
}

// cloakCLIUpdate runs `cloakbrowser update`.
func cloakCLIUpdate(c *connector.Ctx) (string, error) {
	return runCloakCLI(c, 10*time.Minute, "update")
}
