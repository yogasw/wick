package gate

import "testing"

// An unreadable command must not be able to inherit somebody else's
// approval. Hashing "" gave every such call one shared key, so a single
// "allow this session" click stood in for every later call of that tool
// — that is how a PowerShell `rm -rf` ran without ever being shown.
func TestMatchKey_EmptyCommandHasNoKey(t *testing.T) {
	if got := MatchKey("PowerShell", ""); got != "" {
		t.Errorf("MatchKey with empty cmd: got %q, want empty", got)
	}
	if got := MatchKey("Bash", "   "); got != "" {
		t.Errorf("MatchKey with blank cmd: got %q, want empty", got)
	}
}

func TestMatchKey_DistinctPerCommand(t *testing.T) {
	rm := MatchKey("PowerShell", "rm -rf 1111")
	ls := MatchKey("PowerShell", "ls")
	if rm == "" || ls == "" {
		t.Fatal("real commands must produce keys")
	}
	if rm == ls {
		t.Error("different commands collapsed onto one key")
	}
	// Same command under a different interpreter is a different decision.
	if MatchKey("Bash", "rm -rf 1111") == rm {
		t.Error("tool name is not part of the key")
	}
}

func TestIsAutoApproved_EmptyKeyNeverMatches(t *testing.T) {
	// Even with a stale empty entry persisted by an older build, an
	// unreadable command must still be asked about.
	spec := Spec{AutoApproved: []string{"", MatchKey("Bash", "ls")}}
	if IsAutoApproved(spec, "") {
		t.Error("empty key matched the auto-approved list")
	}
	if !IsAutoApproved(spec, MatchKey("Bash", "ls")) {
		t.Error("a real key stopped matching")
	}
}
