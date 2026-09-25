package agentmemory

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Installing a backend's binary from its GitHub release.
//
// Why this exists at all: until it did, "installed" meant "the user put it on
// PATH themselves", and a daemon running from somewhere PATH does not reach
// left the panel telling the truth about HTTP (the daemon answers) while every
// CLI-backed read said `executable file not found in $PATH`. Half the panel
// worked and the other half blamed the user.
//
// The shape follows airouter's install next door: one method that returns
// (output, error), a handler that reports both, and a button. What differs is
// the mechanism — airouter installs an npm package, and ai-memory ships
// per-arch release assets — so the download/verify discipline is the
// updater's: named asset, published checksum, size cap, atomic placement.
//
// Nothing here touches /usr/bin, sudo, or the user's PATH. The binary lands in
// a wick-owned directory and wick REMEMBERS the path; that memory is the point
// of the feature (see ResolveBackendBin).

// ErrInstallNotSupported is what a backend that wick cannot install answers.
// It is separate from a failed install: one is "this is not a thing wick
// does", the other is "it is, and it went wrong", and the panel says
// different sentences for them.
var ErrInstallNotSupported = errors.New("agentmemory: this backend is not installed by wick")

const (
	// maxAssetBytes caps a downloaded asset. The real ones are ~15 MiB; this
	// is loose enough for a much larger backend and tight enough that a
	// wrong URL cannot fill the disk of a 3.6 GB host.
	maxAssetBytes = 256 << 20
	// installHTTPTimeout bounds one HTTP call in the install, not the whole
	// install — a 15 MiB download on a slow link is normal, a stalled
	// connection is not.
	installHTTPTimeout = 5 * time.Minute
)

// githubAPIBase is where release metadata is read from. A var, not a const,
// so the install can be driven end to end against an httptest server — the
// alternative is a test that downloads 15 MiB from GitHub, which is a test
// that gets skipped.
var githubAPIBase = "https://api.github.com"

// hostGOOS and hostGOARCH name the machine wick is running on. Wrapped in
// functions for the same reason: a test installs for THIS host and has to be
// able to ask what that is.
func hostGOOS() string   { return runtime.GOOS }
func hostGOARCH() string { return runtime.GOARCH }

// ── where wick keeps what it installed ───────────────────────────────

var (
	binDirMu sync.RWMutex
	binDir   string
)

// SetBinDir names the directory wick installs backend binaries into. Called
// once at boot with a path under the agents base dir — the same place the
// agent-gate binary lives. Unset means wick installs nothing, which is the
// honest state for a process that was never told where its own files go.
func SetBinDir(dir string) {
	binDirMu.Lock()
	binDir = strings.TrimSpace(dir)
	binDirMu.Unlock()
}

// BinDir is the directory wick installs into, "" when unset.
func BinDir() string {
	binDirMu.RLock()
	defer binDirMu.RUnlock()
	return binDir
}

// managedBinPath is where wick's own copy of name lives, "" when wick has no
// directory of its own.
func managedBinPath(name string) string {
	dir := BinDir()
	if dir == "" || name == "" {
		return ""
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// ResolveBackendBin is the ONE place a backend binary is located.
//
// wick's own copy wins over PATH, and that order is the whole reason the
// install exists: after installing, every exec path — the version probe, the
// daemon spawn, the CLI-backed reads — finds the binary without the user
// editing a shell profile. PATH is still consulted, because a host that
// already had it installed keeps working exactly as before.
//
// Scattering exec.LookPath around is how the two halves of the panel came to
// disagree in the first place; everything goes through here.
func ResolveBackendBin(name string) (string, error) {
	if p := managedBinPath(name); p != "" && isExecutableFile(p) {
		return p, nil
	}
	return resolveOnPath(name)
}

// resolveOnPath is the PATH half, a var so a test can prove that the managed
// copy is preferred without depending on what this host happens to have.
var resolveOnPath = safeexec.ResolveBin

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// ── asset selection ──────────────────────────────────────────────────

// osNames / archNames map Go's names onto the release's. They are the
// project's own words, read off the published assets of ai-memory v2.4.0
// (2026-09-25): ai-memory-{linux,macos,windows}-{x86_64,aarch64}.
var (
	osNames   = map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}
	archNames = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}
)

