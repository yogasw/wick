package wick

import (
	"github.com/yogasw/wick/internal/agents/capability"
)

// init registers wick with the capability registry.
//
// HookSupported is false on purpose: there is no subprocess to install
// a PreToolUse hook config into. The gate still applies — enforced
// natively via the runner's before-tool callback (see gate_callback.go)
// — but the hook-probe machinery (writer + prober + capability check)
// is meaningless in-process, so none of it is registered. The UI reads
// InterceptScope "in-process" to render the "gate enforced natively"
// copy instead of the probe/enable flow.
func init() {
	capability.Register("wick", capability.Capability{
		HookSupported:  false,
		InterceptScope: "in-process",
	})
}
