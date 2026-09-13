package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

func TestParseLdflagsX(t *testing.T) {
	// The real shape recorded by `wick build`, quotes included.
	raw := `"-X github.com/yogasw/wick/internal/appname.BuildAppName=support-tools -X github.com/yogasw/wick/app.BuildAppVersion=0.1.113 -X github.com/yogasw/wick/app.BuildTime=2026-09-11T00:42:34Z"`
	got := parseLdflagsX(raw)
	for k, want := range map[string]string{
		"BuildAppName":    "support-tools",
		"BuildAppVersion": "0.1.113",
		"BuildTime":       "2026-09-11T00:42:34Z",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	// -X=k=v is equally valid and shows up from some build wrappers.
	if got := parseLdflagsX(`-X=main.Version=1.2.3`); got["Version"] != "1.2.3" {
		t.Errorf("-X= form: got %q, want 1.2.3", got["Version"])
	}
	// A value containing '=' must survive; only the first separator counts.
	if got := parseLdflagsX(`-X main.Flags=a=b`); got["Flags"] != "a=b" {
		t.Errorf("value with '=': got %q, want a=b", got["Flags"])
	}
}

// TestInspectBinarySelf reads the test binary itself: a real Go binary,
// built from this module, so every field we care about is populated
// without having to compile a fixture.
func TestInspectBinarySelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve test binary: %v", err)
	}
	info, err := InspectBinary(exe)
	if err != nil {
		t.Fatalf("InspectBinary(self): %v", err)
	}
	if !info.IsWick() {
		t.Errorf("test binary should be recognised as a wick binary, got %+v", info)
	}
	if info.GOARCH != runtime.GOARCH || info.GOOS != runtime.GOOS {
		t.Errorf("platform = %s/%s, want %s/%s", info.GOOS, info.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if info.Size == 0 {
		t.Error("size not populated")
	}
}

func TestInspectBinaryRejectsNonBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectBinary(path); err == nil {
		t.Fatal("expected an error for a shell script, got nil")
	}
}

func TestCompareBinaries(t *testing.T) {
	running := BinaryInfo{
		Path: "/usr/bin/support-tools", MainModule: "qiscus-support-tools",
		AppName: "support-tools", AppVersion: "0.1.112", BuildTime: "2026-09-10T14:00:00Z",
		WickVersion: "v1.8.1", GOOS: "linux", GOARCH: "amd64",
	}
	ok := BinaryInfo{
		Path: "bin/new", MainModule: "qiscus-support-tools",
		AppName: "support-tools", AppVersion: "0.1.113", BuildTime: "2026-09-11T00:42:34Z",
		WickVersion: "v1.9.0", GOOS: "linux", GOARCH: "amd64",
	}

	mutate := func(f func(*BinaryInfo)) BinaryInfo {
		c := ok
		f(&c)
		return c
	}

	cases := []struct {
		name      string
		candidate BinaryInfo
		want      Severity
		label     string
	}{
		{"clean upgrade", ok, SevInfo, ""},
		{"wrong arch", mutate(func(b *BinaryInfo) { b.GOARCH = "arm64" }), SevFatal, "arch"},
		{"wrong os", mutate(func(b *BinaryInfo) { b.GOOS = "darwin" }), SevFatal, "os"},
		{"not wick", mutate(func(b *BinaryInfo) { b.WickVersion = "" }), SevFatal, "identity"},
		{"other module", mutate(func(b *BinaryInfo) { b.MainModule = "something-else" }), SevBlock, "module"},
		{"other app", mutate(func(b *BinaryInfo) { b.AppName = "other-app" }), SevBlock, "app"},
		{"downgrade", mutate(func(b *BinaryInfo) { b.AppVersion = "0.1.111" }), SevBlock, "version"},
		{"same version", mutate(func(b *BinaryInfo) { b.AppVersion = "0.1.112" }), SevFatal, "version"},
		{"local wick tree", mutate(func(b *BinaryInfo) { b.WickLocal = true }), SevWarn, "wick"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := CompareBinaries(tc.candidate, running, "linux", "amd64")
			if got := Worst(findings); got != tc.want {
				t.Fatalf("worst severity = %v, want %v (findings: %+v)", got, tc.want, findings)
			}
			if tc.label == "" {
				return
			}
			for _, f := range findings {
				if f.Label == tc.label && f.Severity == tc.want {
					return
				}
			}
			t.Fatalf("no %v finding labelled %q in %+v", tc.want, tc.label, findings)
		})
	}
}

// A candidate is judged against an unreadable running binary without
// inventing identity mismatches out of the zero value.
func TestCompareBinariesUnknownRunning(t *testing.T) {
	candidate := BinaryInfo{
		MainModule: "qiscus-support-tools", AppName: "support-tools",
		AppVersion: "0.1.113", WickVersion: "v1.9.0", GOOS: "linux", GOARCH: "amd64",
	}
	if got := Worst(CompareBinaries(candidate, BinaryInfo{}, "linux", "amd64")); got != SevInfo {
		t.Fatalf("worst = %v, want info when the running binary is unknown", got)
	}
}

func TestCmpVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.113", "0.1.112", 1},
		{"0.1.112", "0.1.113", -1},
		{"0.1.112", "0.1.112", 0},
		{"0.2.0", "0.1.999", 1},
		{"1.0", "1.0.0", 0},
		{"v1.9.0", "v1.8.1", 1},
		{"dev", "0.1.112", 0}, // unknown never claims a downgrade
		{"0.1.112", "dev", 0}, // ...in either direction
		{"", "0.1.112", 0},    //
		{"1.2.3-rc1", "1.2.3", 0},
	}
	for _, tc := range cases {
		if got := cmpVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("cmpVersions(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSameBuild(t *testing.T) {
	a := BinaryInfo{AppVersion: "0.1.113", BuildTime: "2026-09-11T00:42:34Z"}
	if !SameBuild(a, a) {
		t.Error("identical build info should compare equal")
	}
	b := a
	b.BuildTime = "2026-09-11T01:00:00Z"
	if SameBuild(a, b) {
		t.Error("a rebuild at a different time is not the same build")
	}
	// Without the ldflags there is nothing to compare, so never claim
	// two binaries are the same build.
	if SameBuild(BinaryInfo{}, BinaryInfo{}) {
		t.Error("empty build info must not compare as the same build")
	}
}

func TestInstallAndRestoreBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	src := filepath.Join(dir, "new-app")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}

	backup, err := InstallBinary(src, target, false)
	if err != nil {
		t.Fatalf("InstallBinary: %v", err)
	}
	if got := readFile(t, target); got != "NEW" {
		t.Errorf("target content = %q, want NEW", got)
	}
	if got := readFile(t, backup); got != "OLD" {
		t.Errorf("backup content = %q, want OLD", got)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", st.Mode())
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Error("temp file was left behind")
	}

	if err := RestoreBinary(backup, target, false); err != nil {
		t.Fatalf("RestoreBinary: %v", err)
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("after restore, target = %q, want OLD", got)
	}
	if err := RestoreBinary("", target, false); err == nil {
		t.Error("restoring without a backup should fail loudly")
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256 = %s, want %s", got, want)
	}
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	if !Writable(filepath.Join(dir, "app")) {
		t.Error("a fresh temp dir should be writable")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not apply")
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if Writable(filepath.Join(locked, "app")) {
		t.Error("a dir without write permission must not report writable")
	}
}

// procCmdline0 is what decides where a new binary gets installed, so it is
// checked against a real process rather than trusted.
func TestProcCmdline0(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs is Linux-only")
	}
	sleep, err := safeexec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	cmd := safeexec.Command(sleep, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	deadline := time.Now().Add(2 * time.Second)
	var argv0 string
	for time.Now().Before(deadline) {
		if argv0 = procCmdline0(cmd.Process.Pid); argv0 != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if argv0 == "" {
		t.Fatal("argv[0] not readable for a live child process")
	}
	if !strings.HasSuffix(argv0, "sleep") {
		t.Errorf("argv[0] = %q, want it to end in sleep", argv0)
	}
	if cwd := procCwd(cmd.Process.Pid); cwd == "" {
		t.Error("cwd not readable for a live child process")
	}
}

func TestProcessImageIs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs is Linux-only")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot resolve test binary")
	}
	if !ProcessImageIs(os.Getpid(), exe) {
		t.Error("this process should be reported as running its own binary")
	}
	if ProcessImageIs(os.Getpid(), filepath.Join(t.TempDir(), "nope")) {
		t.Error("a path that does not exist must not match")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Same version is refused OUTRIGHT, not "blocked unless --force": the
// caller only consults --force for SevBlock, so the severity is the
// whole guarantee. A build that swaps itself in under the number that
// is already serving leaves nobody — not the update card, not the
// operator reading `version`, not a rollback — able to say what is
// actually running.
func TestSameVersionIsFatalNotForceable(t *testing.T) {
	running := BinaryInfo{MainModule: "x/app", AppName: "app", AppVersion: "0.1.194", WickVersion: "v1.9.0"}
	candidate := running
	candidate.BuildTime = "2026-09-13T09:28:34Z" // a different build, same number

	findings := CompareBinaries(candidate, running, runtime.GOOS, runtime.GOARCH)
	if got := Worst(findings); got != SevFatal {
		t.Fatalf("worst severity = %v, want SevFatal (--force must not reach it)", got)
	}
	var detail string
	for _, f := range findings {
		if f.Label == "version" {
			detail = f.Detail
		}
	}
	if !strings.Contains(detail, "0.1.195") {
		t.Errorf("finding = %q, want it to name the next acceptable version 0.1.195", detail)
	}
}
