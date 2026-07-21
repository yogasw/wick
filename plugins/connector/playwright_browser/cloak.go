package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yogasw/wick/pkg/connector"
)

// clog is the cloak-install logger. It writes to stderr because that's the
// stream hashicorp/go-plugin pipes back to the wick host — anything logged here
// shows up in the server log alongside the request that triggered it. Without
// this the whole background download was invisible: errors only ever landed in
// the progress file, and if the goroutine died before writing it, they vanished.
var clog = zerolog.New(os.Stderr).With().
	Timestamp().Str("component", "playwright.cloak").Logger()

// CloakBrowser is a patched, stealth Chromium published by CloakHQ. Unlike
// chromium/firefox/webkit it is NOT a Playwright-managed browser — there is no
// playwright.Install for it. The binary is downloaded from a GitHub release and
// launched via ExecutablePath with anti-automation flags (see cloakLaunchArgs).
//
// This file owns everything Cloak-specific: resolving the right release asset
// for the host OS/arch, downloading + extracting it, caching the binary, and
// reporting install state — so maintenance.go / service.go treat "cloakbrowser"
// as just another engine name.

const (
	cloakEngine      = "cloakbrowser"
	cloakDefaultRepo = "CloakHQ/CloakBrowser"
)

// cloakRepo is the owner/repo hosting the release assets. Overridable via the
// cloak_repo config so a fork / mirror can be pointed at without a rebuild.
func cloakRepo(c *connector.Ctx) string {
	if r := strings.TrimSpace(c.Cfg("cloak_repo")); r != "" {
		return r
	}
	return cloakDefaultRepo
}

// cloakDir is where the extracted Cloak binary lives — under the session dir so
// it shares the connector's data tree and survives plugin restarts.
func cloakDir(c *connector.Ctx) string {
	return filepath.Join(sessionDir(c), "cloakbrowser")
}

