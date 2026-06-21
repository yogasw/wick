package app

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// installPlugin verifies the {binary, plugin.json} in srcDir and copies them
// into destRoot/<key>/. destRoot defaults to connplugin.DefaultDir().
func installPlugin(srcDir, destRoot string) error {
	raw, err := os.ReadFile(filepath.Join(srcDir, "plugin.json"))
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	var env wickplugin.Manifest
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("parse plugin.json: %w", err)
	}
	key := env.Module.Meta.Key
	if key == "" {
		return fmt.Errorf("manifest missing module key")
	}
	entry := env.Entry
	if entry == "" {
		entry = key
	}
	binSrc := filepath.Join(srcDir, entry)
	if err := wickplugin.VerifyManifest(env, binSrc); err != nil {
		return fmt.Errorf("verify %q: %w", key, err)
	}
	destDir := filepath.Join(destRoot, key)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}
	if err := copyFile(binSrc, filepath.Join(destDir, entry), 0o755); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := copyFile(filepath.Join(srcDir, "plugin.json"), filepath.Join(destDir, "plugin.json"), 0o644); err != nil {
		return fmt.Errorf("copy manifest: %w", err)
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
}

// resolveSource turns a path/url/archive into a directory containing
// {binary, plugin.json}. Returns the dir and a cleanup func.
func resolveSource(src string) (string, func(), error) {
	noop := func() {}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		tmp, err := os.MkdirTemp("", "wick-plugin-dl-*")
		if err != nil {
			return "", noop, err
		}
		archive := filepath.Join(tmp, "download")
		if err := downloadTo(src, archive); err != nil {
			os.RemoveAll(tmp)
			return "", noop, err
		}
		dir, err := extractArchive(archive, tmp)
		if err != nil {
			os.RemoveAll(tmp)
			return "", noop, err
		}
		return dir, func() { os.RemoveAll(tmp) }, nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", noop, err
	}
	if info.IsDir() {
		return src, noop, nil
	}
	tmp, err := os.MkdirTemp("", "wick-plugin-x-*")
	if err != nil {
		return "", noop, err
	}
	dir, err := extractArchive(src, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return "", noop, err
	}
	return dir, func() { os.RemoveAll(tmp) }, nil
}

func downloadTo(url, dst string) error {
	resp, err := http.Get(url) //nolint:gosec // user-provided plugin source
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// extractArchive extracts a .tar.gz or .zip into destBase and returns the
// directory that holds plugin.json (the archive root or its single subdir).
func extractArchive(archive, destBase string) (string, error) {
	out := filepath.Join(destBase, "x")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(archive, ".zip"):
		if err := extractZip(archive, out); err != nil {
			return "", err
		}
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"), strings.HasSuffix(archive, "download"):
		if err := extractTarGz(archive, out); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported archive (use .tar.gz or .zip): %s", archive)
	}
	return findManifestDir(out)
}

func findManifestDir(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
		return root, nil
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "plugin.json")); err == nil {
				return filepath.Join(root, e.Name()), nil
			}
		}
	}
	return "", fmt.Errorf("plugin.json not found in archive")
}

func extractTarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.Clean("/"+h.Name)) // zip-slip guard
		if h.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil { //nolint:gosec // bounded by archive
			w.Close()
			return err
		}
		w.Close()
	}
}

func extractZip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(dst, filepath.Clean("/"+zf.Name)) // zip-slip guard
		if zf.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc) //nolint:gosec // bounded by archive
		w.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Manage connector plugins"}
	cmd.AddCommand(pluginInstallCmd(), pluginListCmd(), pluginRemoveCmd())
	return cmd
}

func pluginInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <path|url>",
		Short: "Install a connector plugin from a local path, archive, or URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir, cleanup, err := resolveSource(args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			if err := installPlugin(dir, connplugin.DefaultDir()); err != nil {
				return err
			}
			fmt.Println("plugin installed; a running wick will pick it up shortly")
			return nil
		},
	}
}

func pluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed connector plugins",
		RunE: func(_ *cobra.Command, _ []string) error {
			found, err := connplugin.Scan(connplugin.DefaultDir())
			if err != nil {
				return err
			}
			host := runtime.GOOS + "/" + runtime.GOARCH
			for _, f := range found {
				signed := "none"
				if f.Manifest.Signature != "" {
					if wickplugin.VerifySHA256(wickplugin.TrustedKeys(), f.Manifest.SHA256, f.Manifest.Signature) {
						signed = "valid"
					} else {
						signed = "INVALID"
					}
				}
				archOK := "no"
				for _, a := range f.Manifest.OSArch {
					if a == host {
						archOK = "yes"
					}
				}
				fmt.Printf("%-20s %-12s arch:%s signed:%s\n", f.Key, f.Manifest.Version, archOK, signed)
			}
			return nil
		},
	}
}

func pluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <key>",
		Short: "Remove an installed connector plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			found, err := connplugin.Scan(connplugin.DefaultDir())
			if err != nil {
				return err
			}
			for _, f := range found {
				if f.Key == args[0] {
					dir := filepath.Dir(f.BinaryPath)
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
					fmt.Printf("removed plugin %q\n", args[0])
					return nil
				}
			}
			return fmt.Errorf("plugin %q not installed", args[0])
		},
	}
}
