package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Moli (github.com/lexmount/moli) is a headless browser kernel written in Rust
// — NOT a Chromium build. It has no --remote-debugging-port and Playwright
// cannot Launch it. Instead it runs as a protocol server (`moli serve`) that
// speaks CDP, and Playwright attaches with Chromium.ConnectOverCDP. So for this
// engine "launch" means: spawn `moli serve` on a free port, wait for
// /json/version, connect over CDP. Everything after the connect (contexts,
// pages, actions) is the same code path as the Chromium engines.
//
// Trade-offs vs Chromium, from the Moli README: no visible window (headless
// only), no extensions, no pixel-perfect Chrome parity, no full Canvas/WebGL or
// media playback. In return it starts in milliseconds and uses a fraction of
// the RAM, which suits scraping/reading pages on a small host.

const (
	moliEngine      = "moli"
	moliDefaultRepo = "lexmount/moli"
	// moliReadyTimeout bounds how long we wait for `moli serve` to answer
	// /json/version. It is usually ready in well under a second.
	moliReadyTimeout = 15 * time.Second
)

// isMoliEngine reports whether name selects the Moli engine.
func isMoliEngine(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), moliEngine)
}

// moliDir is where wick's own Moli download lives, next to the cloak one.
func moliDir(c *connector.Ctx) string {
	return filepath.Join(sessionDir(c), "moli")
}

func moliBinaryName() string {
	if runtime.GOOS == "windows" {
		return "moli.exe"
	}
	return "moli"
}

// moliBinaryPath returns the executable to run: the admin's moli_executable_path
// override if set, else the managed download. "" when nothing is installed.
func moliBinaryPath(c *connector.Ctx) string {
	if p := strings.TrimSpace(c.Cfg("moli_executable_path")); p != "" {
		return p
	}
	return findMoliBinary(moliDir(c))
}

// findMoliBinary locates the binary at the top of dir or one level deep (the
// release tarball wraps it in moli-v<ver>-<target>/).
func findMoliBinary(dir string) string {
	name := moliBinaryName()
	if p := filepath.Join(dir, name); fileExists(p) {
		return p
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			if p := filepath.Join(dir, e.Name(), name); fileExists(p) {
				return p
			}
		}
	}
	return ""
}

func moliInstalled(c *connector.Ctx) bool {
	p := moliBinaryPath(c)
	return p != "" && fileExists(p)
}

