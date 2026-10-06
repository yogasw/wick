package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// HostOSArch is this server's "<goos>/<goarch>".
func HostOSArch() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Installed describes a verified (and, unless dry-run, installed) plugin.
type Installed struct {
	Key       string
	Kind      string
	Version   string
	ZipSHA256 string
}

// InstallOptions tunes one install. Root maps a kind to its install folder
// (connplugin.KindDir by default; tests point it at a temp dir).
type InstallOptions struct {
	DryRun   bool
	Root     func(kind string) string
	Progress connplugin.ProgressFunc
}

func (o InstallOptions) root(kind string) string {
	if o.Root != nil {
		return o.Root(kind)
	}
	return connplugin.KindDir(kind)
}

// InstallEntry downloads the host build of e and installs it after the full
// check chain: zip_sha256 → signature (when pinned or required) → extract →
// manifest kind/arch/proto/binary sha256 → atomic copy into the kind folder.
func (c *Client) InstallEntry(ctx context.Context, src *entity.PluginSource, e Entry, opts InstallOptions) (Installed, error) {
	a, ok := e.Assets[HostOSArch()]
	if !ok || a.URL == "" {
		return Installed{}, fmt.Errorf("%s v%s has no build for %s", e.Key, e.Version, HostOSArch())
	}
	tmp, err := os.MkdirTemp("", "wick-plugin-src-*")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "download")
	sum, err := c.downloadZip(ctx, src, a, archive, opts.Progress)
	if err != nil {
		return Installed{}, err
	}
	return installArchive(archive, sum, a, e.Kind, pinnedKeys(src), opts)
}

// InstallZip installs an uploaded zip (no index, so no zip_sha256 to match);
// the manifest checks still apply.
func InstallZip(path string, opts InstallOptions) (Installed, error) {
	sum, err := fileSHA256(path)
	if err != nil {
		return Installed{}, err
	}
	return installArchive(path, sum, Asset{}, "", nil, opts)
}

func installArchive(archive, sum string, a Asset, wantKind string, pinned []string, opts InstallOptions) (Installed, error) {
	pf := opts.Progress
	if pf != nil {
		pf(connplugin.Progress{Phase: connplugin.PhaseVerifying, Pct: 100})
	}
	if a.ZipSHA256 != "" && !strings.EqualFold(a.ZipSHA256, sum) {
		return Installed{}, fmt.Errorf("zip_sha256 mismatch: got %s, index says %s", sum, a.ZipSHA256)
	}
	if err := checkSignature(a, sum, pinned); err != nil {
		return Installed{}, err
	}
	tmp, err := os.MkdirTemp("", "wick-plugin-x-*")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(tmp)
	dir, err := connplugin.ExtractArchive(archive, tmp)
	if err != nil {
		return Installed{}, err
	}
	return installDir(dir, sum, wantKind, opts)
}

// InstallDir installs an already-extracted {binary, plugin.json} directory
// (the CLI's path/archive/URL/registry sources) through the same manifest
// checks and kind routing as an upload.
func InstallDir(dir string, opts InstallOptions) (Installed, error) {
	return installDir(dir, "", "", opts)
}

// InstallPath installs from a local directory, a local .zip / .tar.gz, or a
// direct http(s) archive URL — the `plugin install` CLI sources — routing the
// plugin into its kind folder like an upload.
func InstallPath(ctx context.Context, src string, opts InstallOptions) (Installed, error) {
	dir, cleanup, err := connplugin.ResolveSource(ctx, src)
	if err != nil {
		return Installed{}, err
	}
	defer cleanup()
	return InstallDir(dir, opts)
}

// installDir checks the manifest in dir (kind, arch, proto, binary sha256)
// and copies it into the folder of its kind.
func installDir(dir, sum, wantKind string, opts InstallOptions) (Installed, error) {
	pf := opts.Progress
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return Installed{}, fmt.Errorf("read plugin.json: %w", err)
	}
	var m wickplugin.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Installed{}, fmt.Errorf("parse plugin.json: %w", err)
	}
	// NormalizeKind maps an unknown kind to connector — reject it here so a
	// typo'd or future kind is not silently installed as a connector.
	if m.Kind != "" && wickplugin.NormalizeKind(m.Kind) != m.Kind {
		return Installed{}, fmt.Errorf("plugin.json has unknown kind %q (want one of %s)", m.Kind, strings.Join(wickplugin.Kinds, ", "))
	}
	kind := wickplugin.NormalizeKind(m.Kind)
	if wantKind != "" && wickplugin.NormalizeKind(wantKind) != kind {
		return Installed{}, fmt.Errorf("index says kind %q but plugin.json says %q", wantKind, kind)
	}
	entry := m.Entry
	if entry == "" {
		entry = m.Module.Meta.Key
	}
	if err := wickplugin.VerifyManifest(m, filepath.Join(dir, entry)); err != nil {
		return Installed{}, fmt.Errorf("verify %q: %w", m.Module.Meta.Key, err)
	}
	out := Installed{Key: m.Module.Meta.Key, Kind: kind, Version: m.Version, ZipSHA256: sum}
	if opts.DryRun {
		return out, nil
	}
	if pf != nil {
		pf(connplugin.Progress{Phase: connplugin.PhaseReplacing, Pct: 100})
	}
	if err := connplugin.InstallFromDir(dir, opts.root(kind)); err != nil {
		return Installed{}, err
	}
	if pf != nil {
		pf(connplugin.Progress{Phase: connplugin.PhaseDone, Pct: 100})
	}
	return out, nil
}

// pinnedKeys returns the source's pinned publisher key, if any.
func pinnedKeys(src *entity.PluginSource) []string {
	if src == nil || strings.TrimSpace(src.PubKey) == "" {
		return nil
	}
	return []string{strings.TrimSpace(src.PubKey)}
}

// checkSignature enforces the index signature over zip_sha256: required when
// the source pins a key or the host requires signatures; otherwise a present
// signature must still match a trusted key.
func checkSignature(a Asset, sum string, pinned []string) error {
	keys := append(append([]string(nil), pinned...), wickplugin.TrustedKeys()...)
	required := len(pinned) > 0 || wickplugin.RequireSig()
	if a.Signature == "" {
		// An upload has no index entry (zero Asset); its manifest signature
		// is still enforced by VerifyManifest.
		if required && a.URL != "" {
			return fmt.Errorf("signature required but the index entry is unsigned")
		}
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	if !wickplugin.VerifySHA256(keys, sum, a.Signature) {
		return fmt.Errorf("signature verification failed for zip %s", sum[:12])
	}
	return nil
}

// downloadZip streams the asset to dst (100 MB cap), returning its sha256.
// Private GitHub assets go through the API endpoint with the PAT.
func (c *Client) downloadZip(ctx context.Context, src *entity.PluginSource, a Asset, dst string, pf connplugin.ProgressFunc) (string, error) {
	u, accept := a.URL, ""
	if src != nil && src.Type == TypeGitHub && src.Private && a.APIURL != "" {
		u, accept = a.APIURL, "application/octet-stream"
	}
	if pf != nil {
		pf(connplugin.Progress{Phase: connplugin.PhaseDownloading, Pct: 0})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	c.authorize(req, src)
	resp, err := c.hc().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Code: resp.StatusCode, URL: redact(u)}
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxZipBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxZipBytes {
		return "", fmt.Errorf("zip exceeds %d MB", maxZipBytes>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
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
