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

	"github.com/yogasw/wick/internal/appname"
)

//go:embed all:assets
var embeddedGateFS embed.FS

// AppName returns the active app brand. Gate sidecars derive the brand
// from their own executable name (wick-lab-gate.exe → "wick-lab") so
// socket/spec paths land under the correct ~/.<app>/ tree even when no
// ldflag, APP_NAME env, or wick.yml is present. appname.Resolve() is
// used only when the exe-derived name is empty or equals the bare
// default.
func AppName() string {
	if exe, err := os.Executable(); err == nil {
		base := strings.TrimSuffix(filepath.Base(exe), ".exe")
		// Only derive from exe name for explicit gate sidecars (must carry
		// the -gate suffix). Non-gate binaries (server "lab", "wick-lab",
		// embedded "gate") must fall through to appname.Resolve() so the
		// BuildAppName ldflag — baked into both server and gate builds — is
		// the single source of truth regardless of how the binary is named.
		if stem, ok := strings.CutSuffix(base, "-gate"); ok && stem != "" {
			return stem
		}
	}
	return appname.Resolve()
}

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

// brandedGateName returns the sibling/PATH lookup name `<exe>-gate[.exe]`,
// where `<exe>` is the running executable's basename minus `.exe`.
//
// Why exe-derived (not appname.Resolve()): sibling lookup is a *filesystem*
// neighbor check — gate binary lives next to its parent and shares the
// parent's filename stem. AppName (brand) is a separate concept used for
// `~/.<app>/` paths; an operator can run `wick-lab.exe` with
// `APP_NAME=my-app` and we still want to find `bin/wick-lab-gate.exe`,
// not `bin/my-app-gate.exe` that doesn't exist.
//
// Falls back to `gate[.exe]` when os.Executable() fails so PATH-only
// deployments still resolve.
func brandedGateName() string {
	base := "gate"
	if exe, err := os.Executable(); err == nil {
		stem := strings.TrimSuffix(filepath.Base(exe), ".exe")
		stem = strings.TrimSuffix(stem, "-gate")
		if stem != "" {
			base = stem + "-gate"
		}
	}
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	return base
}

// Resolution source labels — exposed via ResolveGateBinaryWithSource
// so the Providers page UI can show *how* the binary got picked,
// useful when debugging why one source silently shadowed another.
const (
	SourceSibling = "sibling"
	SourceEmbed   = "embed"
	SourcePath    = "path"
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
//  1. sibling-of-executable — `<app>-gate[.exe]` next to the running
//     binary. This is the production path: `wick build` ships the
//     sidecar in every installer (.msi / .deb / .app), so the
//     installed app always finds it here without touching disk for
//     an extract.
//  2. embedded asset, extracted into sessionDir/gate/gate[.exe].
//     Backup for portable .exe builds (no installer) and for source
//     builds where someone ran `wick build` once but discarded the
//     sibling artifact.
//  3. `<app>-gate` on PATH — last-ditch fallback for unusual setups
//     (e.g. user installed the gate via a separate package manager).
//
// No env-var override and no ldflag injection: AppName comes from
// the running executable's own filename, so resolution is purely a
// function of what's on disk.
func ResolveGateBinaryWithSource(sessionDir string) (path, source string, err error) {
	if p := siblingGateBinary(); p != "" {
		return p, SourceSibling, nil
	}
	if p, err := extractEmbeddedGate(sessionDir); err == nil {
		return p, SourceEmbed, nil
	} else if !errors.Is(err, errNoEmbeddedGate) {
		return "", "", err
	}
	lookupName := brandedGateName()
	lookupName = strings.TrimSuffix(lookupName, ".exe")
	if p, err := exec.LookPath(lookupName); err == nil {
		return p, SourcePath, nil
	}
	return "", "", fmt.Errorf("gate binary %q not found: build the app with `wick build` (sibling+embed both produced) or place %s on PATH", lookupName, lookupName)
}

// siblingGateBinary returns the absolute path to the gate binary
// sitting in the same directory as the currently-running executable.
// Empty string when the file isn't there or os.Executable lookup
// fails — caller falls through to embed/PATH lookup.
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
