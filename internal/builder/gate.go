package builder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gateModulePath is the import path of the gate command in the wick
// module. Builder resolves it via `go build <importpath>` so a
// downstream project that depends on wick (but doesn't have
// `cmd/gate/` in its own tree) still gets a gate binary built from
// the wick module cache.
const gateModulePath = "github.com/yogasw/wick/cmd/gate"

// embedAssetDir is the path inside the wick module where the
// embedded gate asset lives. embed.go's `//go:embed all:assets`
// directive picks up `assets/gate-<os>-<arch>[.exe]` files dropped
// here right before `go build` runs on the parent.
const embedAssetDir = "internal/agents/gate/assets"

// buildGateBinary compiles cmd/gate twice (or once + copy) per Build
// call:
//
//   - assets/gate-<os>-<arch>[.exe] inside the wick module — picked
//     up by //go:embed in the parent's `go build` step
//   - bin/<app>-gate-<os>-<arch>[.exe] alongside the parent binary
//     — user-visible sidecar so installer packaging or a sibling
//     drop just works without a separate build invocation
//
// Returns the path of the bin/ artifact, or empty string + nil error
// when cmd/gate isn't reachable (downstream fork without the gate
// command in its module graph). The caller treats that as a soft
// skip — runtime gracefully falls back to PATH lookup.
func buildGateBinary(cfg Config) (binArtifact string, err error) {
	if !gateModuleAvailable() {
		fmt.Println("> gate: cmd/gate not in module graph — skipping (downstream fork without gate)")
		return "", nil
	}

	embedExt := ""
	if cfg.GOOS == "windows" {
		embedExt = ".exe"
	}
	embedOut := filepath.Join(embedAssetDir, fmt.Sprintf("gate-%s-%s%s", cfg.GOOS, cfg.GOARCH, embedExt))
	if err := os.MkdirAll(filepath.Dir(embedOut), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(embedOut), err)
	}

	gateLDFlags := fmt.Sprintf("-s -w -X github.com/yogasw/wick/internal/appname.BuildAppName=%s", cfg.AppName)
	fmt.Printf("> go build %s → %s\n", gateModulePath, embedOut)
	embedCmd := exec.Command("go", "build",
		"-trimpath",
		"-ldflags", gateLDFlags,
		"-o", embedOut,
		gateModulePath,
	)
	embedCmd.Stdout = os.Stdout
	embedCmd.Stderr = os.Stderr
	embedCmd.Env = append(os.Environ(),
		"GOOS="+cfg.GOOS,
		"GOARCH="+cfg.GOARCH,
		"CGO_ENABLED=0",
	)
	if err := embedCmd.Run(); err != nil {
		return "", fmt.Errorf("build gate (embed asset): %w", err)
	}

	// User-visible sidecar in bin/ — same content, branded filename
	// so a `bin/` listing tells the operator "this is myapp's gate".
	// We copy the embed asset rather than re-compiling: same bytes,
	// half the time on `--all` builds.
	binDir := filepath.Dir(cfg.Output)
	if binDir == "" || binDir == "." {
		binDir = "bin"
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", binDir, err)
	}
	binArtifact = filepath.Join(binDir, fmt.Sprintf("%s-gate-%s-%s%s", cfg.AppName, cfg.GOOS, cfg.GOARCH, embedExt))
	if err := copyFile(embedOut, binArtifact); err != nil {
		return "", fmt.Errorf("copy gate to bin: %w", err)
	}
	fmt.Printf("> built %s\n", binArtifact)
	return binArtifact, nil
}

// gateModuleAvailable reports whether `go list` can resolve the gate
// command. Returns false on a downstream fork that pruned cmd/gate
// or pinned an older wick that predated it. Used to decide whether
// the build step runs at all.
func gateModuleAvailable() bool {
	cmd := exec.Command("go", "list", gateModulePath)
	cmd.Stderr = nil
	cmd.Stdout = nil
	return cmd.Run() == nil
}

// copyFile is a small portable file copy. Used here to mirror the
// embed-asset bytes into bin/ without invoking `go build` twice.
func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o755)
}
