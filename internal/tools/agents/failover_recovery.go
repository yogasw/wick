package agents

import (
	"context"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// failover_recovery.go brings accounts in the failover health registry
// (provider/health.go) back once they can run again.
//
// It must never become the thing that keeps a limit alive, so:
//
//   - one ticker, one minute; between due dates it only reads local
//     files (the account's usage file, health.json);
//   - an account is due only when the vendor's own reset (Until) has
//     passed AND a full recheck interval (the instance's
//     Failover.RecheckMinutes, 1..60, default 10) has gone by since it
//     was last checked;
//   - a due limit is re-read through the same paced, single-flight usage
//     cache every other reader uses — never a second probe path;
//   - a type with no usage API is only ever recovered by time
//     ("tentatively healthy"): the next real turn proves it, and a
//     failure marks it again;
//   - an auth failure is re-checked from the credential files only:
//     recovered when the login was rewritten after it failed;
//   - it never spawns a CLI or sends a prompt.

const failoverRecoveryTick = time.Minute

// failoverRecoveryProbeTimeout bounds how long one due account waits on
// its usage probe (pace slot included) before the tick moves on.
const failoverRecoveryProbeTimeout = 45 * time.Second

type failoverRecovery struct {
	now       func() time.Time
	accounts  func() map[string]provider.Health
	instances func() ([]provider.Instance, error)
	keyOf     func(provider.Instance) string
	// supportsUsage reports whether a type has a usage API to probe.
	supportsUsage func(provider.Type) bool
	// probe re-reads one account's usage through the paced cache and
	// returns once that reading (or a refusal) is in; the result lands
	// in the usage file, which is what the decision then reads.
	probe func(ins provider.Instance)
	// login reads an account's login from local files only: whether a
	// credential is present and when it last changed (zero = unknown).
	login func(ins provider.Instance) (connected bool, changedAt time.Time)
	run   func(func())

	mu       sync.Mutex
	inflight map[string]bool
}

// tick checks every recorded account once.
func (r *failoverRecovery) tick() {
	accounts := r.accounts()
	if len(accounts) == 0 {
		return
	}
	all, err := r.instances()
	if err != nil {
		return // a config read failing must not read as "every instance gone"
	}
	byKey := map[string][]provider.Instance{}
	for _, ins := range all {
		k := r.keyOf(ins)
		byKey[k] = append(byKey[k], ins)
	}
	for key, h := range accounts {
		insts := byKey[key]
		if len(insts) == 0 {
			// No instance uses this login any more: nothing to recover.
			provider.MarkHealthy(key)
			continue
		}
		r.mu.Lock()
		if r.inflight[key] {
			r.mu.Unlock()
			continue // single flight per account
		}
		r.inflight[key] = true
		r.mu.Unlock()
		key, h := key, h
		r.run(func() {
			defer func() {
				r.mu.Lock()
				delete(r.inflight, key)
				r.mu.Unlock()
			}()
			r.check(key, h, insts)
		})
	}
}

// check decides one account. Every clear goes through MarkHealthyIf
// with the Since it decided about, so a failure recorded while a probe
// was running is never erased by it.
func (r *failoverRecovery) check(key string, h provider.Health, insts []provider.Instance) {
	now := r.now()
	every := recheckEvery(insts)
	if h.Recheck <= 0 {
		h.Recheck = every
	}
	// A fresh reading already on disk (a manual reset, a Re-check) is
	// free to act on, due or not.
	if cleared, known := provider.UsageClears(key, h, now); known && cleared {
		provider.MarkHealthyByUsage(key, h.Since)
		return
	}
	if !recoveryDue(h, now, every) {
		return
	}
	ins := insts[0]
	switch h.Kind {
	case event.ErrAuth:
		connected, changedAt := r.login(ins)
		// Recovered when the login was rewritten after it failed, or the
		// instance's config/env changed (a new key, another dir).
		if (connected && changedAt.After(h.Since)) || fingerprintChanged(h, insts) {
			provider.MarkHealthyIf(key, h.Since)
			return
		}
		// Nothing proves it fixed: open the next tentative retry window,
		// backed off 10, 20, 40, 60 min after the last failure. The next
		// real turn proves it; a failure closes the window again.
		if h.RetryAt.IsZero() || !h.RetryAt.After(h.FailedAt) {
			failed := h.FailedAt
			if failed.IsZero() {
				failed = h.Since
			}
			provider.ScheduleRetry(key, h.Since, failed.Add(provider.AuthRetryBackoff(h.Tries+1)), h.Tries+1)
		}
		return
	case event.ErrLimit:
		if r.supportsUsage(ins.Type) && !h.TimeOnly {
			// omp included: `omp usage` is the same paced usage path the
			// panel uses, not a prompt; it is never used for auth. A
			// TimeOnly limit is one usage cannot see: no probe.
			r.probe(ins)
			cleared, known := provider.UsageClears(key, h, r.now())
			if known && !cleared {
				break // the reading says it is still full
			}
			if known {
				provider.MarkHealthyByUsage(key, h.Since)
				return
			}
		}
		// Cleared, or no reading with a say (probe failed, declined,
		// empty): the vendor's reset — or one recheck interval when it
		// named none — has passed, so the account is tentatively
		// healthy. The next real turn proves it; a failure marks it
		// again. Nothing stays limited forever on a broken usage API.
		provider.MarkHealthyIf(key, h.Since)
		return
	default:
		// Unavailable (vendor down) and anything else: time-based.
		provider.MarkHealthyIf(key, h.Since)
		return
	}
	provider.TouchHealth(key, r.now())
}

// fingerprintChanged: the failure recorded a fingerprint and no
// instance on this login has it any more.
func fingerprintChanged(h provider.Health, insts []provider.Instance) bool {
	if h.Fingerprint == "" {
		return false
	}
	for _, ins := range insts {
		if provider.InstanceFingerprint(ins) == h.Fingerprint {
			return false
		}
	}
	return true
}

// recoveryDue: past EffectiveUntil (the vendor's reset, or one recheck
// interval after the failure when it named none) and a full recheck
// interval since the last check.
func recoveryDue(h provider.Health, now time.Time, every time.Duration) bool {
	if now.Before(h.EffectiveUntil()) {
		return false
	}
	return !now.Before(h.CheckedAt.Add(every))
}

// recheckEvery is the shortest recheck interval among the instances on
// one login; unset reads as the default.
func recheckEvery(insts []provider.Instance) time.Duration {
	mins := 0
	for _, ins := range insts {
		m := provider.DefaultFailoverRecheckMinutes
		if ins.Failover != nil && ins.Failover.RecheckMinutes > 0 {
			m = ins.Failover.RecheckMinutes
		}
		m = min(max(m, provider.MinFailoverRecheckMinutes), provider.MaxFailoverRecheckMinutes)
		if mins == 0 || m < mins {
			mins = m
		}
	}
	if mins == 0 {
		mins = provider.DefaultFailoverRecheckMinutes
	}
	return time.Duration(mins) * time.Minute
}

// usageFetchFor is the paced cache's fetch pair for one instance.
func usageFetchFor(ins provider.Instance) (string, usageFetch, resetsFetch) {
	env := provider.AccountEnv(ins)
	return logintty.UsageIdentity(ins.Type, env), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, env)
	}, instanceResetsFetch(ins.Type, env)
}

