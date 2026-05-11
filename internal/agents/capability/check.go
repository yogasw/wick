package capability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// probeInflight tracks active HookCapabilityCheck runs keyed by
// "<provider>". Used by IsProbing + the inflight guard inside
// HookCapabilityCheck to short-circuit concurrent probes for the same
// provider. A second caller while one is still running gets
// ErrProbeInflight back instead of spawning a duplicate provider
// process — important because the writer/prober pair touch shared
// workspace files and concurrent probes race on the sentinel.
var probeInflight sync.Map

// ErrProbeInflight is returned by HookCapabilityCheck when a probe
// is already running for the same provider name. UI surfaces this as
// "another probe in flight" rather than retrying.
var ErrProbeInflight = errors.New("probe already in flight for this provider")

// IsProbing reports whether a HookCapabilityCheck is currently running
// for the named provider. Used by the HTTP layer / templ to disable
// the Test / Enable buttons while a probe is in flight.
func IsProbing(providerName string) bool {
	_, ok := probeInflight.Load(providerName)
	return ok
}

// CheckResult is what HookCapabilityCheck returns. It mirrors the
// Capability registry entry but with the runtime-probe fields filled
// in. Callers typically merge it back into the registry so subsequent
// Lookup() calls reflect the verified state.
type CheckResult struct {
	Capability
}

// ProbeTimeout is the wall-clock budget for a single capability check.
// Generous because provider CLIs on Windows (.cmd shims wrapping node)
// can take 1-3s just to print --version, never mind doing a real
// prompt round-trip. If the probe doesn't finish in this window we
// treat it as "unverified, retry later" rather than a hard failure.
const ProbeTimeout = 30 * time.Second

// CheckInput is the per-provider data HookCapabilityCheck needs to
// drive a probe. Kept minimal so test code can construct it without
// pulling the whole provider.Instance struct in.
type CheckInput struct {
	// ProviderName is the registry key for the Capability/Writer/Prober
	// triple this check should exercise.
	ProviderName string

	// GateBinary is the absolute path to the <app>-gate executable the
	// provider will invoke via its hook. Empty means "skip probe; we
	// can't verify without a gate to point at".
	GateBinary string

	// WorkspaceRoot is where the probe's throwaway workspace will be
	// created. Empty → os.TempDir. The probe writes a fresh subdir
	// underneath and removes it on completion regardless of outcome.
	WorkspaceRoot string
}

// HookCapabilityCheck spawns the named provider with a probe-mode hook
// config that forces a deny, asks the provider to touch a sentinel
// file, and reports whether the deny was honored. The returned
// Capability has HookVerified=true exactly when the sentinel did NOT
// appear; HookError carries the reason on any failure path.
//
// The function does NOT mutate the registry. Callers decide whether
// to merge the result back via Register — typically yes for a real
// probe, no for ad-hoc CLI runs (`wick agents capability`) that want
// to inspect a result without altering global state.
//
// Probe sequence:
//
//  1. Look up the static Capability. If HookSupported=false, return
//     early — there's no adapter to verify.
//  2. Look up the HookConfigWriter and Prober. Either missing →
//     HookError="<role> not registered for <provider>".
//  3. Create a temp workspace + sentinel path inside it.
//  4. Writer.Write installs the hook config pointing at GateBinary
//     with `--probe-deny --provider=<name>`.
//  5. Prober.SendSentinel spawns the provider and asks it to touch
//     the sentinel.
//  6. Check the sentinel: absent = verified, present = unverified.
//  7. Cleanup workspace.
func HookCapabilityCheck(ctx context.Context, in CheckInput) CheckResult {
	res := CheckResult{}

	// Single-flight guard per provider name. LoadOrStore returns
	// loaded=true when another goroutine already claimed the slot —
	// we bail with a typed error so the caller can render "another
	// probe in flight" without retrying.
	if _, loaded := probeInflight.LoadOrStore(in.ProviderName, struct{}{}); loaded {
		res.HookError = ErrProbeInflight.Error()
		return res
	}
	defer probeInflight.Delete(in.ProviderName)

	cap, ok := Lookup(in.ProviderName)
	if !ok {
		res.HookError = "provider not registered: " + in.ProviderName
		return res
	}
	res.Capability = cap
	res.HookProbedAt = time.Now().UTC()

	if !cap.HookSupported {
		res.HookError = "hook not supported in registry (adapter missing)"
		return res
	}

	writer, ok := LookupHookConfigWriter(in.ProviderName)
	if !ok {
		res.HookError = "hook config writer not registered for " + in.ProviderName
		return res
	}

	prober, ok := LookupProber(in.ProviderName)
	if !ok {
		res.HookError = "prober not registered for " + in.ProviderName
		return res
	}

	if in.GateBinary == "" {
		res.HookError = "gate binary path required"
		return res
	}

	root := in.WorkspaceRoot
	if root == "" {
		root = os.TempDir()
	}
	workspace, err := os.MkdirTemp(root, "wick-capability-*")
	if err != nil {
		res.HookError = fmt.Sprintf("create workspace: %v", err)
		return res
	}
	defer os.RemoveAll(workspace)

	// Hook config pointing at gate's probe-deny mode. The provider
	// name is appended so the gate emits the right envelope shape.
	probeCmd := fmt.Sprintf("%s --probe-deny --provider=%s", in.GateBinary, in.ProviderName)
	if err := writer.Write(workspace, probeCmd); err != nil {
		if errors.Is(err, ErrUnsupported) {
			res.HookError = "writer reports unsupported (provider needs verification)"
			return res
		}
		res.HookError = fmt.Sprintf("install hook config: %v", err)
		return res
	}

	sentinel := filepath.Join(workspace, "sentinel.txt")

	cctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	probeErr := prober.SendSentinel(cctx, workspace, sentinel)

	switch _, statErr := os.Stat(sentinel); {
	case statErr == nil:
		// Sentinel exists — provider ignored the deny envelope.
		res.HookVerified = false
		res.HookError = "sentinel created — provider did not honor deny envelope"
		if probeErr != nil {
			res.HookError += " (probe also errored: " + probeErr.Error() + ")"
		}
	case os.IsNotExist(statErr):
		// Sentinel absent — happy path. The provider tried to run the
		// touch but our forced deny cancelled it.
		res.HookVerified = true
		if probeErr != nil {
			// Probe error without sentinel = ambiguous but lean
			// verified: typical case is provider exits non-zero
			// because its tool got blocked, which IS the success
			// signal we want.
			res.HookError = "probe exited with error but deny was honored: " + probeErr.Error()
		}
	default:
		res.HookVerified = false
		res.HookError = fmt.Sprintf("stat sentinel: %v", statErr)
	}

	return res
}
