package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// health.go is the failover health registry: which provider ACCOUNTS are
// currently unusable (limit, auth, vendor down) and until when. It is
// keyed on the login, not the instance (HealthKey), so two instances on
// one account share one verdict. Entries persist to health.json under
// the model-state dir, so a restart does not walk straight back into an
// account that is still limited.
//
// The registry only records; deciding is HealthyNow, which also reads
// the account's usage file (usage_disk.go) so a limit someone reset by
// hand counts as recovered before the error's own reset time.

// UsageFreshFor is how old a usage reading may be and still overrule a
// recorded limit. An older one is not trusted either way.
const UsageFreshFor = 10 * time.Minute

// Health is one unhealthy account.
type Health struct {
	Kind event.ErrKind `json:"kind"`
	// Since is when the account was first marked unhealthy in this run
	// of failures; a usage reading only counts if it is newer.
	Since time.Time `json:"since"`
	// Until is when the vendor said the limit lifts (zero = unknown).
	Until time.Time `json:"until,omitempty"`
	// CheckedAt is the last time the account was marked or re-checked.
	CheckedAt time.Time `json:"checked_at"`
	Reason    string    `json:"reason,omitempty"`
	// Recheck is the instance's recheck interval, the wait used when the
	// error named no reset time; zero = the default.
	Recheck time.Duration `json:"recheck,omitempty"`
	// Fingerprint is InstanceFingerprint of the instance that failed: a
	// changed config/env (a new key, another dir) is a recovery signal.
	Fingerprint string `json:"fingerprint,omitempty"`
	// FailedAt is the latest failure recorded (CheckedAt also moves on
	// re-checks). RetryAt/Tries are the auth retry windows: past RetryAt
	// (when later than FailedAt) the login is tentatively usable; Tries
	// drives the backoff between windows.
	FailedAt time.Time `json:"failed_at,omitempty"`
	RetryAt  time.Time `json:"retry_at,omitempty"`
	Tries    int       `json:"tries,omitempty"`
	// TimeOnly: this limit failed again right after a usage reading said
	// it had cleared, so it sits in a window the usage API does not
	// report (a per-model cap, say). Usage readings get no vote on it;
	// only EffectiveUntil brings it back.
	TimeOnly bool `json:"time_only,omitempty"`
}

