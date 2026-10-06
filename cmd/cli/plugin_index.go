package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/plugins/source"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// pluginIndexCmd writes a plugins.json v2 index for the zips `wick plugin
// build` produced. It is the per-release counterpart of `plugin catalog`: CI
// runs it next to the zips and uploads the result as a release asset, so a
// GitHub source (or a plain URL) can resolve every os/arch build without
// listing the whole repo. URLs stay relative to plugins.json unless
// --base-url is set.
func pluginIndexCmd() *cobra.Command {
	var (
		dir     string
		out     string
		baseURL string
		signKey string
		keys    []string
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Write plugins.json (v2) for built release zips",
		Long: `Scan --dir for <key>-<version>-<os>-<arch>.zip files and write a plugins.json
v2 index: one entry per plugin key, one asset per os/arch with its zip_sha256.

  wick plugin build --kind job auto_get_data --target linux/amd64,linux/arm64
  wick plugin index --dir bin --out bin/plugins.json

Asset URLs are relative to plugins.json (just the zip file name), which is what
a GitHub release asset needs. --base-url prefixes them for a static host.
--sign-key signs each zip_sha256 with an ed25519 key (same format as
'wick plugin build --sign-key'). --key keeps only the named plugin(s); use it
when one bin/ holds several plugins but the release is for one tag.`,
		RunE: func(c *cobra.Command, _ []string) error {
			entries, err := buildPluginIndex(dir, baseURL, signKey, keys)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if out == "" || out == "-" {
				_, err = os.Stdout.Write(data)
				return err
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote %d plugin(s) to %s\n", len(entries), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "bin", "Folder holding the release zips")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Output path (default: <dir>/plugins.json, '-' for stdout)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "Prefix for asset URLs (default: relative file names)")
	cmd.Flags().StringVar(&signKey, "sign-key", "", "Path to ed25519 private key; signs each zip_sha256")
	cmd.Flags().StringSliceVar(&keys, "key", nil, "Only index these plugin key(s)")
	cmd.PreRun = func(*cobra.Command, []string) {
		if out == "" {
			out = filepath.Join(dir, "plugins.json")
		}
	}
	return cmd
}

// buildPluginIndex reads every release zip in dir, takes key/kind/version/name
// from its plugin.json, and folds them into validated plugins.json entries.
func buildPluginIndex(dir, baseURL, signKey string, keys []string) ([]source.Entry, error) {
	zips, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	byKey := map[string]*source.Entry{}
	for _, z := range zips {
		raw, err := os.ReadFile(z)
		if err != nil {
			return nil, err
		}
		m, err := connplugin.ManifestFromZipBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(z), err)
		}
		key := m.Module.Meta.Key
		if len(want) > 0 && !want[key] {
			continue
		}
		if len(m.OSArch) != 1 {
			return nil, fmt.Errorf("%s: manifest must name exactly one os/arch, got %v", filepath.Base(z), m.OSArch)
		}
		osArch := m.OSArch[0]
		version := strings.TrimPrefix(strings.TrimSpace(m.Version), "v")
		wantName := fmt.Sprintf("%s-%s-%s.zip", key, version, strings.ReplaceAll(osArch, "/", "-"))
		if filepath.Base(z) != wantName {
			return nil, fmt.Errorf("%s: file name does not match its manifest (want %s)", filepath.Base(z), wantName)
		}
		sum := sha256.Sum256(raw)
		asset := source.Asset{URL: indexAssetURL(baseURL, wantName), ZipSHA256: hex.EncodeToString(sum[:])}
		if signKey != "" {
			if asset.Signature, err = wickplugin.SignSHA256(signKey, asset.ZipSHA256); err != nil {
				return nil, err
			}
		}
		e := byKey[key]
		if e == nil {
			e = &source.Entry{
				Key:          key,
				Kind:         wickplugin.NormalizeKind(m.Kind),
				Name:         m.Module.Meta.Name,
				Description:  m.Module.Meta.Description,
				Version:      version,
				ProtoVersion: m.ProtoVersion,
				Assets:       map[string]source.Asset{},
				DefaultTags:  m.Module.Meta.DefaultTags,
			}
			byKey[key] = e
		} else if e.Version != version {
			return nil, fmt.Errorf("%s: two versions in %s (%s and %s) — index one release at a time", key, dir, e.Version, version)
		}
		e.Assets[osArch] = asset
	}
	if len(byKey) == 0 {
		return nil, fmt.Errorf("no plugin zips found in %s", dir)
	}
	entries := make([]source.Entry, 0, len(byKey))
	for _, e := range byKey {
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	if err := source.ValidateIndex(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func indexAssetURL(baseURL, name string) string {
	if baseURL == "" {
		return name
	}
	return strings.TrimSuffix(baseURL, "/") + "/" + name
}

// pluginSignCmd signs release artifacts with an ed25519 key: a plugins.json
// gets every asset's zip_sha256 (re)signed in place; a .zip prints the
// signature of its sha256 so it can be pasted into a hand-written index.
func pluginSignCmd() *cobra.Command {
	var signKey string
	cmd := &cobra.Command{
		Use:   "sign <plugins.json|file.zip>...",
		Short: "Sign plugins.json assets or a release zip with an ed25519 key",
		Long: `Sign release artifacts with an ed25519 private key (base64, as written by
'go run ./cmd/plugin-keygen' / used by 'wick plugin build --sign-key').

  wick plugin sign --sign-key wick-plugin.key bin/plugins.json   # in place
  wick plugin sign --sign-key wick-plugin.key bin/x-1.0.0-linux-amd64.zip

For plugins.json, each asset's zip_sha256 is signed (the host verifies it
against the source's pinned key before extracting). For a zip, the sha256 of
the file is printed with its signature.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if signKey == "" {
				return errors.New("--sign-key is required")
			}
			for _, p := range args {
				if strings.HasSuffix(p, ".zip") {
					sum, err := fileSHA256Hex(p)
					if err != nil {
						return err
					}
					sig, err := wickplugin.SignSHA256(signKey, sum)
					if err != nil {
						return err
					}
					fmt.Printf("%s\tzip_sha256=%s\tsignature=%s\n", filepath.Base(p), sum, sig)
					continue
				}
				n, err := signIndexFile(p, signKey)
				if err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				fmt.Fprintf(os.Stderr, "signed %d asset(s) in %s\n", n, p)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&signKey, "sign-key", "", "Path to ed25519 private key (base64)")
	return cmd
}

// signIndexFile signs every asset that has a zip_sha256 and rewrites the file.
func signIndexFile(path, signKey string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var entries []source.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return 0, fmt.Errorf("parse plugins.json: %w", err)
	}
	n := 0
	for i := range entries {
		for oa, a := range entries[i].Assets {
			if a.ZipSHA256 == "" {
				return 0, fmt.Errorf("%s %s has no zip_sha256 — regenerate with 'wick plugin index'", entries[i].Key, oa)
			}
			if a.Signature, err = wickplugin.SignSHA256(signKey, a.ZipSHA256); err != nil {
				return 0, err
			}
			entries[i].Assets[oa] = a
			n++
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return 0, err
	}
	return n, os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
