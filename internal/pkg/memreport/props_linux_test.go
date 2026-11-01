//go:build linux

package memreport

import (
	"os"
	"runtime/debug"
	"testing"
	"time"
)

func TestVersionFromPath(t *testing.T) {
	for path, want := range map[string]string{
		"/home/dev/.local/share/tool/versions/2.1.303": "2.1.303",
		"/opt/node-v20.11.1/bin/node":                  "20.11.1",
		"/opt/app/1.4.0-rc.2/bin/app":                  "1.4.0-rc.2",
		"/usr/bin/python3.12":                          "",
		"/usr/bin/bash":                                "",
		"":                                             "",
	} {
		if got := versionFromPath(path); got != want {
			t.Errorf("%q: got %q, want %q", path, got, want)
		}
	}
}

func TestGoBuildVersion(t *testing.T) {
	mk := func(ldflags, main string) *debug.BuildInfo {
		bi := &debug.BuildInfo{Main: debug.Module{Version: main}}
		if ldflags != "" {
			bi.Settings = []debug.BuildSetting{{Key: "-ldflags", Value: ldflags}}
		}
		return bi
	}
	cases := []struct {
		bi   *debug.BuildInfo
		want string
	}{
		{mk("-X example.com/acme/app.BuildAppName=acme -X example.com/acme/app.BuildAppVersion=0.1.550 -X example.com/acme/app.BuildTime=x", "(devel)"), "0.1.550"},
		{mk("-s -w -X main.version=v2.3.4", ""), "v2.3.4"},
		{mk("-s -w", "v1.9.0"), "v1.9.0"},
		{mk("", "(devel)"), ""},
	}
	for i, c := range cases {
		if got := goBuildVersion(c.bi); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestPackageVersionOfScript(t *testing.T) {
	root := t.TempDir()
	pkg := root + "/node_modules/acme-mcp"
	if err := os.MkdirAll(pkg+"/dist/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	// A workspace root without a name must not answer for the package.
	_ = os.WriteFile(root+"/package.json", []byte(`{"private":true,"version":"9.9.9"}`), 0o644)
	_ = os.WriteFile(pkg+"/package.json", []byte(`{"name":"acme-mcp","version":"1.7.2"}`), 0o644)
	if v := packageVersion(pkg + "/dist/bin/server.js"); v != "1.7.2" {
		t.Fatalf("got %q", v)
	}
	if v := packageVersion("relative/server.js"); v != "" {
		t.Fatalf("relative path gave %q", v)
	}
	if a := scriptArg([]string{"node", "--max-old-space-size=900", "/srv/app.js", "--port", "1"}); a != "/srv/app.js" {
		t.Fatalf("scriptArg = %q", a)
	}
	if a := scriptArg(nil); a != "" {
		t.Fatalf("scriptArg(nil) = %q", a)
	}
}

// The test binary is a live process of this user: every field it may read
// about itself must come back sane.
func TestReadPropsSelf(t *testing.T) {
	p, err := ReadProps(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if p.PID != os.Getpid() || p.PPID == 0 || p.Name == "" || p.Exe == "" || p.Cwd == "" || p.RSSBytes == 0 || p.Threads == 0 {
		t.Fatalf("props = %+v", p)
	}
	if p.ExeDeleted {
		t.Fatalf("own exe reported deleted: %+v", p)
	}
	if d := time.Since(p.StartedAt); d < 0 || d > time.Hour {
		t.Fatalf("start time off: %v (%v ago)", p.StartedAt, d)
	}
	if _, err := ReadProps(1 << 30); err == nil {
		t.Fatal("missing pid must error")
	}
}
