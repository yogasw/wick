package agentmemory

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// An instance with no key of its own uses the daemon's.
//
// The managed daemon's bearer is configured once, in the panel's Settings tab.
// Before this, a spawn presented only the INSTANCE's key — so the moment
// somebody set a daemon token, every instance that had not separately been
// given the same string was refused, and the refusal surfaced as an
// unexplained 401 in the middle of an agent turn. The field is meant to be an
// override, not a second place the same value has to be typed.
func TestInstanceInheritsTheDaemonsBearer(t *testing.T) {
	be := testBackend(&fakeData{})
	be.Mgr.SetLaunchOptions(LaunchOptions{AuthToken: "daemon-bearer"})

	if got := resolveKey(be, provider.Instance{}); got != "daemon-bearer" {
		t.Fatalf("key = %q, want the daemon's — an empty field means inherit", got)
	}
}

// An explicit key still wins: that is what an override is.
func TestInstanceKeyOverridesTheDaemons(t *testing.T) {
	be := testBackend(&fakeData{})
	be.Mgr.SetLaunchOptions(LaunchOptions{AuthToken: "daemon-bearer"})

	got := resolveKey(be, provider.Instance{AgentMemoryAuthKey: "instance-bearer"})
	if got != "instance-bearer" {
		t.Fatalf("key = %q, want the instance's own", got)
	}
}

// The one that matters for safety: an instance pointed at somebody ELSE's
// server must never be handed wick's bearer. Inheriting there would send a
// credential to a third-party host because a field was left blank — a leak
// caused by a convenience.
func TestARemoteServerNeverInheritsWicksBearer(t *testing.T) {
	be := testBackend(&fakeData{})
	be.Mgr.SetLaunchOptions(LaunchOptions{AuthToken: "daemon-bearer"})

	ins := provider.Instance{AgentMemoryServerURL: "https://memory.example.com"}
	if got := resolveKey(be, ins); got != "" {
		t.Fatalf("key = %q, want empty: wick's own token must not travel to a server it does not run", got)
	}
}

// No daemon token, no instance token: nothing is presented, which is the
// state every host starts in.
func TestNoTokenAnywhereMeansNoBearer(t *testing.T) {
	be := testBackend(&fakeData{})
	if got := resolveKey(be, provider.Instance{}); got != "" {
		t.Fatalf("key = %q, want empty", got)
	}
}
