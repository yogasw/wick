package provider

import (
	"strings"
	"testing"
)

// ExitOOM must be a distinct reason, not folded into ExitError: the pool
// and the UI both branch on it, and "exited abnormally" is exactly the
// unhelpful message this reason exists to replace.
func TestExitOOM_IsDistinct(t *testing.T) {
	if ExitOOM == ExitError || ExitOOM == ExitClean {
		t.Fatal("ExitOOM collides with an existing reason")
	}
	if got := ExitReasonName(ExitOOM); got != "oom" {
		t.Fatalf("ExitReasonName(ExitOOM) = %q, want %q", got, "oom")
	}
	if got := exitReasonDetail(ExitOOM); got == "" || got == "unknown" {
		t.Fatalf("exitReasonDetail(ExitOOM) = %q, want a real sentence", got)
	}
}

// Adding a reason must not renumber the existing ones — they are
// persisted in spawn logs, so a shifted iota silently rewrites history.
func TestExitReasons_StableOrder(t *testing.T) {
	for i, want := range []struct {
		r    ExitReason
		name string
	}{
		{ExitClean, "clean"},
		{ExitIdle, "idle_ttl"},
		{ExitStopped, "stopped"},
		{ExitError, "error"},
		{ExitRespawn, "respawn"},
	} {
		if int(want.r) != i {
			t.Fatalf("%s moved to %d, want %d — old spawn logs now misread", want.name, want.r, i)
		}
	}
}

// The message has to name numbers. "Agent stopped" leaves an operator
// with nothing to change; a peak and a ceiling point straight at the knob.
func TestOOMDetail_NamesNumbers(t *testing.T) {
	got := OOMDetail(1610612736, 1024) // 1.5 GiB peak against a 1024 MB limit
	if !strings.Contains(got, "1.5 GB") {
		t.Fatalf("detail %q does not report the peak in human units", got)
	}
	if !strings.Contains(got, "1024 MB") {
		t.Fatalf("detail %q does not report the limit", got)
	}
}

// With no readable peak the sentence must still be true and useful,
// never a fabricated zero.
func TestOOMDetail_UnknownPeak(t *testing.T) {
	got := OOMDetail(0, 1024)
	if strings.Contains(got, "0 B") || strings.Contains(got, "0.0") {
		t.Fatalf("detail %q reports a fake zero peak", got)
	}
	if !strings.Contains(got, "1024 MB") {
		t.Fatalf("detail %q dropped the limit", got)
	}
}

// An agent with NO per-agent ceiling can still be OOM-killed — by the
// aggregate slice limit, or by the machine running out. Reporting "its
// 0 MB limit" would describe a misconfiguration that does not exist and
// send the operator to the wrong setting.
func TestOOMDetail_NoPerAgentLimit(t *testing.T) {
	for _, peak := range []uint64{0, 1610612736} {
		got := OOMDetail(peak, 0)
		if strings.Contains(got, "0 MB") {
			t.Fatalf("detail %q claims a 0 MB limit", got)
		}
		low := strings.ToLower(got)
		if !strings.Contains(low, "combined limit") && !strings.Contains(low, "running out") {
			t.Fatalf("detail %q does not point at the real cause", got)
		}
	}

	// The measured peak is still worth reporting when it is known.
	withPeak := OOMDetail(1610612736, 0)
	if !strings.Contains(withPeak, "1.5 GB") {
		t.Fatalf("detail %q dropped a known peak", withPeak)
	}
}

// A negative limit is not a real configuration, but it must degrade the
// same way rather than printing "its -1 MB limit".
func TestOOMDetail_NegativeLimitTreatedAsNone(t *testing.T) {
	got := OOMDetail(1024, -1)
	if strings.Contains(got, "-1") {
		t.Fatalf("detail %q leaked a negative limit", got)
	}
}

// humanBytes is what the operator actually reads; check the boundaries
// rather than one happy value.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536 * 1024, "1.5 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
