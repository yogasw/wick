package daemon

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

// wickModulePath identifies a binary as built against wick. A candidate
// without it in its module graph is some other program entirely, which is
// the one mistake a binary-swap command must never wave through.
const wickModulePath = "github.com/yogasw/wick"

// BinaryInfo is everything we can learn about a binary WITHOUT running it.
//
// Running an unknown binary to ask its version is self-defeating: the check
// exists precisely because the file may not be what the operator thinks it
// is. Go records enough in the binary itself (module graph + the -X ldflags
// `wick build` bakes in) to answer every question we care about statically.
type BinaryInfo struct {
	Path        string
	MainModule  string // main module path, e.g. "qiscus-support-tools"
	AppName     string // -X ...BuildAppName (empty for a plain `go build`)
	AppVersion  string // -X ...BuildAppVersion ("dev" when not injected)
	BuildTime   string // -X ...BuildTime
	WickVersion string // version of the github.com/yogasw/wick dependency
	WickLocal   bool   // wick dep is `replace`d by a local tree
	GOOS        string
	GOARCH      string
	Size        int64
	ModTime     time.Time
}

// InspectBinary reads a binary's embedded build info. The file is never
// executed.
func InspectBinary(path string) (BinaryInfo, error) {
	info := BinaryInfo{Path: path}
	st, err := os.Stat(path)
	if err != nil {
		return info, err
	}
	if st.IsDir() {
		return info, fmt.Errorf("%s is a directory", path)
	}
	info.Size = st.Size()
	info.ModTime = st.ModTime()

	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return info, fmt.Errorf("read build info from %s: %w", path, err)
	}
	info.MainModule = bi.Main.Path
	if info.MainModule == "" {
		info.MainModule = bi.Path
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "GOOS":
			info.GOOS = s.Value
		case "GOARCH":
			info.GOARCH = s.Value
		case "-ldflags":
			for k, v := range parseLdflagsX(s.Value) {
				switch k {
				case "BuildAppName":
					info.AppName = v
				case "BuildAppVersion":
					info.AppVersion = v
				case "BuildTime":
					info.BuildTime = v
				}
			}
		}
	}

	if bi.Main.Path == wickModulePath {
		info.WickVersion = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path != wickModulePath {
			continue
		}
		info.WickVersion = d.Version
		if d.Replace != nil {
			info.WickLocal = true
			if d.Replace.Version != "" && d.Replace.Version != "(devel)" {
				info.WickVersion = d.Replace.Version
			}
		}
	}
	return info, nil
}

// IsWick reports whether the binary was built against wick at all.
func (b BinaryInfo) IsWick() bool { return b.WickVersion != "" }

// Describe renders the one-line identity used in the confirmation summary.
func (b BinaryInfo) Describe() string {
	ver := b.AppVersion
	if ver == "" {
		ver = "unknown"
	}
	wick := b.WickVersion
	if wick == "" {
		wick = "none"
	}
	if b.WickLocal {
		wick += " (local tree)"
	}
	return fmt.Sprintf("%s %s (wick %s)", firstNonEmpty(b.AppName, b.MainModule, "unknown"), ver, wick)
}

// parseLdflagsX pulls `-X <import path>.<Var>=<value>` pairs out of the
// recorded -ldflags setting, keyed by the bare variable name. The import
// path is deliberately ignored: the same variable has lived in more than
// one package across versions (appname.BuildAppVersion vs app.BuildAppVersion),
// and the name is what identifies it.
func parseLdflagsX(ldflags string) map[string]string {
	out := map[string]string{}
	fields := strings.Fields(strings.Trim(ldflags, `"`))
	for i, f := range fields {
		var assign string
		switch {
		case f == "-X" && i+1 < len(fields):
			assign = fields[i+1]
		case strings.HasPrefix(f, "-X="):
			assign = strings.TrimPrefix(f, "-X=")
		default:
			continue
		}
		name, value, ok := strings.Cut(assign, "=")
		if !ok {
			continue
		}
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			name = name[dot+1:]
		}
		out[name] = value
	}
	return out
}

// Severity ranks a preflight finding.
type Severity int

const (
	SevInfo  Severity = iota // shown, never blocks
	SevWarn                  // shown prominently, never blocks
	SevBlock                 // blocks unless --force
	SevFatal                 // blocks, period
)

func (s Severity) String() string {
	switch s {
	case SevFatal:
		return "FATAL"
	case SevBlock:
		return "BLOCK"
	case SevWarn:
		return "WARN"
	default:
		return "info"
	}
}

// Finding is one preflight result about a candidate binary.
type Finding struct {
	Severity Severity
	Label    string
	Detail   string
}

// SameBuild reports whether two binaries are the same build — same app
// version and same build timestamp. Swapping one for the other is a no-op.
func SameBuild(a, b BinaryInfo) bool {
	if a.AppVersion == "" || a.BuildTime == "" {
		return false
	}
	return a.AppVersion == b.AppVersion && a.BuildTime == b.BuildTime
}

