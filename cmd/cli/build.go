package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// buildCmd compiles the downstream binary with -ldflags injecting
// BuildAppName / BuildAppVersion (read from wick.yml) plus optional
// GitHubPAT / GitHubRepo for the self-updater. Honors GOOS/GOARCH from
// the environment for cross-compilation.
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
		bundle     bool
		headless   bool
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Compile the app binary with embedded ldflags",
		Long: `Compile the Go binary in CWD with -ldflags injecting app
name/version plus optional GitHub PAT/repo for the self-updater.

Resolution order per value:
  --app-name    flag > $WICK_APP_NAME    > wick.yml name    > "app"
  --app-version flag > $WICK_APP_VERSION > wick.yml version > "dev"
  --github-pat  flag > $GITHUB_PAT
  --github-repo flag > $GITHUB_REPOSITORY

GOOS / GOARCH from the environment drive cross-compilation. Default
output is bin/<app-name>[.exe]; override with --output.`,
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
			if appName == "" {
				appName = "app"
			}
			if appVersion == "" {
				appVersion = "dev"
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

			goos := os.Getenv("GOOS")
			if goos == "" {
				goos = runtime.GOOS
			}

			if output == "" {
				output = filepath.Join("bin", appName)
				if goos == "windows" {
					output += ".exe"
				}
			}
			if dir := filepath.Dir(output); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("mkdir %s: %w", dir, err)
				}
			}

			ldflags := []string{
				fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppName=%s", appName),
				fmt.Sprintf("-X github.com/yogasw/wick/app.BuildAppVersion=%s", appVersion),
			}
			if githubPAT != "" {
				ldflags = append(ldflags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubPAT=%s", githubPAT))
			}
			if githubRepo != "" {
				ldflags = append(ldflags, fmt.Sprintf("-X github.com/yogasw/wick/app.GitHubRepo=%s", githubRepo))
			}
			// Windows GUI subsystem: double-click launches systray with no
			// console window. cmd-launched processes still get stdout via
			// AttachConsole(ATTACH_PARENT_PROCESS) wired in app/console_windows.go.
			// --headless drops this so the binary stays a console app.
			if !headless && goos == "windows" {
				ldflags = append(ldflags, "-H=windowsgui")
			}

			// Windows .exe metadata: drop a .syso resource file next to
			// main.go so the next `go build` picks up the brand icon plus
			// version metadata. Cleanup runs after the build finishes.
			if goos == "windows" {
				cleanup, err := embedWindowsResource(output, appName, appVersion)
				if err != nil {
					return err
				}
				defer cleanup()
			}

			goArgs := []string{"build", "-ldflags", strings.Join(ldflags, " "), "-o", output}
			if headless {
				goArgs = append(goArgs, "-tags", "headless")
			}
			goArgs = append(goArgs, ".")

			fmt.Printf("> go %s\n", strings.Join(goArgs, " "))
			gobuild := exec.Command("go", goArgs...)
			gobuild.Stdout = os.Stdout
			gobuild.Stderr = os.Stderr
			gobuild.Env = os.Environ()
			if err := gobuild.Run(); err != nil {
				return err
			}

			// Optional packaging step: wrap the bare binary into a native
			// distributable per target OS. Windows has nothing to wrap —
			// the .exe already carries its icon + version metadata via the
			// .syso step above. Mac gets a .app bundle, Linux gets a .deb.
			if !bundle {
				return nil
			}
			goarch := os.Getenv("GOARCH")
			if goarch == "" {
				goarch = runtime.GOARCH
			}
			switch goos {
			case "darwin":
				bundleID := resolveBundleID(appName)
				app, err := packageMacApp(output, appName, appVersion, bundleID)
				if err != nil {
					return fmt.Errorf("package mac app: %w", err)
				}
				fmt.Printf("> bundled %s\n", app)
			case "linux":
				deb, err := packageLinuxDeb(output, appName, appVersion, goarch)
				if err != nil {
					return fmt.Errorf("package linux deb: %w", err)
				}
				fmt.Printf("> bundled %s\n", deb)
			case "windows":
				// no-op — icon + version metadata already in the .exe
			default:
				return fmt.Errorf("--bundle: unsupported GOOS %q", goos)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&appName, "app-name", "", "App name → app.BuildAppName (env: WICK_APP_NAME, fallback: wick.yml name)")
	cmd.Flags().StringVar(&appVersion, "app-version", "", "App version → app.BuildAppVersion (env: WICK_APP_VERSION, fallback: wick.yml version)")
	cmd.Flags().StringVar(&githubPAT, "github-pat", "", "GitHub PAT → app.GitHubPAT (env: GITHUB_PAT)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repo owner/<app>-releases → app.GitHubRepo (env: GITHUB_REPOSITORY)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output binary path (default: bin/<app-name>[.exe])")
	cmd.Flags().BoolVar(&bundle, "bundle", false, "Wrap the binary into a native distributable (mac=.app, linux=.deb; windows already self-contains its icon + version metadata)")
	cmd.Flags().BoolVar(&headless, "headless", false, "Build with -tags headless (excludes systray)")
	return cmd
}

// parseSemver pulls the leading major.minor.patch out of a version
// string. Tolerates a leading "v" and any -suffix / +metadata; missing
// segments default to 0 ("0.7" → 0,7,0; "v1.2.3-beta" → 1,2,3).
func parseSemver(v string) (major, minor, patch int) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	get := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, _ := strconv.Atoi(parts[i])
		return n
	}
	return get(0), get(1), get(2)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
