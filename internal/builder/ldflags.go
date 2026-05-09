package builder

import (
	"encoding/base64"
	"fmt"
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
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppName=%s", cfg.AppName),
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppVersion=%s", cfg.AppVersion),
		// gate.AppName drives the per-app sibling/PATH lookup name
		// (`<app>-gate[.exe]`). Without this injection the runtime
		// falls back to the unbranded "gate" lookup, which won't
		// match what `wick build` writes to `bin/`.
		fmt.Sprintf("-X github.com/yogasw/wick/internal/agents/gate.AppName=%s", cfg.AppName),
	}
	if cfg.GitHubPAT != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubPATEnc=%s", obfuscatePAT(cfg.GitHubPAT)))
	}
	if cfg.GitHubRepo != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubRepo=%s", cfg.GitHubRepo))
	}
	return flags
}