// assetName is the release asset for one host, and the archive kind it is.
//
// An unsupported pair is an ERROR rather than a guess: downloading the
// closest-looking asset would install a binary that cannot run here, and the
// failure would surface much later as a daemon that never starts.
func assetName(bin, goos, goarch string) (name, kind string, err error) {
	o, ok := osNames[goos]
	if !ok {
		return "", "", fmt.Errorf("%s publishes no build for %s", bin, goos)
	}
	a, ok := archNames[goarch]
	if !ok {
		return "", "", fmt.Errorf("%s publishes no build for %s/%s", bin, goos, goarch)
	}
	// Windows ships a zip and only for x86_64; everything else is a tarball.
	if o == "windows" {
		if a != "x86_64" {
			return "", "", fmt.Errorf("%s publishes no windows build for %s", bin, goarch)
		}
		return fmt.Sprintf("%s-%s-%s.zip", bin, o, a), "zip", nil
	}
	return fmt.Sprintf("%s-%s-%s.tar.gz", bin, o, a), "tar.gz", nil
}

// ── the install ──────────────────────────────────────────────────────

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// releaseInstall is one install, with every outside dependency as a field so
// the whole of it runs against an httptest server in tests. A downloader that
// can only be tested by downloading is a downloader nobody tests.
type releaseInstall struct {
	repo    string // "owner/name"
	bin     string // the command name, and the name inside the archive
	dest    string // wick's bin dir
	goos    string
	goarch  string
	api     string // GitHub API base
	client  *http.Client
	logLine func(string) // progress, one line at a time
}

// run downloads, verifies and places the binary, returning the log of what it
// did — the same text airouter's install returns, and what the panel shows
// when something goes wrong.
func (ri releaseInstall) run(ctx context.Context) (string, error) {
	var log strings.Builder
	say := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		log.WriteString(line + "\n")
		if ri.logLine != nil {
			ri.logLine(line)
		}
	}

	if strings.TrimSpace(ri.dest) == "" {
		return "", errors.New("agentmemory: no install directory configured — wick does not know where to put the binary")
	}
	want, kind, err := assetName(ri.bin, ri.goos, ri.goarch)
	if err != nil {
		return log.String(), err
	}

	say("resolving the latest release of %s", ri.repo)
	rel, err := ri.latestRelease(ctx)
	if err != nil {
		return log.String(), err
	}

	asset, sum := pickAsset(rel.Assets, want)
	if asset == nil {
		return log.String(), fmt.Errorf("release %s has no asset named %q (it publishes: %s)", rel.TagName, want, assetList(rel.Assets))
	}
	say("downloading %s (%s) from %s", asset.Name, humanBytes(asset.Size), rel.TagName)
	blob, err := ri.download(ctx, asset.URL)
	if err != nil {
		return log.String(), err
	}

	// The checksum is verified BEFORE anything is written. A mismatched
	// archive is never unpacked, so a bad download cannot leave a
	// half-installed binary behind that the next status read calls
	// "installed".
	if sum != nil {
		raw, err := ri.download(ctx, sum.URL)
		if err != nil {
			return log.String(), fmt.Errorf("checksum %s: %w", sum.Name, err)
		}
		want := parseSHA256(string(raw))
		if want == "" {
			return log.String(), fmt.Errorf("checksum %s is not a sha256 line: %q", sum.Name, strings.TrimSpace(string(raw)))
		}
		got := sha256Hex(blob)
		if !strings.EqualFold(got, want) {
			return log.String(), fmt.Errorf("checksum mismatch for %s: the release says %s, the download is %s — nothing was installed", asset.Name, want, got)
		}
		say("verified sha256 %s", got)
	} else {
		// Loud on purpose. Every ai-memory asset publishes one, so its
		// absence is an anomaly worth seeing rather than a quiet skip.
		say("WARNING: the release publishes no checksum for %s — installed without verification", asset.Name)
	}

	exe, err := extractBinary(blob, kind, ri.bin)
	if err != nil {
		return log.String(), err
	}
	path, err := placeBinary(ri.dest, ri.bin, ri.goos, exe)
	if err != nil {
		return log.String(), err
	}
	say("installed %s (%s)", path, humanBytes(int64(len(exe))))
	return log.String(), nil
}

