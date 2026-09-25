package agentmemory

import "testing"

// Everything the operator configured has to reach the daemon.
//
// SetLaunchOptions rebuilds the struct so Port cannot be smuggled in, and that
// rebuild silently dropped AuthToken and Tuning for as long as they have
// existed: ApplySettings decrypted the bearer token at the last moment, handed
// it over, and the daemon was launched without it — while all thirty tuning
// fields in the Settings tab did nothing at all. Nothing failed, nothing was
// logged, and the panel kept saying "Saved".
//
// This test is written against the FIELDS rather than two of them, so the next
// field added to LaunchOptions is caught the same way instead of joining them.
func TestLaunchOptionsCarryEverythingButPort(t *testing.T) {
	be := testBackend(&fakeData{})

	want := LaunchOptions{
		DataDir:   "  /srv/memory  ", // trimmed on the way in
		EnableWeb: true,
		AuthToken: "s3cret-bearer",
		Tuning:    Tuning{LogLevel: "debug", NoCapturePrompts: true},
		Port:      9999, // resolved at start; must NOT survive
	}
	be.Mgr.SetLaunchOptions(want)
	got := be.Mgr.LaunchOpts()

	if got.DataDir != "/srv/memory" {
		t.Errorf("DataDir = %q, want it trimmed", got.DataDir)
	}
	if !got.EnableWeb {
		t.Error("EnableWeb was dropped")
	}
	if got.AuthToken != want.AuthToken {
		t.Errorf("AuthToken = %q, want %q — the daemon would start unauthenticated", got.AuthToken, want.AuthToken)
	}
	if got.Tuning != want.Tuning {
		t.Errorf("Tuning = %+v, want %+v — every knob in the Settings tab would be a no-op", got.Tuning, want.Tuning)
	}
	if got.Port != 0 {
		t.Errorf("Port = %d, want 0: it is resolved at start, not carried", got.Port)
	}
}
