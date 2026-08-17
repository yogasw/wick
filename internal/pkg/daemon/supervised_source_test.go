package daemon

import "testing"

// withSystemdInit forces the platform check for one test, so both halves
// of the decision are reachable from any host. Without it only the
// false-positive branch could be exercised off a systemd machine, leaving
// the case that must keep working unverified.
func withSystemdInit(t *testing.T, booted bool) {
	t.Helper()
	prev := systemdIsInitFn
	systemdIsInitFn = func() bool { return booted }
	t.Cleanup(func() { systemdIsInitFn = prev })
}

// The genuine case must still work: a real systemd unit sets
// INVOCATION_ID on a host that really is running systemd, and that has to
// keep reporting SourceSystemd. A fix that reported "cli" everywhere
// would silence the false positive by breaking the true one.
func TestDetectSource_RealSystemdIsStillTrusted(t *testing.T) {
	withSystemdInit(t, true)
	t.Setenv("INVOCATION_ID", "8f14e45fceea167a5a36dedd4bea2543")
	t.Setenv("WICK_SPAWN_SOURCE", "")

	if got := DetectSource(); got != SourceSystemd {
		t.Fatalf("DetectSource = %q, want %q on a genuine systemd host", got, SourceSystemd)
	}
}

// The mirror image, driven rather than inferred from the host: systemd is
// absent, so the variable must not be believed.
func TestDetectSource_NoSystemdRejectsTheHint(t *testing.T) {
	withSystemdInit(t, false)
	t.Setenv("INVOCATION_ID", "1") // the literal value Fly's init exports
	t.Setenv("WICK_SPAWN_SOURCE", "")

	if got := DetectSource(); got == SourceSystemd {
		t.Fatal("INVOCATION_ID was trusted on a host with no systemd")
	}
}

// The bug this guards against, seen on a real Fly.io machine:
//
//	$ wick-agent status
//	wick-agent: running (via systemd)
//	$ systemd-run --user --scope ...
//	System has not been booted with systemd as init system (PID 1).
//
// INVOCATION_ID is a plain environment variable, and Fly's init sets it
// on a microVM with no systemd installed. Trusting it alone made status
// claim a supervisor that was not there — and an operator reads that as
// evidence that scope isolation works.
// Checked against the REAL platform probe, not a stub: this is the one
// test that would have caught the incident on the affected host itself,
// and it stays honest wherever it runs.
func TestDetectSource_InvocationIDAloneIsNotProof(t *testing.T) {
	t.Setenv("INVOCATION_ID", "5f3a9c2e8b1d4a7f")
	t.Setenv("WICK_SPAWN_SOURCE", "")

	got := DetectSource()

	if !systemdIsInit() && got == SourceSystemd {
		t.Fatal("INVOCATION_ID alone reported systemd on a host without it — " +
			"this is the Fly.io false positive")
	}
	if systemdIsInit() && got != SourceSystemd {
		t.Fatalf("DetectSource = %q on a genuine systemd host, want %q", got, SourceSystemd)
	}
}

// An explicit stamp from the tray or the daemon parent still wins, since
// that one is set by wick itself rather than inferred.
func TestDetectSource_ExplicitStampIsHonoured(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("WICK_SPAWN_SOURCE", string(SourceTray))

	if got := DetectSource(); got != SourceTray {
		t.Fatalf("DetectSource = %q, want %q", got, SourceTray)
	}
}

// No signals at all means a direct CLI launch, not "unknown" — the
// operator ran the binary themselves.
func TestDetectSource_DefaultsToCLI(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("WICK_SPAWN_SOURCE", "")

	if got := DetectSource(); got != SourceCLI {
		t.Fatalf("DetectSource = %q, want %q", got, SourceCLI)
	}
}

// A garbage value in the explicit stamp must fall through to the default
// rather than being reported verbatim.
func TestDetectSource_RejectsUnknownStamp(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("WICK_SPAWN_SOURCE", "kubernetes")

	if got := DetectSource(); got != SourceCLI {
		t.Fatalf("DetectSource = %q, want %q for an unrecognised stamp", got, SourceCLI)
	}
}
