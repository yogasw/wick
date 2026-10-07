package daemon

import "testing"

// A build with no recorded version is the loop this guard exists to stop: it
// can never equal the running version, so it stays "pending" forever and the
// watcher hands over to it at every idle moment.
func TestBlockReasonVersionless(t *testing.T) {
	reason, want := blockReason(BinaryInfo{AppVersion: ""}, "0.1.343")
	if reason == "" {
		t.Fatal("a versionless candidate must be blocked")
	}
	if want != "0.1.344" {
		t.Fatalf("want = %q, expected the next patch of the running version", want)
	}
}

func TestBlockReasonDowngrade(t *testing.T) {
	reason, want := blockReason(BinaryInfo{AppVersion: "0.1.340"}, "0.1.343")
	if reason == "" {
		t.Fatal("an older candidate must be blocked")
	}
	if want != "0.1.344" {
		t.Fatalf("want = %q", want)
	}
}

func TestBlockReasonForward(t *testing.T) {
	if reason, _ := blockReason(BinaryInfo{AppVersion: "0.1.344"}, "0.1.343"); reason != "" {
		t.Fatalf("a newer build must not be blocked: %s", reason)
	}
	// Same number, different build — the normal local-tree rebuild. PendingSwap
	// only reaches blockReason when the builds already differ.
	if reason, _ := blockReason(BinaryInfo{AppVersion: "0.1.343"}, "0.1.343"); reason != "" {
		t.Fatalf("a rebuild of the same version must not be blocked: %s", reason)
	}
	// An unparseable version on either side is not called a downgrade.
	if reason, _ := blockReason(BinaryInfo{AppVersion: "dev"}, "0.1.343"); reason != "" {
		t.Fatalf("an unparseable version must not be reported as a downgrade: %s", reason)
	}
}
