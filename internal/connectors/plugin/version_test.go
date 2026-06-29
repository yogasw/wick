package plugin

import "testing"

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.4.2", "1.4.1", true},   // patch bump
		{"v1.4.2", "1.4.1", true},  // mixed prefix
		{"1.5.0", "1.4.9", true},   // minor bump
		{"2.0.0", "1.9.9", true},   // major bump
		{"1.4.2", "1.4.2", false},  // equal
		{"1.4.1", "1.4.2", false},  // older
		{"v1.4.1", "v1.4.2", false},
		{"", "1.0.0", false},       // empty catalog version → no update
		{"weird", "1.0.0", true},   // unparseable but different → surface a hint
		{"weird", "weird", false},  // unparseable + equal → no hint
	}
	for _, c := range cases {
		if got := VersionNewer(c.a, c.b); got != c.want {
			t.Errorf("VersionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
