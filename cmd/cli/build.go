package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// buildCmd compiles the downstream binary with -ldflags injecting
// BuildAppName / BuildAppVersion (read from wick.yml) plus optional
// GitHubPAT / GitHubRepo for the self-updater. Honors GOOS/GOARCH from
// the environment for cross-compilation.
//
// Replaces the prior generic `runTask("build")` wrapper — wick.yml's
// build task should now invoke `wick build` as its final step instead
// of a hand-rolled `go build -ldflags "..."` line.
func buildCmd() *cobra.Command {
	var (
		appName    string
		appVersion string
		githubPAT  string
		githubRepo string
		output     string
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
			if appName == "" || appVersion == "" {
				if cfg, err := loadConfig(); err == nil {
					if appName == "" {
						appName = cfg.Name
					}
					if appVersion == "" {
						appVersion = cfg.Version
					}
				}
			}
			if appName == "" {
				appName = "app"
			}
			if appVersion == "" {
				appVersion = "dev"
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
			return gobuild.Run()
		},
	}
	cmd.Flags().StringVar(&appName, "app-name", "", "App name → app.BuildAppName (env: WICK_APP_NAME, fallback: wick.yml name)")
	cmd.Flags().StringVar(&appVersion, "app-version", "", "App version → app.BuildAppVersion (env: WICK_APP_VERSION, fallback: wick.yml version)")
	cmd.Flags().StringVar(&githubPAT, "github-pat", "", "GitHub PAT → app.GitHubPAT (env: GITHUB_PAT)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repo owner/<app>-releases → app.GitHubRepo (env: GITHUB_REPOSITORY)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output binary path (default: bin/<app-name>[.exe])")
	cmd.Flags().BoolVar(&headless, "headless", false, "Build with -tags headless (excludes systray)")
	return cmd
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