// moliVersion asks the binary itself (`moli --version` → "moli 1.1.17"). It is
// a plain CLI call, not a browser launch, so it is cheap enough for the status
// widget. Falls back to the release tag recorded at install time.
func moliVersion(c *connector.Ctx) string {
	if bin := moliBinaryPath(c); bin != "" {
		cmd := safeexec.Command(bin, "--version")
		// The plugin's own env, which the host already scrubbed at spawn.
		cmd.Env = cmd.Environ()
		if out, err := cmd.Output(); err == nil {
			if f := strings.Fields(string(out)); len(f) >= 2 {
				return f[len(f)-1]
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(moliDir(c), "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(b)), "v")
}

// moliAssetName is the release asset for this host, following lexmount's
// `moli-<rust-target>.<ext>` naming. "" for an unsupported host.
func moliAssetName() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	target := map[string]string{
		"linux":   "unknown-linux-gnu",
		"darwin":  "apple-darwin",
		"windows": "pc-windows-msvc",
	}[runtime.GOOS]
	if arch == "" || target == "" {
		return ""
	}
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("moli-%s-%s.%s", arch, target, ext)
}

// resolveMoliAsset reads the latest release of lexmount/moli and returns the
// tag + download URL of this host's asset. Unlike CloakHQ, Moli publishes every
// platform in every release, so /latest is enough.
func resolveMoliAsset(c *connector.Ctx) (tag, url, assetName string, err error) {
	want := moliAssetName()
	if want == "" {
		return "", "", "", fmt.Errorf("no Moli build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	api := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", moliDefaultRepo)
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, api, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("list releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", "", "", fmt.Errorf("GitHub releases API %d: %s", resp.StatusCode, string(body))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", "", fmt.Errorf("decode release: %w", err)
	}
	for _, a := range rel.Assets {
		if a.Name == want {
			return rel.TagName, a.URL, a.Name, nil
		}
	}
	return "", "", "", fmt.Errorf("no %s asset in %s %s", want, moliDefaultRepo, rel.TagName)
}

// latestMoli caches the newest tag for 1h so the status widget, which polls,
// stays well under GitHub's unauthenticated rate limit.
var (
	latestMoliMu   sync.Mutex
	latestMoliTag  string
	latestMoliWhen time.Time
)

func moliLatestVersion(c *connector.Ctx) string {
	latestMoliMu.Lock()
	if latestMoliTag != "" && time.Since(latestMoliWhen) < time.Hour {
		tag := latestMoliTag
		latestMoliMu.Unlock()
		return tag
	}
	latestMoliMu.Unlock()
	tag, _, _, err := resolveMoliAsset(c)
	if err != nil {
		clog.Warn().Err(err).Msg("moli: latest-version check failed")
		return ""
	}
	latestMoliMu.Lock()
	latestMoliTag, latestMoliWhen = tag, time.Now()
	latestMoliMu.Unlock()
	return tag
}

// moliUpdateAvailable reports whether a newer release exists than the managed
// download. An executable_path override is never "updatable" — we don't own it.
func moliUpdateAvailable(c *connector.Ctx) (bool, string) {
	if strings.TrimSpace(c.Cfg("moli_executable_path")) != "" || !moliInstalled(c) {
		return false, ""
	}
	latest := moliLatestVersion(c)
	if latest == "" {
		return false, ""
	}
	if strings.TrimPrefix(moliVersion(c), "v") == strings.TrimPrefix(latest, "v") {
		return false, latest
	}
	return true, latest
}

// installMoli downloads and extracts the Moli release for this host. The
// archive is ~45 MB, small enough to block the install RPC like the Playwright
// engines do, so there is no background goroutine / progress file here.
// Idempotent unless force is set (update path).
func installMoli(c *connector.Ctx, force bool) (string, error) {
	if !force && moliInstalled(c) {
		return moliVersion(c), nil
	}
	if strings.TrimSpace(c.Cfg("moli_executable_path")) != "" {
		return "", fmt.Errorf("moli uses a custom moli_executable_path — wick won't download over it; update that binary yourself")
	}
	tag, url, assetName, err := resolveMoliAsset(c)
	if err != nil {
		return "", err
	}
	// Extract into a fresh staging dir and swap it in, so a failed download or
	// update never leaves a half-written binary where a working one was.
	dir := moliDir(c)
	stage := dir + ".staging"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", fmt.Errorf("create moli dir: %w", err)
	}
	defer os.RemoveAll(stage)

	archive := filepath.Join(stage, assetName)
	if err := downloadPlain(c, url, archive); err != nil {
		return "", fmt.Errorf("download %s: %w", assetName, err)
	}
	if strings.HasSuffix(assetName, ".zip") {
		err = extractZip(archive, stage)
	} else {
		err = extractTarGz(archive, stage)
	}
	_ = os.Remove(archive)
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", assetName, err)
	}
	bin := findMoliBinary(stage)
	if bin == "" {
		return "", fmt.Errorf("extracted archive has no %s", moliBinaryName())
	}
	_ = os.Chmod(bin, 0o755)
	_ = os.WriteFile(filepath.Join(stage, "VERSION"), []byte(tag), 0o644)

	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("remove old moli: %w", err)
	}
	if err := os.Rename(stage, dir); err != nil {
		return "", fmt.Errorf("install moli: %w", err)
	}
	clog.Info().Str("tag", tag).Str("dir", dir).Msg("moli install: done")
	return strings.TrimPrefix(tag, "v"), nil
}

// downloadPlain streams url to dst without the cloak progress-file side effect
// that downloadFile carries.
func downloadPlain(c *connector.Ctx, url, dst string) error {
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// moliUninstall removes the managed download. An override path is left alone.
func moliUninstall(c *connector.Ctx) error {
	if strings.TrimSpace(c.Cfg("moli_executable_path")) != "" {
		return fmt.Errorf("moli uses a custom moli_executable_path — remove it manually; wick won't delete a path it didn't download")
	}
	return os.RemoveAll(moliDir(c))
}

// ── serve ────────────────────────────────────────────────────────────

// moliServeArgs builds the `moli serve` argv. profileDir is "" for a throwaway
// task run (in-memory state) and a user-data dir for a live session, passed in
// the `--profile-dir=<dir>` form so the orphan reaper can match it the same way
// it matches Chromium's --user-data-dir.
func moliServeArgs(c *connector.Ctx, port int, profileDir string) []string {
	args := []string{"serve", "--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	// Layout is what gives screenshots, element geometry and coordinate clicks.
	// Without it Moli keeps compatibility geometry only, so it defaults on.
	if v := strings.TrimSpace(c.Cfg("moli_layout")); v == "" || c.CfgBool("moli_layout") {
		args = append(args, "--layout")
	}
	if c.CfgBool("moli_resource") {
		args = append(args, "--resource")
	}
	if profileDir != "" {
		args = append(args, moliProfileFlag+profileDir)
	}
	return append(args, moliNetArgs(c)...)
}

// moliNetArgs are the request-side flags `moli serve` and `moli fetch` share:
// proxy, user agent, and the private-network / CIDR blocklist.
func moliNetArgs(c *connector.Ctx) []string {
	var args []string
	if px := strings.TrimSpace(c.Cfg("proxy_server")); px != "" {
		args = append(args, "--http-proxy", px)
		if bp := strings.TrimSpace(c.Cfg("proxy_bypass")); bp != "" {
			args = append(args, "--http-no-proxy", strings.ReplaceAll(bp, " ", ""))
		}
	}
	if ua := strings.TrimSpace(c.Cfg("user_agent")); ua != "" {
		args = append(args, "--user-agent", ua)
	}
	if c.CfgBool("moli_block_private_networks") {
		args = append(args, "--block-private-networks")
	}
	// One flag with a comma list: Moli rejects --block-cidrs given twice.
	if cidrs := strings.ReplaceAll(strings.TrimSpace(c.Cfg("moli_block_cidrs")), " ", ""); cidrs != "" {
		args = append(args, "--block-cidrs", cidrs)
	}
	return args
}

// ── fetch ────────────────────────────────────────────────────────────

// moliFetchArgs builds the `moli fetch` argv for get_markdown. No --layout:
// markdown and the semantic tree are read from the DOM, not from paint.
func moliFetchArgs(c *connector.Ctx, in getMarkdownInput) []string {
	args := []string{"fetch", "--dump", in.Format, "--wait", "done"}
	if in.WaitSelector != "" {
		args = append(args, "--wait-selector", in.WaitSelector)
	}
	args = append(args, moliNetArgs(c)...)
	return append(args, in.URL)
}

// moliFetch runs one `moli fetch` and returns its dump. A one-shot CLI call: no
// server, no CDP, no Playwright, which is what makes it much faster than
// get_content. Bounded by the navigation timeout plus a little for startup.
func moliFetch(c *connector.Ctx, in getMarkdownInput) (any, error) {
	bin := moliBinaryPath(c)
	if bin == "" || !fileExists(bin) {
		return nil, fmt.Errorf("moli is not installed: download it from the Browser card on this connector, or set moli_executable_path")
	}
	ctx, cancel := context.WithTimeout(c.Context(), time.Duration(navTimeout(c))*time.Millisecond+5*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, bin, moliFetchArgs(c, in)...)
	// The plugin's own env, which the host already scrubbed at spawn.
	cmd.Env = cmd.Environ()
	var stdout, stderr bytes.Buffer
	// clip keeps maxOutputRune runes; at most 4 bytes each, so this is enough.
	cmd.Stdout = &limitedWriter{w: &stdout, n: maxOutputRune*4 + 64}
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out: %w", ctx.Err())
		}
		msg := strings.TrimSpace(ansiRE.ReplaceAllString(stderr.String(), ""))
		if len(msg) > 1000 {
			msg = msg[len(msg)-1000:]
		}
		if msg == "" {
			msg = "(no output)"
		}
		return nil, fmt.Errorf("moli fetch %s: %v: %s", in.URL, err, msg)
	}
	return map[string]any{"url": in.URL, "format": in.Format, "content": clip(stdout.String())}, nil
}

// moliProfileFlag is the flag form that marks a Moli process as ours in the
// orphan sweep (see findOwnedBrowserPIDs).
const moliProfileFlag = "--profile-dir="

// freeLocalPort asks the kernel for an unused loopback port. There is a tiny
// window between closing the probe listener and Moli binding it; if another
// process grabs it Moli exits with a bind error and startMoli reports it.
func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// moliProc is a running `moli serve`.
type moliProc struct {
	cmd    *exec.Cmd
	cdpURL string
	exited chan struct{}
	stderr *bytes.Buffer // only for in-process (task) runs
}

// stop kills the server and waits for it to be reaped.
func (m *moliProc) stop() {
	if m == nil || m.cmd == nil || m.cmd.Process == nil {
		return
	}
	_ = m.cmd.Process.Kill()
	select {
	case <-m.exited:
	case <-time.After(3 * time.Second):
	}
}

// startMoli spawns `moli serve` and waits until its CDP endpoint answers.
// logPath "" keeps stderr in memory (task runs, so a startup failure can be
// reported); a path sends it to a file instead (live sessions, which outlive
// this call and must not hold a pipe to this process).
func startMoli(c *connector.Ctx, profileDir, logPath string) (*moliProc, error) {
	bin := moliBinaryPath(c)
	if bin == "" || !fileExists(bin) {
		return nil, fmt.Errorf("moli is not installed: download it from the Browser card on this connector, or set moli_executable_path")
	}
	port, err := freeLocalPort()
	if err != nil {
		return nil, fmt.Errorf("pick a port for moli: %w", err)
	}
	cmd := safeexec.Command(bin, moliServeArgs(c, port, profileDir)...)
	// The plugin's own env, which the host already scrubbed at spawn.
	cmd.Env = cmd.Environ()
	p := &moliProc{cmd: cmd, cdpURL: "http://127.0.0.1:" + strconv.Itoa(port), exited: make(chan struct{})}
	var logFile *os.File
	if logPath == "" {
		p.stderr = &bytes.Buffer{}
		cmd.Stderr = &limitedWriter{w: p.stderr, n: 8 << 10}
	} else if f, ferr := os.Create(logPath); ferr == nil {
		logFile = f
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("start moli: %w", err)
	}
	// Wait in the background: it reaps the child (no <defunct> zombie) and
	// tells the readiness loop below when Moli died on startup.
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		close(p.exited)
	}()

	deadline := time.Now().Add(moliReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			return nil, fmt.Errorf("moli exited on startup: %s", strings.TrimSpace(p.startupOutput(logPath)))
		default:
		}
		if cdpAlive(p.cdpURL) {
			return p, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.stop()
	return nil, fmt.Errorf("moli did not answer on %s within %s", p.cdpURL, moliReadyTimeout)
}

// startupOutput returns what Moli printed before dying, for the error message.
func (m *moliProc) startupOutput(logPath string) string {
	var s string
	if m.stderr != nil {
		s = m.stderr.String()
	} else if logPath != "" {
		b, _ := os.ReadFile(logPath)
		s = string(b)
	}
	s = ansiRE.ReplaceAllString(s, "")
	if len(s) > 1000 {
		s = s[len(s)-1000:]
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

// ansiRE strips the color codes Moli's logger emits.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// limitedWriter keeps at most n bytes and silently drops the rest, so a chatty
// server can't grow memory without bound during a long task run.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(b []byte) (int, error) {
	if l.n <= 0 {
		return len(b), nil
	}
	keep := b
	if len(keep) > l.n {
		keep = keep[:l.n]
	}
	_, err := l.w.Write(keep)
	l.n -= len(keep)
	return len(b), err
}

// touchProfileDir bumps the profile dir's mtime, which profile_list reports as
// last_used. Chromium rewrites files in the profile root on every launch; moli
// only writes into subdirs, so without this last_used stays at creation time.
func touchProfileDir(udd string, t time.Time) {
	_ = os.Chtimes(udd, t, t)
}
