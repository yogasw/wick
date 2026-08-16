//go:build linux || android

package daemon

import (
	"strings"
	"testing"
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