// CompareBinaries runs the preflight gate: is the candidate a wick binary,
// for this host, for THIS app, and is it moving forward?
//
// running may be a zero BinaryInfo when the current binary could not be
// inspected; identity checks that need it are then skipped rather than
// guessed at.
func CompareBinaries(candidate, running BinaryInfo, hostGOOS, hostGOARCH string) []Finding {
	var checks []Finding
	add := func(sev Severity, label, format string, args ...any) {
		checks = append(checks, Finding{Severity: sev, Label: label, Detail: fmt.Sprintf(format, args...)})
	}

	if !candidate.IsWick() {
		add(SevFatal, "identity", "%s does not depend on %s — this is not a wick binary", filepath.Base(candidate.Path), wickModulePath)
	}
	if candidate.GOOS != "" && hostGOOS != "" && candidate.GOOS != hostGOOS {
		add(SevFatal, "os", "built for %s, host is %s", candidate.GOOS, hostGOOS)
	}
	if candidate.GOARCH != "" && hostGOARCH != "" && candidate.GOARCH != hostGOARCH {
		add(SevFatal, "arch", "built for %s, host is %s — systemd would fail with 203/EXEC", candidate.GOARCH, hostGOARCH)
	}

	if running.MainModule != "" && candidate.MainModule != "" && running.MainModule != candidate.MainModule {
		add(SevBlock, "module", "%s, running binary is %s — different application", candidate.MainModule, running.MainModule)
	}
	if running.AppName != "" && candidate.AppName != "" && running.AppName != candidate.AppName {
		add(SevBlock, "app", "%s, running binary is %s — different data dir, unit and paths", candidate.AppName, running.AppName)
	}
	if candidate.AppName == "" && running.AppName != "" {
		add(SevWarn, "app", "candidate carries no BuildAppName — not built by `wick build`")
	}

	switch cmpVersions(candidate.AppVersion, running.AppVersion) {
	case -1:
		add(SevBlock, "version", "%s is OLDER than the running %s — downgrade", candidate.AppVersion, running.AppVersion)
	case 0:
		if candidate.AppVersion != "" && candidate.AppVersion == running.AppVersion {
			add(SevWarn, "version", "same version %s as the running binary", candidate.AppVersion)
		}
	}

	if candidate.WickLocal {
		add(SevWarn, "wick", "built from a LOCAL wick tree (go.mod replace), not a released tag")
	}
	return checks
}

// Worst returns the highest severity among findings.
func Worst(checks []Finding) Severity {
	worst := SevInfo
	for _, c := range checks {
		if c.Severity > worst {
			worst = c.Severity
		}
	}
	return worst
}

// cmpVersions compares dotted numeric versions ("0.1.113"). It returns 0
// whenever either side is not comparable (empty, "dev", a git hash): an
// unknown version is never reported as a downgrade, because a false
// "you are going backwards" is worse than no opinion.
func cmpVersions(a, b string) int {
	an, aok := numericVersion(a)
	bn, bok := numericVersion(b)
	if !aok || !bok {
		return 0
	}
	for i := 0; i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func numericVersion(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// FileSHA256 hashes a file, for --sha256 verification of a binary that
// arrived from somewhere else.
func FileSHA256(path string) (string, error) {
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

// InstallBinary replaces target with src atomically and returns the path of
// the backup holding the previous binary.
//
// Two details matter and both are learned the hard way:
//
//   - writing straight over a running binary fails with ETXTBSY, so the new
//     bytes go to a sibling temp file first;
//   - rename(2) within the same directory is atomic, and the running process
//     keeps its old inode alive until it exits on its own — which is exactly
//     what a graceful handoff needs.
//
// When sudo is true the three file operations are delegated to sudo(8),
// for the common layout where the binary lives in a root-owned directory
// but the daemon runs as a user unit.
func InstallBinary(src, target string, sudo bool) (string, error) {
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	tmp := target + ".new"
	backup := target + ".prev"

	if sudo {
		if err := runSudo("install", "-m", "0755", src, tmp); err != nil {
			return "", err
		}
		// Best effort: a first install has no previous binary to keep.
		_ = runSudo("cp", "-f", target, backup)
		if err := runSudo("mv", "-f", tmp, target); err != nil {
			return "", err
		}
		return backup, nil
	}

	if err := copyFileSync(src, tmp, 0o755); err != nil {
		return "", err
	}
	_ = os.Remove(backup)
	if err := os.Link(target, backup); err != nil {
		// Different filesystem or no hard links — fall back to a copy.
		if cerr := copyFileSync(target, backup, 0o755); cerr != nil {
			backup = ""
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return backup, fmt.Errorf("rename %s -> %s: %w", tmp, target, err)
	}
	return backup, nil
}

// RestoreBinary puts a backup taken by InstallBinary back in place, used
// when the successor never came up.
func RestoreBinary(backup, target string, sudo bool) error {
	if backup == "" {
		return errors.New("no backup was taken")
	}
	if _, err := os.Stat(backup); err != nil {
		return err
	}
	if sudo {
		return runSudo("cp", "-f", backup, target)
	}
	return copyFileSync(backup, target, 0o755)
}

func copyFileSync(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func runSudo(args ...string) error {
	cmd := safeexec.Command("sudo", append([]string{"-n"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Writable reports whether the current process can replace the file at
// path — it needs to create a sibling and rename over it, so the check is
// on the directory, not the file.
func Writable(path string) bool {
	dir := filepath.Dir(path)
	probe, err := os.CreateTemp(dir, ".wick-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
