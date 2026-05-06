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
}

// Result lists the artifacts a Build produced. Binary is always the
// raw compiled binary; Bundles are the platform-native distributables
// (.app, .dmg, .deb) layered on top.
type Result struct {
	Binary  string
	Bundles []string
}
