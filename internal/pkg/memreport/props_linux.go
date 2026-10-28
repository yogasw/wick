//go:build linux

package memreport

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// props_linux.go reads the properties of ONE process on request, for the
// process explorer's Properties dialog. Nothing here runs per refresh and
// nothing is cached: the dialog asks for one pid and gets a fresh read.
//
// The question that asked for it: during a zero-downtime handover two wick
// servers run side by side under the same name and command line, and only
// the build each one was started from tells them apart. The binary on disk
// is already the new one by then, so the version comes from the running
// image (/proc/<pid>/exe), never from the path and never by executing it.

// Props is what the dialog shows for one process.
type Props struct {
	PID   int    `json:"pid"`
	PPID  int    `json:"ppid"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Exe   string `json:"exe,omitempty"`
	// ExeDeleted: the file the process was started from has since been
	// replaced or removed — the old process of an upgrade.
	ExeDeleted bool      `json:"exe_deleted,omitempty"`
	Cmdline    string    `json:"cmdline,omitempty"`
	Cwd        string    `json:"cwd,omitempty"`
	OSUser     string    `json:"os_user,omitempty"`
	Threads    int       `json:"threads,omitempty"`
	RSSBytes   uint64    `json:"rss_bytes"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	// Version and where it was read from: "go build info", "package.json"
	// or "path". Both empty when it cannot be known — never guessed.
	Version       string `json:"version,omitempty"`
	VersionSource string `json:"version_source,omitempty"`
}

var (
	// ldflagsVersion matches "-X <pkg>.BuildAppVersion=1.2.3" (how wick
	// stamps its builds) and the common "-X main.version=v1.2.3".
	ldflagsVersion = regexp.MustCompile(`-X[= ]\S*\.(?:BuildAppVersion|[Vv]ersion)=([^\s'"]+)`)
	// pathVersion matches a path segment that is (or ends in) a full
	// version — "/versions/2.1.3", "node-v20.11.1". A bare major.minor glued
	// to a name ("python3.12") is a family, not a build, and is skipped.
	pathVersion = regexp.MustCompile(`(?:^|[/@_-])v?(\d+\.\d+\.\d+(?:[-+.][0-9A-Za-z.]+)?)(?:/|$)`)
)

// clockTicks is USER_HZ, 100 on every Linux wick runs on; reading it needs
// cgo (sysconf), which is not worth it for a start time shown to the second.
const clockTicks = 100

// ReadProps reads one process. An error only when the process is gone;
// fields another user's process refuses are left empty.
func ReadProps(pid int) (Props, error) {
	dir := fmt.Sprintf("/proc/%d", pid)
	status, err := os.ReadFile(dir + "/status")
	if err != nil {
		return Props{}, fmt.Errorf("process %d not found", pid)
	}
	p := Props{PID: pid}
	uid := ""
	for _, line := range strings.Split(string(status), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Name":
			p.Name = v
		case "State":
			p.State = v
		case "PPid":
			p.PPID, _ = strconv.Atoi(v)
		case "Threads":
			p.Threads, _ = strconv.Atoi(v)
		case "VmRSS":
			kb, _ := strconv.ParseUint(strings.Fields(v + " 0")[0], 10, 64)
			p.RSSBytes = kb * 1024
		case "Uid":
			uid = strings.Fields(v + " ")[0]
		}
	}
	if uid != "" {
		if u, err := user.LookupId(uid); err == nil {
			p.OSUser = u.Username
		} else {
			p.OSUser = uid
		}
	}
	if raw, err := os.ReadFile(dir + "/cmdline"); err == nil {
		p.Cmdline = parseCmdline(string(raw))
	}
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		p.Exe = strings.TrimSuffix(exe, " (deleted)")
		p.ExeDeleted = p.Exe != exe
	}
	p.Cwd, _ = os.Readlink(dir + "/cwd")
	p.StartedAt = startTime(dir)
	p.Version, p.VersionSource = readVersion(dir, p.Exe)
	return p, nil
}

// startTime is boot time plus the process's start tick (field 22 of stat).
func startTime(dir string) time.Time {
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return time.Time{}
	}
	// comm (field 2) may contain spaces and parens: fields count from
	// after its closing paren, where field 3 (state) is index 0.
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return time.Time{}
	}
	f := strings.Fields(string(stat[i+1:]))
	if len(f) < 20 {
		return time.Time{}
	}
	ticks, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return time.Time{}
	}
	boot := bootTime()
	if boot.IsZero() {
		return time.Time{}
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks)
}

func bootTime() time.Time {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

// readVersion: the script's package.json for node/bun (the runtime's own
// version says nothing about the program), then the Go build info of the
// running image, then a version-shaped segment of the executable path.
func readVersion(dir, exe string) (string, string) {
	if isScriptRuntime(exe) {
		if raw, err := os.ReadFile(dir + "/cmdline"); err == nil {
			if v := packageVersion(scriptArg(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"))); v != "" {
				return v, "package.json"
			}
		}
	}
	if bi, err := buildinfo.ReadFile(dir + "/exe"); err == nil {
		if v := goBuildVersion(bi); v != "" {
			return v, "go build info"
		}
	}
	if v := versionFromPath(exe); v != "" {
		return v, "path"
	}
	return "", ""
}

func goBuildVersion(bi *buildinfo.BuildInfo) string {
	for _, s := range bi.Settings {
		if s.Key == "-ldflags" {
			if m := ldflagsVersion.FindStringSubmatch(s.Value); m != nil {
				return m[1]
			}
		}
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return ""
}

func versionFromPath(path string) string {
	m := pathVersion.FindAllStringSubmatch(path, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// isScriptRuntime reports whether the executable runs a script named on its
// command line, so the script, not the runtime, is the program.
func isScriptRuntime(path string) bool {
	switch filepath.Base(path) {
	case "node", "nodejs", "bun":
		return true
	}
	return false
}

// scriptArg is the first argument after the runtime that is not a flag.
func scriptArg(argv []string) string {
	for _, a := range argv[min(1, len(argv)):] {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// packageVersion walks up from a script to the nearest package.json that
// has a name and a version — the package's own manifest, not a workspace
// root — and returns that version.
func packageVersion(script string) string {
	if !filepath.IsAbs(script) {
		return ""
	}
	dir := filepath.Dir(script)
	for i := 0; i < 8 && dir != "/" && dir != "."; i++ {
		if f, err := os.Open(filepath.Join(dir, "package.json")); err == nil {
			var pkg struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			err = json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(&pkg)
			f.Close()
			if err == nil && pkg.Name != "" && pkg.Version != "" {
				return pkg.Version
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
