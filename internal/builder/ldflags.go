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
// GitHubPATEnc / GitHubRepo for the self-updater. On Windows we also
// add -H=windowsgui (unless --headless) so double-click launches
// without a console window.
func assembleLDFlags(cfg Config) []string {
	flags := []string{
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppName=%s", cfg.AppName),
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppVersion=%s", cfg.AppVersion),
	}
	if cfg.GitHubPAT != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubPATEnc=%s", obfuscatePAT(cfg.GitHubPAT)))
	}
	if cfg.GitHubRepo != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubRepo=%s", cfg.GitHubRepo))
	}
	if !cfg.Headless && cfg.GOOS == "windows" {
		flags = append(flags, "-H=windowsgui")
	}
	return flags
}