func (ri releaseInstall) latestRelease(ctx context.Context) (*ghRelease, error) {
	url := strings.TrimRight(ri.api, "/") + "/repos/" + ri.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ri.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// The rate limit is the failure an unauthenticated download meets
		// first, and it is fixable by waiting — so it is named rather than
		// left as a bare 403.
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("github rate limit reached (unauthenticated downloads are capped per IP) — try again later: %s", strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("github %d for %s: %s", resp.StatusCode, ri.repo, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github release metadata: %w", err)
	}
	return &rel, nil
}

func (ri releaseInstall) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := ri.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("download %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// One byte past the cap is read on purpose: it is how an oversized body
	// is told apart from one that happens to be exactly the cap.
	blob, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(blob)) > maxAssetBytes {
		return nil, fmt.Errorf("download is larger than %s — refusing", humanBytes(maxAssetBytes))
	}
	return blob, nil
}

func (ri releaseInstall) httpClient() *http.Client {
	if ri.client != nil {
		return ri.client
	}
	return &http.Client{Timeout: installHTTPTimeout}
}

// pickAsset finds the wanted asset and its published checksum sibling.
func pickAsset(assets []ghAsset, want string) (asset, sum *ghAsset) {
	for i := range assets {
		switch assets[i].Name {
		case want:
			asset = &assets[i]
		case want + ".sha256":
			sum = &assets[i]
		}
	}
	return
}

// assetList names what the release DOES publish, so a host with no build says
// so with the evidence attached rather than "not found".
func assetList(assets []ghAsset) string {
	names := make([]string, 0, len(assets))
	for _, a := range assets {
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, ", ")
}

// extractBinary pulls just the backend's executable out of the archive.
//
// Only that one entry is written anywhere — the archives also carry docs,
// templates and hook scripts, and unpacking a release into a wick directory
// would be taking on files nobody asked wick to own. It also makes path
// traversal a non-question: no archive-supplied path is ever used.
func extractBinary(blob []byte, kind, bin string) ([]byte, error) {
	switch kind {
	case "zip":
		return extractFromZip(blob, bin)
	default:
		return extractFromTarGz(blob, bin)
	}
}

func extractFromTarGz(blob []byte, bin string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("the download is not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != bin {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxAssetBytes))
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf("the archive contains no %q", bin)
}

func extractFromZip(blob []byte, bin string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, fmt.Errorf("the download is not a zip archive: %w", err)
	}
	for _, f := range zr.File {
		base := filepath.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		if base != bin && base != bin+".exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxAssetBytes))
		rc.Close()
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf("the archive contains no %q", bin)
}

// placeBinary writes the executable into wick's bin dir atomically: a
// temporary file in the same directory, then a rename. A half-written binary
// is never visible to the resolver, and replacing a running daemon's own file
// is a rename rather than a truncation.
func placeBinary(dir, bin, goos string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := bin
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", err
	}
	return final, nil
}

// ── small helpers ────────────────────────────────────────────────────

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// parseSHA256 reads the hash out of a checksum file. The published format is
// coreutils' "<hex>  <filename>"; a bare hash is accepted too.
func parseSHA256(text string) string {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		h := strings.ToLower(fields[0])
		if len(h) == 64 && isHex(h) {
			return h
		}
	}
	return ""
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
