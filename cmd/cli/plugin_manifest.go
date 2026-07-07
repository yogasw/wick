package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yogasw/wick/pkg/safeexec"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// dumpPluginManifest produces the plugin.json envelope for a plugin built for
// (goos, goarch).
//
// The manifest's Module + ProtoVersion come from the binary's own
// `--dump-manifest` output (single source of truth, no drift). But a
// cross-compiled binary cannot run on the build host, so we build a SECOND,
// host-arch helper binary from the same source purely to dump the manifest,
// then rewrite the distribution fields (Entry, OSArch, SHA256, Signature) to
// describe the REAL target binary at crossBinPath.
//
// When the requested target IS the host, we skip the helper build and dump from
// the already-built target binary directly.
func dumpPluginManifest(kind, srcDir, version, signKey, crossBinPath, goos, goarch, entryName string) ([]byte, error) {
	isHost := goos == runtime.GOOS && goarch == runtime.GOARCH

	dumpBin := crossBinPath
	if !isHost {
		helper, err := os.CreateTemp("", "wick-plugin-dump-*")
		if err != nil {
			return nil, fmt.Errorf("temp helper: %w", err)
		}
		helperPath := helper.Name()
		helper.Close()
		if runtime.GOOS == "windows" {
			os.Remove(helperPath)
			helperPath += ".exe"
		}
		defer os.Remove(helperPath)

		ldflags := "-X github.com/yogasw/wick/pkg/plugin.Version=" + version
		build := safeexec.Command("go", "build", "-ldflags", ldflags, "-o", helperPath, "./"+filepath.ToSlash(srcDir))
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		build.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
		if err := build.Run(); err != nil {
			return nil, fmt.Errorf("build host helper for manifest: %w", err)
		}
		dumpBin = helperPath
	}

	dumpArgs := []string{"--dump-manifest"}
	if signKey != "" {
		dumpArgs = append(dumpArgs, "--sign-key", signKey)
	}
	raw, err := safeexec.Command(dumpBin, dumpArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("run --dump-manifest: %w", err)
	}

	var m wickplugin.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse dumped manifest: %w", err)
	}

	// One identity rule (same as built-in connectors): the plugin's Meta.Key MUST
	// equal its folder name. Key is the slug used everywhere downstream — the zip
	// name, the install dir (DefaultDir/<key>), the runtime registry key, and the
	// catalog match — so a folder/key mismatch would silently drift (build a
	// "gworkspace-*.zip" that installs into ".../google_workspace/"). Fail loudly
	// at build time instead. Meta.Name stays free for display.
	folder := filepath.Base(srcDir)
	if m.Module.Meta.Key != folder {
		return nil, fmt.Errorf(
			"Meta.Key %q must equal the folder name %q (key is the slug used for the zip, install dir, and registry — rename one so they match; Meta.Name is the free display name)",
			m.Module.Meta.Key, folder)
	}
	if err := wickplugin.ValidateKey(m.Module.Meta.Key); err != nil {
		return nil, err
	}

	// Rewrite distribution fields to describe the real target binary.
	sum, err := sha256OfFile(crossBinPath)
	if err != nil {
		return nil, err
	}
	m.Kind = wickplugin.NormalizeKind(kind)
	m.Entry = entryName
	m.OSArch = []string{goos + "/" + goarch}
	m.SHA256 = sum
	m.Version = version
	// The helper signed the HOST hash; for a cross target that signature is over
	// the wrong sha256. Re-sign the real target's hash so VerifyManifest passes.
	if signKey != "" {
		sig, err := wickplugin.SignSHA256(signKey, sum)
		if err != nil {
			return nil, fmt.Errorf("sign target hash: %w", err)
		}
		m.Signature = sig
	} else {
		m.Signature = ""
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return out, nil
}

// sha256OfFile returns the hex sha256 of the file at path.
func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
