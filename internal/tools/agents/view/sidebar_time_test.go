package view

import "testing"

func TestRelativeAge(t *testing.T) {
	const now = int64(10_000_000_000)
	cases := []struct {
		name string
		ago  int64
		want string
	}{
		{"just now", 0, "now"},
		{"under a minute", 59_999, "now"},
		{"one minute", 60_000, "1m"},
		{"minutes floor", 3*60_000 + 59_000, "3m"},
		{"last minute of the hour", 3_599_999, "59m"},
		{"one hour", 3_600_000, "1h"},
		{"hours", 2*3_600_000 + 1, "2h"},
		{"one day", 86_400_000, "1d"},
		{"days", 5*86_400_000 + 7, "5d"},
		{"clock skew reads as now", -5_000, "now"},
	}
	for _, c := range cases {
		if got := RelativeAge(now-c.ago, now); got != c.want {
			t.Errorf("%s: RelativeAge(-%d) = %q, want %q", c.name, c.ago, got, c.want)
		}
	}
	// A session with no recorded activity has no age to show.
	if got := RelativeAge(0, now); got != "" {
		t.Errorf("RelativeAge(0) = %q, want empty", got)
	}
}

func TestIsRunningStatus(t *testing.T) {
	for _, s := range []string{"working", "spawning", "subagent", "queued"} {
		if !IsRunningStatus(s) {
			t.Errorf("IsRunningStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"idle", "killed", ""} {
		if IsRunningStatus(s) {
			t.Errorf("IsRunningStatus(%q) = true, want false", s)
		}
	}
}
