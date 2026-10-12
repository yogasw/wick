package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/savedresets"
)

// usage_disk.go keeps the last good usage/quota reading of every account
// on disk, one file per account (keyed on logintty.UsageIdentity), so a
// restart, a reload or the failover checker starts from what was last
// read instead of probing the shared, rate-limited endpoint again. The
// file is only a starting point, like climodels_disk.go: the probe cache
// in tools/agents still owns pacing, single-flight and refresh, and
// writes the file back after every good reading.

// usageDiskDir is where the readings live; "" turns persistence off. Off
// under `go test` unless a test points it somewhere, so no test reads or
// writes the operator's real readings.
var usageDiskDir = func() string {
	if testing.Testing() {
		return ""
	}
	dir, err := modelStateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "usage")
}

// usageDiskMu serialises writers; readers go straight to the file, which
// is only ever replaced by an atomic rename.
var usageDiskMu sync.Mutex

// UsageDiskWindow is one normalised rate-limit window.
type UsageDiskWindow struct {
	Key         string    `json:"key"`
	Utilization float64   `json:"utilization"` // percent, 0-100
	ResetsAt    time.Time `json:"resets_at,omitempty"`
	// ObservedAt is when the provider reported the figure, when it says
	// (codex reads it from its last turn); zero = when it was read.
	ObservedAt time.Time `json:"observed_at,omitempty"`
	// Account tags a window of a multi-account reading (omp pool,
	// opencode folders); "" for single-account types.
	Account string `json:"account,omitempty"`
	// Error marks a placeholder (Key "") for an account that failed.
	Error string `json:"error,omitempty"`
}

// UsageDisk is one account's persisted reading. Key guards against a
// file left by another account whose identity hashes to the same name:
// a mismatch is ignored.
type UsageDisk struct {
	Key     string            `json:"key"`
	Windows []UsageDiskWindow `json:"windows"`
	// ObservedAt is when the last GOOD reading was taken; zero when the
	// account has only failed so far.
	ObservedAt  time.Time                `json:"observed_at"`
	SavedResets *savedresets.SavedResets `json:"saved_resets,omitempty"`

	// Probe bookkeeping, persisted so a restart keeps honouring it: when
	// the last probe ran, how many failed in a row (drives the backoff),
	// and a cooldown the server asked for (Retry-After).
	AttemptedAt time.Time `json:"attempted_at,omitempty"`
	Fails       int       `json:"fails,omitempty"`
	ServerUntil time.Time `json:"server_until,omitempty"`
	// LastError is the last failure, shown while there is no good
	// reading to show instead.
	LastError string `json:"last_error,omitempty"`
}

// windowObserved is when the vendor reported a window: its own
// ObservedAt (codex: the turn the figure came from) when set, else when
// the reading was taken.
func (d UsageDisk) windowObserved(w UsageDiskWindow) time.Time {
	if !w.ObservedAt.IsZero() {
		return w.ObservedAt
	}
	return d.ObservedAt
}

// Verdict is what the reading says about a limit recorded at since.
//
//   - A window is CLEAR when it is under 100% and was reported after
//     since, or when the reset it announced has passed. A figure from
//     before the limit (a codex snapshot of an earlier turn) proves
//     nothing about it.
//   - A window is FULL when it is at 100% and its reset has not passed.
//   - An account (windows grouped by Account; "" = the single account)
//     is recovered when every window is clear, blocked when any is full.
//
// recovered: at least one account is recovered — a pool (omp, opencode)
// rotates to it by itself. known=false when the reading has no say:
// no real window at all, or no account recovered and some unknown.
func (d UsageDisk) Verdict(now, since time.Time) (recovered, known bool) {
	type acct struct{ clear, full, unknown int }
	accts := map[string]*acct{}
	for _, w := range d.Windows {
		if w.Key == "" {
			continue // a failed-account placeholder
		}
		a := accts[w.Account]
		if a == nil {
			a = &acct{}
			accts[w.Account] = a
		}
		reset := !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)
		switch {
		case reset || (w.Utilization < 100 && d.windowObserved(w).After(since)):
			a.clear++
		case w.Utilization >= 100:
			a.full++
		default:
			a.unknown++
		}
	}
	if len(accts) == 0 {
		return false, false
	}
	allBlocked := true
	for _, a := range accts {
		if a.full == 0 && a.unknown == 0 {
			return true, true
		}
		if a.full == 0 {
			allBlocked = false
		}
	}
	return false, allBlocked
}

func usageDiskFile(key string) string {
	dir := usageDiskDir()
	if dir == "" || key == "" {
		return ""
	}
	// The identity can hold an email or a config path; the hash keeps the
	// name short and filesystem-safe, the type prefix keeps it readable.
	typ, _, _ := strings.Cut(key, ":")
	typ = strings.NewReplacer("/", "_", `\`, "_", "..", "_").Replace(typ)
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, typ+"-"+hex.EncodeToString(sum[:8])+".json")
}

// UsageDiskLookup reads the persisted reading of one account.
func UsageDiskLookup(key string) (UsageDisk, bool) {
	path := usageDiskFile(key)
	if path == "" {
		return UsageDisk{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return UsageDisk{}, false
	}
	var d UsageDisk
	if json.Unmarshal(b, &d) != nil || d.Key != key || (d.ObservedAt.IsZero() && d.AttemptedAt.IsZero()) {
		return UsageDisk{}, false
	}
	return d, true
}

// UsageDiskStore writes one account's state through: the last good
// reading (a failure never replaces it) plus the probe bookkeeping.
func UsageDiskStore(d UsageDisk) {
	path := usageDiskFile(d.Key)
	if path == "" || (d.ObservedAt.IsZero() && d.AttemptedAt.IsZero()) {
		return
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	usageDiskMu.Lock()
	defer usageDiskMu.Unlock()
	writeFileAtomic(path, b)
}

// UsageDiskDrop forgets one account's reading.
func UsageDiskDrop(key string) {
	if path := usageDiskFile(key); path != "" {
		usageDiskMu.Lock()
		_ = os.Remove(path)
		usageDiskMu.Unlock()
	}
}

// writeFileAtomic replaces path with b via a temp file + rename, so a
// reader never sees half a file. Best effort: a failure leaves the old
// file in place.
func writeFileAtomic(path string, b []byte) {
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// SetUsageDiskDirForTest points the usage files at dir ("" = off); the
// returned func restores the previous location. Tests only.
func SetUsageDiskDirForTest(dir string) (restore func()) {
	prev := usageDiskDir
	usageDiskDir = func() string { return dir }
	return func() { usageDiskDir = prev }
}
