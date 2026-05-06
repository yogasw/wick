package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/builder"
)

// allTargets is the canonical list of OS/arch pairs `wick build --all`
// iterates. Order chosen so cheap pure-Go targets (windows, linux) run
// before the cgo-heavy darwin pair — fail-fast feedback when a darwin
// cross-compile blows up on a non-darwin host.
var allTargets = []string{
	"windows/amd64",
	"windows/arm64",
	"linux/amd64",
	"linux/arm64",
	"darwin/amd64",
	"darwin/arm64",
}

// buildCmd compiles the downstream binary plus the platform-native
// distributable (.dmg on darwin, .deb on linux, .exe with embedded
// metadata on windows). Reads name/version from wick.yml and bakes
// optional GitHubPAT/GitHubRepo into the binary for the self-updater.
//
// Runs the wick.yml `generate` task first when present so templ +
// CSS + go generate stay in sync with the binary — keeps CI one-shot.
func buildCmd() *cobra.Command {
	var (
		appName    string
		appVersion string
		githubPAT  string
		githubRepo string
		output     string
		target     string
		goos       string
		goarch     string
		headless   bool
		buildAll   bool
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Compile the app binary plus the native distributable",
		Long: `Compile the Go binary in CWD with -ldflags injecting app
name/version plus optional GitHub PAT/repo for the self-updater, then
wrap it into the platform-native distributable:

  windows → .exe with embedded brand icon + version metadata
  darwin  → .app bundle, then .dmg disk image (host-darwin only)
  linux   → .deb binary package

Resolution order per value:
  --app-name    flag > $WICK_APP_NAME    > wick.yml name    > "app"
  --app-version flag > $WICK_APP_VERSION > wick.yml version > "dev"
  --github-pat  flag > $GITHUB_PAT
  --github-repo flag > $GITHUB_REPOSITORY
  --target / --goos+--goarch flag > $GOOS, $GOARCH > host runtime

Default output is bin/<app-name>-<goos>-<goarch>[.exe]; override with --output.`,
		RunE: func(c *cobra.Command, args []string) error {
			appName = firstNonEmpty(appName, os.Getenv("WICK_APP_NAME"))
			appVersion = firstNonEmpty(appVersion, os.Getenv("WICK_APP_VERSION"))
			cfg, _ := loadConfig()
			if cfg != nil {
				if appName == "" {
					appName = cfg.Name
				}
				if appVersion == "" {
					appVersion = cfg.Version
				}
			}

			if cfg != nil {
				if _, ok := cfg.Tasks["generate"]; ok {
					if err := runTask("generate"); err != nil {
						return fmt.Errorf("generate task: %w", err)
					}
				}
			}

			githubPAT = firstNonEmpty(githubPAT, os.Getenv("GITHUB_PAT"))
			githubRepo = firstNonEmpty(githubRepo, os.Getenv("GITHUB_REPOSITORY"))

			baseCfg := builder.Config{
				AppName:    appName,
				AppVersion: appVersion,
				GitHubPAT:  githubPAT,
				GitHubRepo: githubRepo,
				Headless:   headless,
			}

			if buildAll {
				if target != "" || goos != "" || goarch != "" || output != "" {
					return fmt.Errorf("--all is mutually exclusive with --target/--goos/--goarch/--output")
				}
				return runBuildAll(baseCfg)
			}

			if target != "" {
				if goos != "" || goarch != "" {
					return fmt.Errorf("--target is mutually exclusive with --goos/--goarch")
				}
				parts := strings.SplitN(target, "/", 2)
				if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
					return fmt.Errorf("--target must be <os>/<arch> (e.g. linux/arm64)")
				}
				goos, goarch = parts[0], parts[1]
			}
			baseCfg.GOOS = firstNonEmpty(goos, os.Getenv("GOOS"))
			baseCfg.GOARCH = firstNonEmpty(goarch, os.Getenv("GOARCH"))
			baseCfg.Output = output

			res, err := builder.Build(baseCfg)
			if err != nil {
				return err
			}
			fmt.Printf("> built %s\n", res.Binary)
			return nil
		},
	}
	cmd.Flags().StringVar(&appName, "app-name", "", "App name → app.BuildAppName (env: WICK_APP_NAME, fallback: wick.yml name)")
	cmd.Flags().StringVar(&appVersion, "app-version", "", "App version → app.BuildAppVersion (env: WICK_APP_VERSION, fallback: wick.yml version)")
	cmd.Flags().StringVar(&githubPAT, "github-pat", "", "GitHub PAT → app.GitHubPAT (env: GITHUB_PAT)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repo owner/<app>-releases → app.GitHubRepo (env: GITHUB_REPOSITORY)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output binary path (default: bin/<app-name>-<goos>-<goarch>[.exe])")
	cmd.Flags().StringVarP(&target, "target", "t", "", "Build target shorthand: <os>/<arch> (e.g. linux/arm64, darwin/amd64). Mutually exclusive with --goos/--goarch")
	cmd.Flags().StringVar(&goos, "goos", "", "Target GOOS (env: GOOS). Mutually exclusive with --target")
	cmd.Flags().StringVar(&goarch, "goarch", "", "Target GOARCH (env: GOARCH). Mutually exclusive with --target")
	cmd.Flags().BoolVar(&headless, "headless", false, "Build with -tags headless (excludes systray)")
	cmd.Flags().BoolVar(&buildAll, "all", false, "Best-effort build for every supported OS/arch (skips darwin/* on non-darwin hosts). Mutually exclusive with --target/--goos/--goarch/--output")
	return cmd
}

// runBuildAll iterates allTargets, building each one in turn and
// collecting errors instead of failing fast. Darwin targets are
// skipped upfront on non-darwin hosts (cgo systray needs Cocoa, never
// produces a usable binary cross-compiled). Returns a non-nil error
// only when zero targets succeeded.
func runBuildAll(base builder.Config) error {
	type result struct {
		target string
		out    string
		err    error
	}
	hostOS := runtime.GOOS
	results := make([]result, 0, len(allTargets))

	for _, t := range allTargets {
		parts := strings.SplitN(t, "/", 2)
		tgtOS, tgtArch := parts[0], parts[1]
		if tgtOS == "darwin" && hostOS != "darwin" {
			results = append(results, result{t, "", errors.New("skipped: darwin needs a macOS host (cgo systray)")})
			fmt.Printf("> %-15s ✗ skipped (darwin needs macOS host)\n", t)
			continue
		}
		cfg := base
		cfg.GOOS = tgtOS
		cfg.GOARCH = tgtArch
		cfg.Output = ""
		fmt.Printf("> %-15s building...\n", t)
		res, err := builder.Build(cfg)
		if err != nil {
			results = append(results, result{t, "", err})
			fmt.Printf("> %-15s ✗ %v\n", t, err)
			continue
		}
		results = append(results, result{t, res.Binary, nil})
		fmt.Printf("> %-15s ✓ %s\n", t, res.Binary)
	}

	successes := 0
	for _, r := range results {
		if r.err == nil {
			successes++
		}
	}
	fmt.Printf("\nSummary: %d/%d succeeded", successes, len(results))
	if skipped := len(results) - successes; skipped > 0 {
		fmt.Printf(" (%d skipped/failed)", skipped)
	}
	fmt.Println()
	if successes == 0 {
		return errors.New("--all: every target failed")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
