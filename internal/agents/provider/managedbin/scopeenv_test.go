package managedbin

import (
	"strings"
	"testing"
)

// The wrapped --version runs under systemd-run --user --scope, which cannot
// reach the user manager without these two. Losing them failed every managed
// install on the host with "Failed to connect to bus: No medium found".
func TestScopeBusEnvCarriesUserManagerLocation(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	got := strings.Join(scopeBusEnv(), "\n")
	for _, want := range []string{"XDG_RUNTIME_DIR=/run/user/1000", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus"} {
		if !strings.Contains(got, want) {
			t.Errorf("scopeBusEnv() = %q, missing %q", got, want)
		}
	}
}

func TestScopeBusEnvSkipsUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if got := scopeBusEnv(); len(got) != 0 {
		t.Errorf("scopeBusEnv() = %q, want empty", got)
	}
}
