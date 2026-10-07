package opencode

import "github.com/yogasw/wick/internal/agents/capability"

// init registers opencode with the capability registry as hook-less: no
// HookConfigWriter / Prober, so the gate stays off for opencode instances.
func init() {
	capability.Register("opencode", capability.Capability{HookSupported: false})
}
