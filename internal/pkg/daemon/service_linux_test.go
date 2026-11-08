//go:build linux || android

package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Without a start limit, an out-of-memory kill crash-loops invisibly:
// restart, resume, respawn, balloon, repeat. With one, the unit lands in
// `failed` and the operator sees an incident instead of a mystery.
func TestRenderUnit_HasRestartRateLimit(t *testing.T) {
	got := renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log")

	for _, want := range []string{"StartLimitIntervalSec=", "StartLimitBurst="} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing %q:\n%s", want, got)
		}
	}
}

// The sibling-slice design needs nothing from wick's own unit. Adding
// Delegate= would signal the rejected sub-cgroup placement, in which
// agent memory counts toward wick's own cgroup and an aggregate limit
// would put wick inside the blast radius.
func TestRenderUnit_NoDelegate(t *testing.T) {
	if strings.Contains(renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log"), "Delegate=") {
		t.Fatal("unit sets Delegate=; the sibling slice does not need it")
	}
}

// The existing behaviour must survive the edit — this unit is what starts
// wick on every boot.
func TestRenderUnit_KeepsExistingDirectives(t *testing.T) {
	got := renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/wick all",
		"Restart=on-failure",
		"RestartSec=5",
		"WantedBy=default.target",
		"StandardOutput=append:/var/log/wick.log",
		"StandardError=append:/var/log/wick.log",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit lost %q:\n%s", want, got)
		}
	}
}

// The app name is not always "wick" (the CLI is reusable), so the unit
// must carry whatever name it was given.
func TestRenderUnit_UsesAppName(t *testing.T) {
	got := renderUnit("otherapp", "/opt/otherapp/bin", "/var/log/otherapp.log")
	if !strings.Contains(got, "Description=otherapp daemon") {
		t.Fatalf("unit did not use the app name:\n%s", got)
	}
}

// The Termux boot script restarts a daemon whose health keeps failing and
// leaves a crash report behind.
func TestTermuxBootScriptRestartsAndReports(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	app := filepath.Join(dir, "app")
	fake := "#!/bin/sh\ncase \"$1\" in\n  start) echo x >>" + starts + " ;;\n  status) echo 'http:    down' ;;\nesac\n"
	if err := os.WriteFile(app, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(dir, "logs")
	_ = os.MkdirAll(logs, 0o755)
	_ = os.WriteFile(filepath.Join(logs, "daemon-2026-01-01.log"), []byte("last line before it died\n"), 0o644)
	script := filepath.Join(dir, "boot.sh")
	body := termuxBootScript("app", Paths{Dir: dir, LogFile: filepath.Join(logs, "daemon-2026-01-01.log"), ExePath: app})
	body = strings.Replace(body, "#!/data/data/com.termux/files/usr/bin/sh", "#!/bin/sh", 1)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script)
	cmd.Env = append(os.Environ(), "WICK_WATCHDOG_INTERVAL=0.2")
	_ = cmd.Run()

	b, _ := os.ReadFile(starts)
	if n := strings.Count(string(b), "x"); n < 2 {
		t.Fatalf("started %d times, want the boot start plus a restart", n)
	}
	crashes, _ := filepath.Glob(filepath.Join(logs, "crash-*.log"))
	if len(crashes) == 0 {
		t.Fatal("no crash report written")
	}
	rep, _ := os.ReadFile(crashes[0])
	if !strings.Contains(string(rep), "last line before it died") {
		t.Fatalf("crash report misses the log tail:\n%s", rep)
	}
}
