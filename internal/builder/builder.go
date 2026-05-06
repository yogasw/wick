// Package builder compiles a wick app into a raw Go binary plus the
// platform-native distributable that ships with each release:
//
//	windows → .exe with embedded brand icon + version metadata
//	darwin  → .app bundle, then .dmg disk image (host-darwin only)
//	linux   → .deb binary package
//
// Cross-compilation is supported for everything except .dmg (which
// requires the macOS-only `hdiutil` tool); cross-builds from non-darwin
// hosts produce the .app bundle and skip the .dmg step.
package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yogasw/wick/internal/builder/darwin"
	"github.com/yogasw/wick/internal/builder/linux"
	"github.com/yogasw/wick/internal/builder/windows"
)

// Build compiles the Go source in CWD per cfg, then wraps the
// resulting binary into the platform-native distributable. Returns
// the artifacts produced (raw binary always, plus platform bundles).
func Build(cfg Config) (Result, error) {
	if cfg.AppName == "" {
		cfg.AppName = "app"
	}
	if cfg.AppVersion == "" {
		cfg.AppVersion = "dev"
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.GOARCH == "" {
		cfg.GOARCH = runtime.GOARCH
	}
	if cfg.Output == "" {
		cfg.Output = filepath.Join("bin", cfg.AppName+"-"+cfg.GOOS+"-"+cfg.GOARCH)
		if cfg.GOOS == "windows" {
			cfg.Output += ".exe"
		}
	}
	if dir := filepath.Dir(cfg.Output); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	ldflags := assembleLDFlags(cfg)

	// Windows .syso must exist next to main.go BEFORE `go build`
	// runs — the linker picks it up automatically. Cleanup runs
	// after the compile finishes regardless of success.
	if cfg.GOOS == "windows" {
		cleanup, err := windows.EmbedResource(cfg.Output, cfg.AppName, cfg.AppVersion)
		if err != nil {
			return Result{}, err
		}
		defer cleanup()
	}

	if err := runGoBuild(cfg, ldflags); err != nil {
		return Result{}, err
	}

	res := Result{Binary: cfg.Output}

	switch cfg.GOOS {
	case "darwin":
		bundleID := ResolveBundleID(cfg.AppName)
		appPath, err := darwin.PackageApp(cfg.Output, cfg.AppName, cfg.AppVersion, bundleID)
		if err != nil {
			return res, fmt.Errorf("package mac app: %w", err)
		}
		res.Bundles = append(res.Bundles, appPath)
		fmt.Printf("> bundled %s\n", appPath)

		dmgPath := filepath.Join(filepath.Dir(cfg.Output), fmt.Sprintf("%s-darwin-%s.dmg", cfg.AppName, cfg.GOARCH))
		out, err := darwin.PackageDMG(appPath, dmgPath, cfg.AppName)
		switch {
		case err == darwin.ErrSkippedDMG:
			fmt.Println("> dmg skipped (hdiutil only available on macOS host)")
		case err != nil:
			return res, fmt.Errorf("package mac dmg: %w", err)
		default:
			res.Bundles = append(res.Bundles, out)
			fmt.Printf("> bundled %s\n", out)
		}

	case "linux":
		debPath, err := linux.PackageDeb(cfg.Output, cfg.AppName, cfg.AppVersion, cfg.GOARCH)
		if err != nil {
			return res, fmt.Errorf("package linux deb: %w", err)
		}
		res.Bundles = append(res.Bundles, debPath)
		fmt.Printf("> bundled %s\n", debPath)

	case "windows":
		// .exe already self-contained (icon + version metadata baked
		// in via the .syso step above) — no further wrapping needed.
	}

	return res, nil
}

func runGoBuild(cfg Config, ldflags []string) error {
	args := []string{"build", "-ldflags", strings.Join(ldflags, " "), "-o", cfg.Output}
	if cfg.Headless {
		args = append(args, "-tags", "headless")
	}
	args = append(args, ".")

	fmt.Printf("> go %s\n", strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"GOOS="+cfg.GOOS,
		"GOARCH="+cfg.GOARCH,
	)
	return cmd.Run()
}
