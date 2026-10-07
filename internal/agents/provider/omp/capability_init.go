package omp

import "github.com/yogasw/wick/internal/agents/capability"

// init registers omp with the capability registry as hook-less: no
// HookConfigWriter / Prober, so the gate stays off for omp instances.
func init() {
	capability.Register("omp", capability.Capability{HookSupported: false})
}
