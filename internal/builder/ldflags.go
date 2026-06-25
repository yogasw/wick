package builder

import (
	"encoding/base64"
	"fmt"
	"time"
)

// obfPATKey MUST match app/app.go patObfKey — runtime XORs back with it.
// Stops casual `strings <binary> | grep ghp_` from surfacing the token.
// Not real encryption: an attacker who reads the binary can extract this
// constant and decode. Real defense is scoping the PAT to read-only on
// the releases repo so a leak only enables downloading already-public
// release assets.
const obfPATKey = "wick-self-updater-pat-v1"

func obfuscatePAT(s string) string {
	b := []byte(s)
	k := []byte(obfPATKey)
	for i := range b {
		b[i] ^= k[i%len(k)]
	}
	return base64.StdEncoding.EncodeToString(b)
}

// assembleLDFlags builds the -ldflags string passed to `go build`,
// injecting BuildAppName / BuildAppVersion (always) plus optional
// GitHubPATEnc / GitHubRepo for the self-updater.
//
// On Windows we deliberately do NOT pass -H=windowsgui. That flag would
// hide the console flash on Explorer double-click but also detaches
// stdin/stdout/stderr from any parent — breaking `mcp serve` (Claude
// Desktop pipes), `server`, and `worker` whenever they're spawned by a
// console-aware client. Instead the binary stays a console subsystem
// executable and systemtray.Run calls FreeConsole on Windows the moment
// it knows it's in tray mode, so the cmd window vanishes immediately
// without breaking pipe-attached subcommands.
func assembleLDFlags(cfg Config) []string {
	flags := []string{
		// Single source of truth for the app brand. `app.BuildAppName`
		// is mirrored from this in app.init() so legacy callers still
		// work; agents/gate/Layout read appname.Resolve() directly.
		fmt.Sprintf("-X github.com/yogasw/wick/internal/appname.BuildAppName=%s", cfg.AppName),
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppVersion=%s", cfg.AppVersion),
		// Build time = when this `wick build` runs. Stamped directly so the
		// "Built" field is always populated, regardless of whether the build
		// happens inside a git checkout. Go's own vcs.time stamping only fires
		// in a git tree (absent in the release pipeline's `wick init` scaffold,
		// which left "Built" reading "unknown"); this overrides it everywhere
		// with the actual compile time, which is what "Built" should mean.
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildTime=%s", time.Now().UTC().Format(time.RFC3339)),
	}
	if cfg.GitHubPAT != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubPATEnc=%s", obfuscatePAT(cfg.GitHubPAT)))
	}
	if cfg.GitHubRepo != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubRepo=%s", cfg.GitHubRepo))
	}
	return flags
}
