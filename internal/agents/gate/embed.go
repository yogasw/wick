package gate

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed all:assets
var embeddedGateFS embed.FS

// AppName is the brand prefix for the per-app gate binary. Injected
// at link time via `-X github.com/yogasw/wick/internal/agents/gate.AppName=<name>`
// so the parent app and its gate sidecar share the same brand. Empty
// string falls back to "gate" for `go run` / source builds.
//
// Used to derive the sibling-binary lookup name (`<app>-gate[.exe]`)
// and the PATH lookup name. The embedded asset stays generic
// (`assets/gate-<os>-<arch>`) — branding only matters at runtime
// resolution, not at embed time.
var AppName = ""

// envOverride is the env-var name dev tooling sets to point at a
// freshly-built gate binary outside the embed (e.g. wicklab + go run).
// Resolution checks this first so VSCode F5 doesn't need a full
// rebuild of the parent binary.
const envOverride = "GATE_BIN"

// errNoEmbeddedGate signals that the embed is empty — typically a
// `go run` build that skipped the build step. Caller can fall back
// to PATH lookup or surface the misconfiguration.
var errNoEmbeddedGate = errors.New("no embedded gate binary for this platform")

// embeddedGateName returns the asset filename for the current
// runtime. Format mirrors the builder step: assets/gate-<os>-<arch>[.exe].
func embeddedGateName() string {
	name := fmt.Sprintf("assets/gate-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// brandedGateName returns the per-app sibling/PATH lookup name:
// `<AppName>-gate[.exe]`, or `gate[.exe]` when AppName is empty.
func brandedGateName() string {
	base := "gate"
	if AppName != "" {
		base = AppName + "-gate"
	}
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	return base
}

// Resolution source labels — exposed via ResolveGateBinaryWithSource
// so the Providers page UI can show *how* the binary got picked,
// useful when debugging why dev override silently shadowed the
// embedded one.
const (
	SourceEnvOverride = "env_override"
	SourceEmbed       = "embed"
	SourceSibling     = "sibling"
	SourcePath        = "path"
)

// ResolveGateBinary picks the gate binary for the current process.
// Thin wrapper around ResolveGateBinaryWithSource for callers that
// don't care about the resolution source.
func ResolveGateBinary(sessionDir string) (string, error) {
	path, _, err := ResolveGateBinaryWithSource(sessionDir)
	return path, err
}

// ResolveGateBinaryWithSource resolves the gate binary and returns
// which step found it. Resolution order:
//
//  1. $GATE_BIN — dev override, no extraction needed
//  2. embedded asset, extracted into sessionDir/gate/gate[.exe]
//  3. sibling-of-executable — `<app>-gate[.exe]` next to the running
//     binary. Covers the common dev / installer case where the
//     parent ships a sidecar binary in the same folder (matches
//     how `wick build` lays them out: both `bin/<app>[.exe]` +
//     `bin/<app>-gate-<os>-<arch>[.exe]`, the latter copied next
//     to the parent for installer packaging).
//  4. `<app>-gate` on PATH — last-ditch fallback for source builds
//     where neither override nor embed nor sibling are populated
func ResolveGateBinaryWithSource(sessionDir string) (path, source string, err error) {
	if p := strings.TrimSpace(os.Getenv(envOverride)); p != "" {
		return p, SourceEnvOverride, nil
	}
	if p, err := extractEmbeddedGate(sessionDir); err == nil {
		return p, SourceEmbed, nil
	} else if !errors.Is(err, errNoEmbeddedGate) {
		return "", "", err
	}
	if p := siblingGateBinary(); p != "" {
		return p, SourceSibling, nil
	}
	lookupName := brandedGateName()
	lookupName = strings.TrimSuffix(lookupName, ".exe")
	if p, err := exec.LookPath(lookupName); err == nil {
		return p, SourcePath, nil
	}
	return "", "", fmt.Errorf("gate binary %q not found: set %s, place %s next to the parent binary, or build the parent with the embed step", lookupName, envOverride, lookupName)
}

// siblingGateBinary returns the absolute path to the gate binary
// sitting in the same directory as the currently-running executable.
// Empty string when the file isn't there or os.Executable lookup
// fails — caller falls through to PATH lookup.
func siblingGateBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exe), brandedGateName())
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return ""
}

// extractEmbeddedGate writes the embedded binary into
// sessionDir/gate/gate[.exe] and returns the absolute path.
// Idempotent: if the file already exists with the same size as the
// embed, the extract is skipped.
//
// Internal extract path stays generic (`gate[.exe]`) — the user-
// facing brand only matters for sibling/PATH lookup, not for files
// claude's hook resolves via absolute path.
func extractEmbeddedGate(sessionDir string) (string, error) {
	name := embeddedGateName()
	data, err := embeddedGateFS.ReadFile(name)
	if err != nil {
		// Either the asset is missing or the FS is empty — both map
		// to "no embedded gate" so the caller falls back gracefully.
		if errors.Is(err, fs.ErrNotExist) {
			return "", errNoEmbeddedGate
		}
		return "", fmt.Errorf("read embedded gate %s: %w", name, err)
	}
	if len(data) == 0 {
		return "", errNoEmbeddedGate
	}

	gateDir := filepath.Join(sessionDir, "gate")
	if err := os.MkdirAll(gateDir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", gateDir, err)
	}
	out := filepath.Join(gateDir, "gate")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	// Skip rewrite if the on-disk file already matches the embed —
	// avoids fighting Windows ACLs on every spawn.
	if st, err := os.Stat(out); err == nil && st.Size() == int64(len(data)) {
		return out, nil
	}

	if err := os.WriteFile(out, data, 0o755); err != nil {
		return "", fmt.Errorf("write %s: %w", out, err)
	}
	return out, nil
}
