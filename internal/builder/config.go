package builder

// Config drives a Build invocation. AppName + AppVersion are baked
// into the binary via -ldflags; GOOS / GOARCH select the target.
// Empty fields fall back to runtime defaults.
type Config struct {
	AppName    string
	AppVersion string
	GOOS       string
	GOARCH     string
	Output     string
	GitHubPAT  string
	GitHubRepo string
	Headless   bool
	// Installer opts the windows target into building an .msi on top
	// of the raw .exe (requires `wixl` from msitools on PATH). The
	// .msi is always built per-user — installs to %LocalAppData%\
	// Programs\<AppName>, requires no UAC, and lets the in-app
	// self-updater rewrite the .exe without elevation. Off by default
	// so existing pipelines keep producing the same artifacts.
	Installer bool
}

// Result lists the artifacts a Build produced. Binary is always the
// raw compiled binary; Bundles are the platform-native distributables
// (.app, .dmg, .deb) layered on top.
type Result struct {
	Binary  string
	Bundles []string
}
