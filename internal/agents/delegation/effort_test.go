package delegation

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func TestDelegationEffortPrecedence(t *testing.T) {
	for _, tc := range []struct{ call, role, want string }{
		{"", "", ""},
		{"", "High", "high"},
		{" max ", "low", "max"},
		{"default", "high", "default"},
	} {
		if got := DelegationEffort(tc.call, tc.role); got != tc.want {
			t.Fatalf("DelegationEffort(%q, %q) = %q, want %q", tc.call, tc.role, got, tc.want)
		}
	}
}

func TestChildEffortPrefersTheDelegationsOwn(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if got := childEffort(layout, ChildSpec{Effort: "xhigh", ParentSessionID: "missing"}); got != "xhigh" {
		t.Fatalf("got %q", got)
	}
	if got := childEffort(layout, ChildSpec{}); got != "" {
		t.Fatalf("no parent, no effort: %q", got)
	}
}
