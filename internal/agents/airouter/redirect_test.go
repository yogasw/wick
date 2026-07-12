package airouter

import (
	"context"
	"testing"
)

type redirectFakeStore struct{}

func (redirectFakeStore) Enabled() bool                                            { return true }
func (redirectFakeStore) AccessAllowed(context.Context) bool                       { return true }
func (redirectFakeStore) GetAutostart(string) bool                                 { return false }
func (redirectFakeStore) SetAutostart(context.Context, string, bool) error         { return nil }
func (redirectFakeStore) GetExternalAPI(string) bool                               { return false }
func (redirectFakeStore) SetExternalAPI(context.Context, string, bool) error       { return nil }
func (redirectFakeStore) ExternalAPIAllowed(string) bool                           { return false }

func TestRedirectTargetFor(t *testing.T) {
	store = redirectFakeStore{}
	t.Cleanup(func() { store = nil })
	Register(Descriptor{ID: "rt-omni", RoutePrefixes: []string{"/home", "/settings"}})

	// Referer under /airouter/<id>/ resolves the router directly.
	if got := RedirectTargetFor("/home", "http://x/airouter/rt-omni/dashboard"); got != "/airouter/rt-omni/home" {
		t.Fatalf("referer redirect = %q, want /airouter/rt-omni/home", got)
	}
	// Subpath of a declared route is re-rooted too, query preserved by caller.
	if got := RedirectTargetFor("/settings/keys", "http://x/airouter/rt-omni/"); got != "/airouter/rt-omni/settings/keys" {
		t.Fatalf("subpath redirect = %q", got)
	}
	// Service-worker-relayed request (referer /sw.js) → active-asset fallback.
	markAssetRouter("rt-omni")
	if got := RedirectTargetFor("/home", "http://x/sw.js"); got != "/airouter/rt-omni/home" {
		t.Fatalf("active-asset fallback = %q", got)
	}
	// A path that isn't a declared router route is left to normal wick routing.
	if got := RedirectTargetFor("/tools/agents", "http://x/airouter/rt-omni/"); got != "" {
		t.Fatalf("unrelated path should not redirect, got %q", got)
	}
}