// cloakProgress is the download state the widget polls to draw a progress bar.
// It lives in a file (not memory) because the async install runs in a goroutine
// that may outlive the browser_install call, and a later browser_status poll —
// possibly served by a respawned plugin process — must still read it. Done and
// Error are terminal; the widget stops polling once either is set.
type cloakProgress struct {
	Phase string `json:"phase"` // "downloading" | "extracting" | "done" | "error"
	Pct   int    `json:"pct"`   // 0..100 (download bytes; extraction reports 100 + phase)
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

func cloakProgressPath(c *connector.Ctx) string {
	return filepath.Join(cloakDir(c), "cloak-progress.json")
}

// writeCloakProgress persists the current install state. Best-effort — a failed
// write just means the bar misses one frame.
func writeCloakProgress(c *connector.Ctx, p cloakProgress) {
	if b, err := json.Marshal(p); err == nil {
		_ = os.WriteFile(cloakProgressPath(c), b, 0o644)
	}
}

// readCloakProgress returns the current install state and whether a file exists.
func readCloakProgress(c *connector.Ctx) (cloakProgress, bool) {
	b, err := os.ReadFile(cloakProgressPath(c))
	if err != nil {
		return cloakProgress{}, false
	}
	var p cloakProgress
	if json.Unmarshal(b, &p) != nil {
		return cloakProgress{}, false
	}
	return p, true
}

// cloakInstalling reports whether a download/extract is currently in flight
// (a progress file exists that is neither done nor errored).
func cloakInstalling(c *connector.Ctx) bool {
	p, ok := readCloakProgress(c)
	return ok && !p.Done && p.Error == ""
}

// cloakBinaryPath returns the resolved Cloak executable: the admin's explicit
// cloak_executable_path override if set, else the cached download location.
// Existence is the caller's concern (fileExists).
func cloakBinaryPath(c *connector.Ctx) string {
	if p := strings.TrimSpace(c.Cfg("cloak_executable_path")); p != "" {
		return p
	}
	return filepath.Join(cloakDir(c), cloakBinaryName())
}

// cloakBinaryName is the Chromium executable name inside the extracted archive.
func cloakBinaryName() string {
	if runtime.GOOS == "windows" {
		return "chrome.exe"
	}
	return "chrome"
}

// cloakInstalled reports whether a usable Cloak binary is present.
func cloakInstalled(c *connector.Ctx) bool {
	// Explicit override: trust the given path.
	if p := strings.TrimSpace(c.Cfg("cloak_executable_path")); p != "" {
		return fileExists(p)
	}
	// Cached download: the binary may sit at the top level or one dir deep
	// depending on how the archive was packed; locate it lazily.
	bin := findCloakBinary(cloakDir(c))
	return bin != ""
}

// cloakVersion is best-effort: Cloak binaries don't expose a cheap version
// probe without launching, and launching a stealth browser just to read a
// version is wasteful — so we surface the release tag recorded at install time.
func cloakVersion(c *connector.Ctx) string {
	b, err := os.ReadFile(filepath.Join(cloakDir(c), "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ── asset resolution (GitHub) ────────────────────────────────────────

// ghRelease is the slice of the GitHub releases API we read.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// cloakAssetName is the release asset filename for the host OS/arch, following
// CloakHQ's pattern `cloakbrowser-<os>-<arch>.<ext>` (windows → .zip, others →
// .tar.gz). Returns "" for an unsupported host.
func cloakAssetName() string {
	osPart := map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	archPart := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if osPart == "" || archPart == "" {
		return ""
	}
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("cloakbrowser-%s-%s.%s", osPart, archPart, ext)
}

// resolveCloakAsset loops the repo's releases (newest first) and returns the
// first one carrying an asset for this host, as (tag, downloadURL). Because
// CloakHQ publishes different platforms in different releases, we must NOT rely
// on /latest — we scan until a matching asset appears.
func resolveCloakAsset(c *connector.Ctx) (tag, url, assetName string, err error) {
	want := cloakAssetName()
	if want == "" {
		return "", "", "", fmt.Errorf("no CloakBrowser build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	api := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=50", cloakRepo(c))
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, api, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The common failure on locked-down hosts: DNS/connectivity to
		// api.github.com. Log the raw error so it's obvious it's a network
		// problem, not a bug in the release-scanning logic.
		clog.Error().Err(err).Str("api", api).Msg("cloak install: GitHub releases API unreachable")
		return "", "", "", fmt.Errorf("list releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		clog.Error().Int("status", resp.StatusCode).Str("body", string(body)).
			Msg("cloak install: GitHub releases API non-200 (rate limit?)")
		return "", "", "", fmt.Errorf("GitHub releases API %d: %s", resp.StatusCode, string(body))
	}
	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", "", "", fmt.Errorf("decode releases: %w", err)
	}
	for _, rel := range releases {
		for _, a := range rel.Assets {
			if a.Name == want {
				return rel.TagName, a.URL, a.Name, nil
			}
		}
	}
	clog.Error().Str("want", want).Int("releases_scanned", len(releases)).
		Msg("cloak install: no matching asset in any release")
	return "", "", "", fmt.Errorf("no %s asset in any release of %s", want, cloakRepo(c))
}

// ── download + extract ───────────────────────────────────────────────

// startCloakInstall runs installCloak in the background so the browser_install
// RPC can return immediately. The goroutine gets a FRESH context detached from
// the manager call (which is cancelled the moment the RPC returns) plus a copy
// of this call's configs, so a long download isn't killed mid-flight. Progress
// is observed through the state file, not the returned error.
func startCloakInstall(c *connector.Ctx) {
	cfgs := c.Configs() // snapshot the config map
	dir := cloakDir(c)
	clog.Info().Str("dir", dir).Str("os", runtime.GOOS).Str("arch", runtime.GOARCH).
		Msg("cloak install: launching background goroutine")
	go func() {
		// A panic in the goroutine would otherwise kill the whole plugin
		// subprocess silently and leave no progress file behind — the exact
		// "nothing happened" symptom. Recover, log it, and record it.
		defer func() {
			if r := recover(); r != nil {
				clog.Error().Interface("panic", r).Msg("cloak install: goroutine panicked")
				writeCloakProgress(c, cloakProgress{Phase: "error", Done: true, Error: fmt.Sprintf("install panicked: %v", r)})
			}
		}()
		bg := connector.NewPluginCtx(context.Background(), cfgs, map[string]string{})
		if err := installCloak(bg); err != nil {
			clog.Error().Err(err).Msg("cloak install: failed")
			return
		}
		clog.Info().Msg("cloak install: completed")
	}()
}

// installCloak downloads and extracts the CloakBrowser binary for this host,
// writing progress to the state file as it goes so the widget can draw a bar.
// Idempotent — a present binary short-circuits. Runs in its own context (not
// the short manager call context) so a ~200MB download isn't killed when the
// browser_install RPC returns; see browserInstall's async path.
func installCloak(c *connector.Ctx) error {
	dir := cloakDir(c)
	if cloakInstalled(c) {
		clog.Info().Str("dir", dir).Msg("cloak install: already installed, nothing to do")
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		clog.Error().Err(err).Str("dir", dir).Msg("cloak install: cannot create dir")
		return fmt.Errorf("create cloak dir: %w", err)
	}
	fail := func(err error) error {
		clog.Error().Err(err).Msg("cloak install: aborting")
		writeCloakProgress(c, cloakProgress{Phase: "error", Done: true, Error: err.Error()})
		return err
	}

	writeCloakProgress(c, cloakProgress{Phase: "downloading", Pct: 0})
	clog.Info().Str("repo", cloakRepo(c)).Str("want", cloakAssetName()).
		Msg("cloak install: resolving release asset")
	tag, url, assetName, err := resolveCloakAsset(c)
	if err != nil {
		return fail(err)
	}
	clog.Info().Str("tag", tag).Str("asset", assetName).Str("url", url).
		Msg("cloak install: asset resolved, downloading")

	archive := filepath.Join(dir, assetName)
	if err := downloadFile(c, url, archive); err != nil {
		return fail(fmt.Errorf("download %s: %w", assetName, err))
	}
	defer os.Remove(archive)
	clog.Info().Str("asset", assetName).Msg("cloak install: download done, extracting")

	writeCloakProgress(c, cloakProgress{Phase: "extracting", Pct: 100})
	if strings.HasSuffix(assetName, ".zip") {
		err = extractZip(archive, dir)
	} else {
		err = extractTarGz(archive, dir)
	}
	if err != nil {
		return fail(fmt.Errorf("extract %s: %w", assetName, err))
	}
	if findCloakBinary(dir) == "" {
		return fail(fmt.Errorf("extracted archive has no %s", cloakBinaryName()))
	}
	_ = os.WriteFile(filepath.Join(dir, "VERSION"), []byte(tag), 0o644)
	writeCloakProgress(c, cloakProgress{Phase: "done", Pct: 100, Done: true})
	clog.Info().Str("tag", tag).Str("dir", dir).Msg("cloak install: done")
	return nil
}

// downloadFile streams url to dst, honoring the call context so a cancel aborts
// the (large, ~200MB) download instead of leaking. It updates the cloak progress
// file with the byte percentage as it copies, using Content-Length when known.
func downloadFile(c *connector.Ctx, url, dst string) error {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		clog.Error().Err(err).Str("url", url).Msg("cloak install: download request failed (network?)")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		clog.Error().Int("status", resp.StatusCode).Str("url", url).Msg("cloak install: download non-200")
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength // -1 when unknown
	pr := &progressReader{
		r:     resp.Body,
		total: total,
		onPct: func(pct int) { writeCloakProgress(c, cloakProgress{Phase: "downloading", Pct: pct}) },
	}
	_, err = io.Copy(f, pr)
	return err
}

// progressReader wraps a body and reports the download percentage as bytes
// flow, throttled to whole-percent changes so it doesn't hammer the disk. When
// total is unknown (<=0) it leaves pct at 0 (the bar shows an indeterminate
// "downloading" state instead).
type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	lastPct int
	onPct   func(pct int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.total > 0 {
		p.read += int64(n)
		pct := int(p.read * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		if pct != p.lastPct {
			p.lastPct = pct
			if p.onPct != nil {
				p.onPct(pct)
			}
		}
	}
	return n, err
}

// findCloakBinary walks dir (up to a few levels) for the chrome executable.
// Archives may nest the binary under a top-level folder, so a plain join isn't
// enough. Returns "" if not found.
func findCloakBinary(dir string) string {
	name := cloakBinaryName()
	var found string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && d.Name() == name {
			found = path
			return io.EOF // stop early
		}
		return nil
	})
	return found
}

// extractTarGz unpacks a .tar.gz into dir, preserving executable bits.
func extractTarGz(archive, dir string) error {
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
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFileFrom(tr, target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}
}

// extractZip unpacks a .zip into dir, preserving executable bits.
func extractZip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target, err := safeJoin(dir, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFileFrom(rc, target, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFileFrom copies r into path with mode, ensuring the executable bit
// survives (chrome must stay runnable after extraction).
func writeFileFrom(r io.Reader, path string, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o111)
	if err != nil {
		// Fall back to a sane mode if the header mode was odd.
		out, err = os.Create(path)
		if err != nil {
			return err
		}
	}
	defer out.Close()
	_, err = io.Copy(out, r)
	return err
}

// safeJoin joins base + name and rejects paths that escape base (zip-slip /
// tar traversal guard).
func safeJoin(base, name string) (string, error) {
	target := filepath.Join(base, name)
	if !strings.HasPrefix(target, filepath.Clean(base)+string(os.PathSeparator)) && target != filepath.Clean(base) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return target, nil
}

// ── launch flags ─────────────────────────────────────────────────────

// cloakLaunchArgs are the anti-automation launch tweaks CloakBrowser needs. The
// stealth is compiled into the binary; these just avoid re-flagging automation.
var cloakLaunchArgs = struct {
	IgnoreDefaultArgs []string
	Args              []string
}{
	IgnoreDefaultArgs: []string{"--enable-automation"},
	Args:              []string{"--no-sandbox"},
}
