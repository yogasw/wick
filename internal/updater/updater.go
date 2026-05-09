// Package updater downloads, verifies, and applies new release
// binaries from GitHub. The current binary embeds a target release
// repo + PAT (set at build time via `wick build --release-github-repo ...`
// and `--release-github-pat ...`); at startup the system tray asks this
// package whether a staged update is pending (apply + re-exec) or
// whether to fetch a newer release in the background.
//
// Asset naming convention (must match the release CI workflow):
//
//	<appName>-darwin-<GOARCH>.dmg              macOS disk image
//	<appName>-linux-<GOARCH>.deb               Debian package
//	<appName>-windows-<GOARCH>.exe             Windows binary
//	<asset>.sha256                             checksum sibling
//
// The downloaded asset is extracted to its inner binary (per-OS via
// extractStaged) before being written to the staged path; .exe is a
// pass-through.
//
// Repo resolution:
//
//	1. repoFull arg ("owner/repo"), typically baked from --release-github-repo
//	2. fallback to debug.ReadBuildInfo() Main.Path when arg is empty
//	   (lets a "same source repo as releases" setup work without a flag)
//	3. else updater is disabled — Configured() returns false and
//	   CheckNow returns an error.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/mod/semver"

	"github.com/yogasw/wick/internal/userconfig"
)

const (
	githubAPI   = "https://api.github.com"
	httpTimeout = 60 * time.Second
)

// Updater is safe for concurrent use. CheckNow guards itself with a
// "in flight" flag so background and manual triggers don't double-fire.
type Updater struct {
	appName        string
	currentVersion string
	owner, repo    string
	pat            string
	cacheDir       string

	cfg     *userconfig.Config
	saveCfg func() error

	mu       sync.Mutex
	checking bool
}

// Result is what CheckNow returns to the caller. The tray uses
// Downloaded to show "Restart now (vX)" and AlreadyLatest to log a
// quiet "you're current" line for a manual click.
type Result struct {
	LatestVersion string
	Downloaded    bool
	AlreadyLatest bool
}

// New constructs an Updater. cfg + save let the updater persist
// staged-update state into the same user-config file the tray uses
// for its other prefs, so a quit-and-relaunch picks up the staged
// binary without re-downloading.
func New(cfg *userconfig.Config, save func() error, appName, currentVersion, repoFull, pat string) (*Updater, error) {
	if cfg == nil || save == nil {
		return nil, errors.New("updater: cfg and save are required")
	}
	owner, repo := parseRepo(repoFull)
	if owner == "" {
		owner, repo = parseRepo(moduleRepo())
	}
	base, err := userconfig.Dir(appName)
	if err != nil {
		return nil, fmt.Errorf("user config dir: %w", err)
	}
	cache := filepath.Join(base, "updates")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir cache: %w", err)
	}
	return &Updater{
		appName:        appName,
		currentVersion: normalizeVer(currentVersion),
		owner:          owner,
		repo:           repo,
		pat:            pat,
		cacheDir:       cache,
		cfg:            cfg,
		saveCfg:        save,
	}, nil
}

// Configured reports whether a release source is known. False means
// the updater can't do anything — caller should hide UI affordances.
func (u *Updater) Configured() bool { return u.owner != "" && u.repo != "" }

// HasStaged returns true when a previously downloaded binary is still
// on disk and waiting to be applied. Stale config rows that point at
// a missing file are treated as no-staged (and should be cleared by
// the caller).
func (u *Updater) HasStaged() bool {
	return u.cfg.StagedUpdatePath != "" && fileExists(u.cfg.StagedUpdatePath)
}

// StagedVersion is the tag (e.g. "v1.2.3") of the pending update.
func (u *Updater) StagedVersion() string { return u.cfg.StagedUpdateVersion }

// LatestInfo describes the GitHub release latest tag plus the assets
// that match this binary's GOOS/GOARCH. Returned by CheckLatest so the
// caller (tray) can show the version before kicking off the download.
type LatestInfo struct {
	Version       string // normalised "vX.Y.Z"
	AlreadyLatest bool   // true when current >= latest (Download is a no-op)
	AlreadyStaged bool   // true when this exact version is already staged on disk
	bin           *ghAsset
	sum           *ghAsset
}

