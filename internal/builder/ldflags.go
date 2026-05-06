package builder

import "fmt"

// assembleLDFlags builds the -ldflags string passed to `go build`,
// injecting BuildAppName / BuildAppVersion (always) plus optional
// GitHubPAT / GitHubRepo for the self-updater. On Windows we also
// add -H=windowsgui (unless --headless) so double-click launches
// without a console window.
func assembleLDFlags(cfg Config) []string {
	flags := []string{
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppName=%s", cfg.AppName),
		fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppVersion=%s", cfg.AppVersion),
	}
	if cfg.GitHubPAT != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubPAT=%s", cfg.GitHubPAT))
	}
	if cfg.GitHubRepo != "" {
		flags = append(flags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubRepo=%s", cfg.GitHubRepo))
	}
	if !cfg.Headless && cfg.GOOS == "windows" {
		flags = append(flags, "-H=windowsgui")
	}
	return flags
}