// refreshUsageByKey is provider.RequestRefresh's hook: a paced refresh of
// whichever instance logs in as key. Non-blocking.
func refreshUsageByKey(key string) {
	all, err := provider.Load()
	if err != nil {
		return
	}
	for _, ins := range all {
		if ins.Disabled || !logintty.SupportsUsage(ins.Type) {
			continue
		}
		if k, fetch, resets := usageFetchFor(ins); k == key {
			usageProbes.autoRefresh(k, fetch, resets)
			return
		}
	}
}

// newFailoverRecovery builds the live checker; ctx bounds its probe waits
// so a shutdown does not sit out a pace slot.
func newFailoverRecovery(ctx context.Context) *failoverRecovery {
	return &failoverRecovery{
		now:      time.Now,
		accounts: provider.UnhealthyAccounts,
		instances: func() ([]provider.Instance, error) {
			all, err := provider.Load()
			if err != nil {
				return nil, err
			}
			out := all[:0]
			for _, ins := range all {
				if !ins.Disabled {
					out = append(out, ins)
				}
			}
			return out, nil
		},
		keyOf:         provider.HealthKey,
		supportsUsage: logintty.SupportsUsage,
		probe: func(ins provider.Instance) {
			k, fetch, resets := usageFetchFor(ins)
			ch := usageProbes.autoRefresh(k, fetch, resets)
			if ch == nil {
				return
			}
			ctx, cancel := context.WithTimeout(ctx, failoverRecoveryProbeTimeout)
			defer cancel()
			select {
			case <-ch:
			case <-ctx.Done():
			}
		},
		login: func(ins provider.Instance) (bool, time.Time) {
			env := provider.AccountEnv(ins)
			changedAt := logintty.CredentialsChangedAt(ins.Type, env)
			if ins.Type == provider.TypeOMP {
				// omp's ReadAccount runs `omp usage` (a subprocess that
				// calls the vendor); the credential store's mtime is the
				// local signal.
				return !changedAt.IsZero(), changedAt
			}
			return logintty.ReadAccount(ins.Type, env).Connected, changedAt
		},
		run:      func(f func()) { go f() },
		inflight: map[string]bool{},
	}
}

// Run ticks every interval until ctx ends.
func (r *failoverRecovery) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick()
		}
	}
}

var (
	failoverRecoveryMu      sync.Mutex
	failoverRecoveryStarted bool
	failoverRecoveryStop    context.CancelFunc
	failoverRecoveryDone    chan struct{}
)

// startFailoverRecovery starts the checker and installs the paced
// refresh behind provider.RequestRefresh. Idempotent; a stopped checker
// stays stopped.
func startFailoverRecovery() {
	failoverRecoveryMu.Lock()
	defer failoverRecoveryMu.Unlock()
	if failoverRecoveryStarted {
		return
	}
	failoverRecoveryStarted = true
	provider.SetUsageRefreshHook(refreshUsageByKey)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	failoverRecoveryStop, failoverRecoveryDone = cancel, done
	r := newFailoverRecovery(ctx)
	go func() {
		defer close(done)
		r.Run(ctx, failoverRecoveryTick)
	}()
}

// StopFailoverRecovery stops the checker at shutdown and waits for its
// loop to return. A check already handed to its own goroutine finishes
// on its own; its probe wait ends with the context. Safe to call more
// than once, or when the checker never started.
func StopFailoverRecovery() {
	failoverRecoveryMu.Lock()
	stop, done := failoverRecoveryStop, failoverRecoveryDone
	failoverRecoveryStop, failoverRecoveryDone = nil, nil
	failoverRecoveryMu.Unlock()
	if stop == nil {
		return
	}
	stop()
	<-done
}