// CheckLatest fetches the latest release and compares it to the
// running version. It does NOT download — call Download with the
// returned LatestInfo to actually fetch the asset. Concurrent calls
// are coalesced.
func (u *Updater) CheckLatest(ctx context.Context) (LatestInfo, error) {
	if !u.Configured() {
		return LatestInfo{}, errors.New("updater not configured (no github repo)")
	}
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return LatestInfo{}, errors.New("check already in progress")
	}
	u.checking = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.checking = false
		u.mu.Unlock()
	}()

	rel, err := u.fetchLatest(ctx)
	if err != nil {
		return LatestInfo{}, fmt.Errorf("fetch latest: %w", err)
	}
	latest := normalizeVer(rel.TagName)
	info := LatestInfo{Version: latest}
	if !semverNewer(latest, u.currentVersion) {
		info.AlreadyLatest = true
		return info, nil
	}
	if u.cfg.StagedUpdateVersion == latest && fileExists(u.cfg.StagedUpdatePath) {
		info.AlreadyStaged = true
		return info, nil
	}
	info.bin, info.sum = u.pickAssets(rel.Assets, latest)
	if info.bin == nil {
		return info, fmt.Errorf("no asset matched %s", u.assetName(latest))
	}
	return info, nil
}

// Download fetches the binary asset described by info, verifies its
// SHA256 against the sibling .sha256 file, and stages it under the
// updater's cache dir. Persists the staged path/version into the
// userconfig so a subsequent Apply or auto-apply on next launch can
// pick it up. No-op if info indicates AlreadyLatest or AlreadyStaged.
func (u *Updater) Download(ctx context.Context, info LatestInfo) error {
	if info.AlreadyLatest || info.AlreadyStaged {
		return nil
	}
	if info.bin == nil {
		return errors.New("download: no binary asset in LatestInfo")
	}
	binData, err := u.downloadAsset(ctx, info.bin.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if info.sum != nil {
		sumData, err := u.downloadAsset(ctx, info.sum.URL)
		if err != nil {
			return fmt.Errorf("download sha256: %w", err)
		}
		want := parseSHA256(string(sumData))
		got := sha256Hex(binData)
		if want == "" || !strings.EqualFold(want, got) {
			return fmt.Errorf("sha256 mismatch (got %s, want %s)", got, want)
		}
	}
	binary, err := u.extractStaged(binData)
	if err != nil {
		return fmt.Errorf("extract staged: %w", err)
	}
	stagedPath := filepath.Join(u.cacheDir, fmt.Sprintf("%s-%s%s", u.appName, info.Version, stagedExt()))
	if err := os.WriteFile(stagedPath, binary, 0o755); err != nil {
		return fmt.Errorf("write staged: %w", err)
	}
	u.cfg.StagedUpdatePath = stagedPath
	u.cfg.StagedUpdateVersion = info.Version
	if err := u.saveCfg(); err != nil {
		return fmt.Errorf("save staged path: %w", err)
	}
	return nil
}

// CheckNow runs CheckLatest then Download in one shot — convenience
// for the background auto-update goroutine that doesn't need
// intermediate UI feedback.
func (u *Updater) CheckNow(ctx context.Context) (Result, error) {
	info, err := u.CheckLatest(ctx)
	if err != nil {
		return Result{}, err
	}
	if info.AlreadyLatest {
		return Result{LatestVersion: info.Version, AlreadyLatest: true}, nil
	}
	if info.AlreadyStaged {
		return Result{LatestVersion: info.Version}, nil
	}
	if err := u.Download(ctx, info); err != nil {
		return Result{LatestVersion: info.Version}, err
	}
	return Result{LatestVersion: info.Version, Downloaded: true}, nil
}

// ApplyStagedAndRestart performs the binary swap and re-execs the new
// process. Caller passes stop funcs (server cancel, worker cancel) so
// goroutines drain before the swap. On success this function does not
// return — Unix syscall.Exec replaces our image; Windows spawns a new
// process and we os.Exit. Returns an error only when the swap itself
// fails before re-exec.
//
// Before handing control to the per-OS swap, an update sentinel is
// written to cacheDir recording the expected post-install version.
// The next launch reads it via CheckUpdateOutcome to decide if the
// install succeeded — installer exit codes alone are not trusted
// (msiexec /qn, dpkg postinst, and inner-binary swaps all have ways
// to silently no-op).
func (u *Updater) ApplyStagedAndRestart(stops ...func()) error {
	if !u.HasStaged() {
		return errors.New("no staged update")
	}
	for _, s := range stops {
		s()
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	staged := u.cfg.StagedUpdatePath
	stagedVersion := u.cfg.StagedUpdateVersion

	// Clear staged state BEFORE swap so a partial failure on next
	// launch doesn't loop us through a broken update forever.
	u.cfg.StagedUpdatePath = ""
	u.cfg.StagedUpdateVersion = ""
	if err := u.saveCfg(); err != nil {
		return fmt.Errorf("clear staged: %w", err)
	}

	sentinel := Sentinel{
		FromVersion:   u.currentVersion,
		ToVersion:     stagedVersion,
		StartedAt:     time.Now().UTC(),
		ExpectedPath:  exe,
		OldBinaryPath: exe + ".old",
	}

	if runtime.GOOS == "windows" {
		sentinel.Method = "msi"
		sentinel.InstallerLog = filepath.Join(u.cacheDir, "msiexec-install.log")
		sentinel.HelperScript = filepath.Join(u.cacheDir, "update-helper.bat")
		sentinel.HelperLog = filepath.Join(u.cacheDir, "update-helper.log")
		if err := writeSentinel(u.cacheDir, sentinel); err != nil {
			return fmt.Errorf("write sentinel: %w", err)
		}
		return swapWindows(exe, staged, u.cacheDir, sentinel)
	}
	if runtime.GOOS == "linux" && strings.HasSuffix(staged, ".deb") {
		sentinel.Method = "dpkg"
		sentinel.InstallerLog = filepath.Join(u.cacheDir, "dpkg-install.log")
		sentinel.HelperScript = filepath.Join(u.cacheDir, "update-helper.sh")
		sentinel.HelperLog = filepath.Join(u.cacheDir, "update-helper.log")
		if err := writeSentinel(u.cacheDir, sentinel); err != nil {
			return fmt.Errorf("write sentinel: %w", err)
		}
		return swapLinuxDeb(exe, staged, u.cacheDir, sentinel)
	}
	sentinel.Method = "binary-swap"
	if err := writeSentinel(u.cacheDir, sentinel); err != nil {
		return fmt.Errorf("write sentinel: %w", err)
	}
	return swapUnix(exe, staged)
}

// swapUnix renames staged → current then re-execs in place. On macOS
// the inner Mach-O binary inside the .app bundle is swappable while
// the process runs (Darwin allows this), but a fresh download carries
// com.apple.quarantine — we strip that xattr so Gatekeeper doesn't
// block the relaunch. The original binary is preserved as <exe>.old
// so the next launch can verify and roll back if the new binary fails
// to start (e.g. unsigned, corrupted, ABI mismatch).
//
// The previous flow renamed straight onto current and re-exec'd — that
// works in the happy path but leaves no rollback if the new binary is
// broken, and skipped quarantine clearing entirely.
func swapUnix(current, staged string) error {
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("chmod staged: %w", err)
	}
	if runtime.GOOS == "darwin" {
		// Best-effort: missing xattr is fine, but a hard error here
		// (e.g. permission) means the relaunch will be Gatekeeper-blocked
		// and the user will see no app — bail and surface it.
		if err := clearQuarantine(staged); err != nil {
			return fmt.Errorf("clear quarantine: %w", err)
		}
	}

	backup := current + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(current, backup); err != nil {
		// On Linux a non-root user may not be able to rename a binary
		// in /usr/bin — but we only land here for non-.deb Unix paths
		// (e.g. someone built locally and dropped the binary in $HOME).
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(staged, current); err != nil {
		// Cross-device rename → copy fallback. Keep backup so the
		// caller can recover manually if both copies fail.
		if cerr := copyFile(staged, current); cerr != nil {
			_ = os.Rename(backup, current)
			return fmt.Errorf("install staged binary: rename=%v copy=%w", err, cerr)
		}
		_ = os.Remove(staged)
	}
	if err := os.Chmod(current, 0o755); err != nil {
		return fmt.Errorf("chmod current: %w", err)
	}
	args := append([]string{current}, os.Args[1:]...)
	return syscall.Exec(current, args, os.Environ())
}

// clearQuarantine removes the com.apple.quarantine extended attribute
// from path. macOS adds this xattr to anything downloaded over a
// network; if it survives onto a binary launched via syscall.Exec, the
// re-exec is fine but a subsequent open by Finder/launchd would prompt
// Gatekeeper. Removing it eagerly avoids surprises after the next
// real reboot/login. Missing xattr is not an error.
func clearQuarantine(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cmd := exec.Command("xattr", "-d", "com.apple.quarantine", path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// xattr returns non-zero when the attribute is absent — distinguish
	// that from a real failure so we don't fail updates on Macs where
	// the binary was never quarantined to begin with.
	if strings.Contains(string(out), "No such xattr") || strings.Contains(string(out), "could not be found") {
		return nil
	}
	return fmt.Errorf("xattr -d: %w (%s)", err, strings.TrimSpace(string(out)))
}

// swapWindows runs msiexec via a detached helper .bat that waits for
// this process to exit, applies the MSI, verifies the result, and
// relaunches the binary. The previous design fired msiexec with
// cmd.Start() then immediately os.Exit(0) — that races with the OS
// releasing the locked .exe and produces silent partial-installs:
// shortcut updated to the new path, .exe never written, user sees
// "Windows cannot find ...support-tools.exe".
//
// The helper does five things the previous flow did not:
//
//  1. Wait-for-PID — polls until our PID is gone before touching the
//     locked .exe (avoids "files in use" rollback that msiexec /qn
//     would silently swallow).
//  2. msiexec /wait — runs synchronously and captures the exit code.
//  3. Verify — checks the expected .exe exists and is non-zero after
//     install; failure here is reported via the helper log, not
//     silently lost.
//  4. Relaunch — only fires if step 3 passed, replacing the WXS
//     LaunchApp custom action which has historically been flaky.
//  5. Outcome — leaves a helper log on disk that the next launch can
//     compare against the sentinel to decide success/failure.
//
// `cmd /c start "" /b` detaches the helper from the current console
// so we can exit immediately without orphaning it; `current` is the
// post-install .exe path the helper will verify and relaunch.
func swapWindows(current, staged, cacheDir string, sentinel Sentinel) error {
	logPath := sentinel.InstallerLog
	helperPath := sentinel.HelperScript
	helperLog := sentinel.HelperLog
	if err := writeWindowsHelper(helperPath, helperLog, current, staged, logPath); err != nil {
		return fmt.Errorf("write update helper: %w", err)
	}
	pid := os.Getpid()
	log.Printf("updater: scheduling helper=%s pid=%d staged=%s", helperPath, pid, staged)

	cmd := exec.Command("cmd.exe", "/c", "start", "", "/b", helperPath, fmt.Sprintf("%d", pid))
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start update helper: %w", err)
	}
	// Detach from helper so it survives our exit. Process.Release
	// drops the handle without waiting; a zombie wait would defeat
	// the whole point of the helper.
	_ = cmd.Process.Release()
	os.Exit(0)
	return nil
}

// writeWindowsHelper materializes the .bat the parent process invokes.
// Kept on disk (not piped via stdin) so it can be inspected after a
// failed update — the path is recorded in the sentinel for diagnosis.
//
// Layout of arguments / vars:
//
//	%1            = parent PID (this process, must exit before msiexec)
//	expectedExe   = path the installer is expected to (re)create
//	stagedMsi     = .msi to hand to msiexec
//	msiLog        = msiexec /L*v output path
//
// All paths are quoted so spaces in `Program Files` etc. don't break
// the script. The script never deletes the staged MSI on failure so
// the user can re-run it manually from %LocalAppData%\<app>\updates.
func writeWindowsHelper(helperPath, helperLog, expectedExe, stagedMsi, msiLog string) error {
	script := fmt.Sprintf(`@echo off
setlocal
set "HLOG=%s"
> "%%HLOG%%" echo [%%date%% %%time%%] update helper start pid=%%1

:wait_pid
tasklist /FI "PID eq %%1" 2>NUL | find "%%1" >NUL
if not errorlevel 1 (
  timeout /t 1 /nobreak >NUL
  goto wait_pid
)
>> "%%HLOG%%" echo [%%date%% %%time%%] parent pid %%1 exited

>> "%%HLOG%%" echo [%%date%% %%time%%] running msiexec
msiexec /i "%s" /qn /norestart /L*v "%s" REINSTALLMODE=amus
set MEC=%%errorlevel%%
>> "%%HLOG%%" echo [%%date%% %%time%%] msiexec exit=%%MEC%%

if not "%%MEC%%"=="0" (
  >> "%%HLOG%%" echo [%%date%% %%time%%] FAIL: msiexec non-zero exit, see %s
  exit /b %%MEC%%
)

if not exist "%s" (
  >> "%%HLOG%%" echo [%%date%% %%time%%] FAIL: expected exe missing after install
  exit /b 2
)

for %%%%I in ("%s") do set SZ=%%%%~zI
if "%%SZ%%"=="0" (
  >> "%%HLOG%%" echo [%%date%% %%time%%] FAIL: expected exe is zero bytes
  exit /b 3
)

>> "%%HLOG%%" echo [%%date%% %%time%%] OK: launching %s
start "" "%s"
exit /b 0
`,
		helperLog,
		stagedMsi,
		msiLog,
		msiLog,
		expectedExe,
		expectedExe,
		expectedExe,
		expectedExe,
	)
	return os.WriteFile(helperPath, []byte(script), 0o755)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// CleanupOldBinary removes any leftover <exe>.old from a prior
// Windows update swap. Safe to call from startup; quietly ignores
// "file in use" errors (Windows will purge on reboot).
func CleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.Remove(exe + ".old"); err != nil && !os.IsNotExist(err) {
		log.Printf("updater: remove old binary: %v", err)
	}
}

// ----- GitHub API -----

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (u *Updater) fetchLatest(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPI, u.owner, u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if u.pat != "" {
		req.Header.Set("Authorization", "Bearer "+u.pat)
	}
	resp, err := newClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("github auth failed (%d) — RELEASE_GITHUB_DOWNLOAD_PAT may be expired; rotate it and publish a new release", resp.StatusCode)
		}
		return nil, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func (u *Updater) downloadAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if u.pat != "" {
		req.Header.Set("Authorization", "Bearer "+u.pat)
	}
	resp, err := newClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("download %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

func (u *Updater) pickAssets(assets []ghAsset, version string) (bin, sum *ghAsset) {
	target := u.assetName(version)
	sumName := target + ".sha256"
	for i := range assets {
		a := &assets[i]
		switch a.Name {
		case target:
			bin = a
		case sumName:
			sum = a
		}
	}
	return
}

// ----- helpers -----

func newClient() *http.Client { return &http.Client{Timeout: httpTimeout} }

func parseRepo(full string) (owner, repo string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	full = strings.TrimPrefix(full, "github.com/")
	parts := strings.Split(full, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}

func moduleRepo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Path
}

func normalizeVer(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" || v == "unknown" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func semverNewer(latest, current string) bool {
	if latest == "" {
		return false
	}
	if current == "" {
		// dev / unknown build — treat any tagged release as newer.
		return true
	}
	if !semver.IsValid(latest) || !semver.IsValid(current) {
		return false
	}
	return semver.Compare(latest, current) > 0
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// parseSHA256 reads a `sha256sum`-style line ("<64 hex>  filename")
// and returns the digest. Tolerates extra whitespace / a trailing
// newline; ignores everything after the first field.
func parseSHA256(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