// AuthRetryBackoff is the wait before the n-th tentative retry of a
// broken login nothing proved fixed: 10, 20, 40, then 60 minutes.
func AuthRetryBackoff(n int) time.Duration {
	d := 10 * time.Minute
	for i := 1; i < n && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// InstanceFingerprint hashes what picks an instance's account (type,
// name, account env) — never stored in plaintext, since the env holds
// keys.
func InstanceFingerprint(ins Instance) string {
	env := append([]string(nil), AccountEnv(ins)...)
	sort.Strings(env)
	sum := sha256.Sum256([]byte(string(ins.Type) + "\x00" + ins.Name + "\x00" + strings.Join(env, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// MarkInstanceUnhealthy is MarkUnhealthy keyed and filled for one
// instance: HealthKey, its Fingerprint and its recheck interval.
func MarkInstanceUnhealthy(ins Instance, h Health) {
	if h.Fingerprint == "" {
		h.Fingerprint = InstanceFingerprint(ins)
	}
	if h.Recheck == 0 && ins.Failover != nil && ins.Failover.RecheckMinutes > 0 {
		h.Recheck = time.Duration(ins.Failover.RecheckMinutes) * time.Minute
	}
	MarkUnhealthy(HealthKey(ins), h)
}

// ResetHealth forgets an account's failure outright — the UI's reset
// button. The next turn proves whether it was right.
func ResetHealth(key string) { MarkHealthy(key) }

// ScheduleRetry opens the next tentative retry window of a failure that
// is still the one on record (Since unchanged).
func ScheduleRetry(key string, since, at time.Time, tries int) {
	healthMu.Lock()
	defer healthMu.Unlock()
	healthLoadLocked()
	h, ok := healthMap[key]
	if !ok || !h.Since.Equal(since) {
		return
	}
	h.RetryAt, h.Tries = at, tries
	healthMap[key] = h
	healthSaveLocked()
}

// failedAt is FailedAt, or Since for a record written before it existed.
func (h Health) failedAt() time.Time {
	if !h.FailedAt.IsZero() {
		return h.FailedAt
	}
	return h.Since
}

// retryOpen: an auth retry window was opened after the last failure and
// has started.
func (h Health) retryOpen(now time.Time) bool {
	return !h.RetryAt.IsZero() && h.RetryAt.After(h.failedAt()) && !now.Before(h.RetryAt)
}

// EffectiveUntil is when time alone lets the account be tried again:
// the vendor's reset when it named one, else one recheck interval after
// the failure. Never zero, so no account stays limited forever on time.
func (h Health) EffectiveUntil() time.Time {
	if !h.Until.IsZero() {
		return h.Until
	}
	every := h.Recheck
	if every <= 0 {
		every = time.Duration(DefaultFailoverRecheckMinutes) * time.Minute
	}
	// From the latest failure, not the first: an account that keeps
	// failing stays out until one interval passes without a failure.
	last := h.failedAt()
	if h.Since.After(last) {
		last = h.Since
	}
	return last.Add(every)
}

// healthDir is where health.json lives; "" keeps the registry in memory
// only. Off under `go test` unless a test points it somewhere.
var healthDir = func() string {
	if testing.Testing() {
		return ""
	}
	dir, err := modelStateDir()
	if err != nil {
		return ""
	}
	return dir
}

var (
	healthMu     sync.RWMutex
	healthMap    = map[string]Health{}
	healthLoaded bool
	healthNow    = time.Now
	// usageClearedAt is when a usage reading last cleared each account
	// (memory only): a limit coming back within one recheck interval
	// of it is TimeOnly.
	usageClearedAt = map[string]time.Time{}
)

// HealthKeyFunc names the account behind an instance. logintty fills it
// with UsageIdentity (provider cannot import logintty); nil or an empty
// answer falls back to "type/name".
var HealthKeyFunc func(ins Instance) string

// HealthKey is the registry key of an instance.
func HealthKey(ins Instance) string {
	if HealthKeyFunc != nil {
		if k := HealthKeyFunc(ins); k != "" {
			return k
		}
	}
	return string(ins.Type) + "/" + ins.Name
}

func healthFile() string {
	dir := healthDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "health.json")
}

// healthLoadLocked fills the map from disk once. Callers hold healthMu
// for writing.
func healthLoadLocked() {
	if healthLoaded {
		return
	}
	healthLoaded = true
	path := healthFile()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]Health
	if json.Unmarshal(b, &m) != nil {
		return
	}
	for k, h := range m {
		if k != "" && h.Kind != event.ErrNone {
			healthMap[k] = h
		}
	}
}

// healthSaveLocked writes the map through. Callers hold healthMu.
func healthSaveLocked() {
	path := healthFile()
	if path == "" {
		return
	}
	b, err := json.Marshal(healthMap)
	if err != nil {
		return
	}
	writeFileAtomic(path, b)
}

func healthEnsureLoaded() {
	healthMu.RLock()
	loaded := healthLoaded
	healthMu.RUnlock()
	if !loaded {
		healthMu.Lock()
		healthLoadLocked()
		healthMu.Unlock()
	}
}

// MarkUnhealthy records a failure. Idempotent: a repeat keeps the first
// Since and the latest Until, so the same error arriving twice (two
// instances on one login, a retry) never shortens the wait.
func MarkUnhealthy(key string, h Health) {
	if key == "" || h.Kind == event.ErrNone {
		return
	}
	now := healthNow()
	if h.CheckedAt.IsZero() {
		h.CheckedAt = now
	}
	if h.Since.IsZero() {
		h.Since = h.CheckedAt
	}
	if h.FailedAt.IsZero() {
		h.FailedAt = h.CheckedAt
	}
	contradicted := h.Kind == event.ErrLimit && usageContradicted(key, h)
	healthMu.Lock()
	defer healthMu.Unlock()
	healthLoadLocked()
	if contradicted {
		h.TimeOnly = true
		delete(usageClearedAt, key)
	}
	if prev, ok := healthMap[key]; ok {
		// A limit that just turned TimeOnly starts a new run: its wait
		// counts from this failure, not from the one usage cleared.
		if prev.Since.Before(h.Since) && (prev.TimeOnly || !h.TimeOnly) {
			h.Since = prev.Since
		}
		if prev.TimeOnly && prev.Kind == h.Kind {
			h.TimeOnly = true
		}
		if prev.Until.After(h.Until) {
			h.Until = prev.Until
		}
		if prev.CheckedAt.After(h.CheckedAt) {
			h.CheckedAt = prev.CheckedAt
		}
		if h.Reason == "" {
			h.Reason = prev.Reason
		}
		if h.Recheck == 0 {
			h.Recheck = prev.Recheck
		}
		if h.Fingerprint == "" {
			h.Fingerprint = prev.Fingerprint
		}
		if prev.FailedAt.After(h.FailedAt) {
			h.FailedAt = prev.FailedAt
		}
		// The retry schedule survives a repeat; a failure after the
		// window opened closes it (retryOpen compares with FailedAt).
		if h.RetryAt.IsZero() {
			h.RetryAt, h.Tries = prev.RetryAt, prev.Tries
		}
		if prev == h {
			return
		}
	}
	healthMap[key] = h
	healthSaveLocked()
}

// usageContradicted: a limit is failing again although the usage file
// said it had cleared, either right now (a fresh reading clears the
// entry still on record, which is how HealthyNow let it run) or by
// clearing it within one recheck interval of this failure.
func usageContradicted(key string, h Health) bool {
	if prev, ok := HealthOf(key); ok {
		if prev.Kind != event.ErrLimit || prev.TimeOnly {
			return false
		}
		cleared, known := UsageClears(key, prev, h.FailedAt)
		return known && cleared
	}
	healthMu.RLock()
	at, ok := usageClearedAt[key]
	healthMu.RUnlock()
	every := h.Recheck
	if every <= 0 {
		every = time.Duration(DefaultFailoverRecheckMinutes) * time.Minute
	}
	return ok && !h.FailedAt.Before(at) && h.FailedAt.Sub(at) < every
}

// MarkHealthyByUsage is MarkHealthyIf for a clear a usage reading
// decided; it remembers when, so a limit back within one recheck
// interval is known to come from a window usage does not report.
func MarkHealthyByUsage(key string, since time.Time) bool {
	if !MarkHealthyIf(key, since) {
		return false
	}
	healthMu.Lock()
	usageClearedAt[key] = healthNow()
	healthMu.Unlock()
	return true
}

// MarkHealthy clears an account.
func MarkHealthy(key string) {
	healthMu.Lock()
	defer healthMu.Unlock()
	healthLoadLocked()
	if _, ok := healthMap[key]; !ok {
		return
	}
	delete(healthMap, key)
	healthSaveLocked()
}

// MarkHealthyIf clears an account only if the failure on record is the
// one the caller decided about (its Since is not newer than since): a
// check that took a while must not erase a failure recorded meanwhile.
func MarkHealthyIf(key string, since time.Time) bool {
	healthMu.Lock()
	defer healthMu.Unlock()
	healthLoadLocked()
	h, ok := healthMap[key]
	if !ok || h.Since.After(since) {
		return false
	}
	delete(healthMap, key)
	healthSaveLocked()
	return true
}

// TouchHealth records a re-check that did not recover the account, so
// the next one waits a full interval.
func TouchHealth(key string, at time.Time) {
	healthMu.Lock()
	defer healthMu.Unlock()
	healthLoadLocked()
	h, ok := healthMap[key]
	if !ok || !at.After(h.CheckedAt) {
		return
	}
	h.CheckedAt = at
	healthMap[key] = h
	healthSaveLocked()
}

// HealthOf returns the recorded state of an account; ok=false = healthy.
func HealthOf(key string) (Health, bool) {
	healthEnsureLoaded()
	healthMu.RLock()
	defer healthMu.RUnlock()
	h, ok := healthMap[key]
	return h, ok
}

// UnhealthyAccounts is a snapshot of every recorded account.
func UnhealthyAccounts() map[string]Health {
	healthEnsureLoaded()
	healthMu.RLock()
	defer healthMu.RUnlock()
	out := make(map[string]Health, len(healthMap))
	for k, h := range healthMap {
		out[k] = h
	}
	return out
}

// HealthyNow decides whether an account may be used at now. Cheap (one
// small file read), so it is meant to run before every spawn/send.
//
// Order matters:
//  1. nothing recorded → healthy;
//  2. a limit with a FRESH usage reading that has a say (Verdict known)
//     decides on its own: cleared → healthy now, even before Until
//     (someone reset it by hand); still full → unhealthy;
//  3. otherwise time: past EffectiveUntil → healthy, tentatively. The
//     next real turn proves it, and a failure marks it again.
//
// Pure: it never changes the registry. The checker or the caller acts
// on the answer.
func HealthyNow(key string, now time.Time) (bool, Health) {
	h, ok := HealthOf(key)
	if !ok {
		return true, Health{}
	}
	if cleared, known := UsageClears(key, h, now); known {
		return cleared, h
	}
	if h.Kind == event.ErrAuth && h.Until.IsZero() {
		// A broken login does not heal with time alone: the checker
		// clears it when the credentials or the instance change, and
		// otherwise opens backed-off retry windows (10, 20, 40, 60 min).
		return h.retryOpen(now), h
	}
	return !now.Before(h.EffectiveUntil()), h
}

// UsageClears is the usage file's verdict on a recorded limit: known is
// false when there is no fresh reading with a say about it (or h is not
// a limit, or a TimeOnly one), and then the file has no vote.
func UsageClears(key string, h Health, now time.Time) (cleared, known bool) {
	if h.Kind != event.ErrLimit || h.TimeOnly {
		return false, false
	}
	d, ok := UsageDiskLookup(key)
	if !ok || !usageFresh(d, now) {
		return false, false
	}
	return d.Verdict(now, h.Since)
}

func usageFresh(d UsageDisk, now time.Time) bool {
	return !d.ObservedAt.IsZero() && now.Sub(d.ObservedAt) <= UsageFreshFor
}

var (
	usageRefreshMu   sync.RWMutex
	usageRefreshHook func(key string)
)

// SetUsageRefreshHook installs the paced usage refresh (tools/agents).
// The hook must not block.
func SetUsageRefreshHook(f func(key string)) {
	usageRefreshMu.Lock()
	usageRefreshHook = f
	usageRefreshMu.Unlock()
}

// RequestRefresh asks for a new usage reading of an account that is
// marked limited and whose file is missing or stale. Non-blocking: the
// caller goes on with what it has and sees the result on a later call.
// The hook's own pacing decides whether a probe actually runs.
func RequestRefresh(key string) bool {
	h, ok := HealthOf(key)
	if !ok || h.Kind != event.ErrLimit {
		return false
	}
	if d, ok := UsageDiskLookup(key); ok && usageFresh(d, healthNow()) {
		return false
	}
	usageRefreshMu.RLock()
	f := usageRefreshHook
	usageRefreshMu.RUnlock()
	if f == nil {
		return false
	}
	go f(key)
	return true
}

// SetHealthDirForTest points health.json at dir ("" = memory only) and
// empties the registry; the returned func restores both. Tests only.
func SetHealthDirForTest(dir string) (restore func()) {
	healthMu.Lock()
	prev := healthDir
	healthDir = func() string { return dir }
	healthMap, healthLoaded = map[string]Health{}, false
	usageClearedAt = map[string]time.Time{}
	healthMu.Unlock()
	return func() {
		healthMu.Lock()
		healthDir = prev
		healthMap, healthLoaded = map[string]Health{}, false
		usageClearedAt = map[string]time.Time{}
		healthMu.Unlock()
	}
}

// RecordUsage writes a good usage reading through to the account's file
// and, when it shows a recorded limit has cleared (a manual reset, a
// Re-check after the window rolled over), marks the account healthy on
// the spot. Every usage probe ends here, so the failover registry never
// needs a probe of its own.
func RecordUsage(d UsageDisk) {
	UsageDiskStore(d)
	h, ok := HealthOf(d.Key)
	if !ok || h.Kind != event.ErrLimit || !usageFresh(d, healthNow()) {
		return
	}
	if h.TimeOnly {
		return
	}
	if cleared, known := d.Verdict(healthNow(), h.Since); known && cleared {
		MarkHealthyByUsage(d.Key, h.Since)
	}
}
