package provider

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/pkg/safeexec"
)

// helperreg.go records every helper process HelperCommand builds, so none
// is ever missed: its label, instance, scope unit, start and deadline (and
// its pid once started). The timeout already kills a helper's group; the
// reaper is the net under that — an entry still registered past its
// deadline (release never ran, a kill that did not land) is killed and
// dropped. At boot, helper scopes a previous wick process left running are
// stopped. Every kill is logged.
//
// The registry never reads cmd.Process: Start writes it in the owner's
// goroutine with no lock, so the reaper kills through the helper's context
// instead (exec then runs cmd.Cancel → the group kill on its own side).

// HelperRecord is one registered helper.
type HelperRecord struct {
	Seq      int
	Label    string
	Instance string
	Unit     string
	Start    time.Time
	Deadline time.Time
}

type helperEntry struct {
	rec     HelperRecord
	cancel  context.CancelFunc // ends the helper (exec kills its group)
	release func()             // its scope
}

var (
	helperRegMu sync.Mutex
	helperReg   = map[int]*helperEntry{}
	helperNow   = time.Now
)

// reaperGrace is how far past its deadline an entry must be before the
// reaper acts (the timeout's own kill gets there first when it works).
const reaperGrace = 15 * time.Second

func registerHelper(rec HelperRecord, cancel context.CancelFunc, releaseScope func()) {
	helperRegMu.Lock()
	helperReg[rec.Seq] = &helperEntry{rec: rec, cancel: cancel, release: releaseScope}
	helperRegMu.Unlock()
}

func unregisterHelper(seq int) {
	helperRegMu.Lock()
	delete(helperReg, seq)
	helperRegMu.Unlock()
}

// Helpers lists the registered helpers, oldest first (logs, Resources).
func Helpers() []HelperRecord {
	helperRegMu.Lock()
	out := make([]HelperRecord, 0, len(helperReg))
	for _, e := range helperReg {
		out = append(out, e.rec)
	}
	helperRegMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// ReapHelpers kills every registered helper past deadline + grace (its
// whole process group), releases its scope and drops it. Returns how many.
func ReapHelpers() int {
	now := helperNow()
	helperRegMu.Lock()
	var due []*helperEntry
	for seq, e := range helperReg {
		if now.After(e.rec.Deadline.Add(reaperGrace)) {
			due = append(due, e)
			delete(helperReg, seq)
		}
	}
	helperRegMu.Unlock()
	for _, e := range due {
		if e.cancel != nil {
			e.cancel()
		}
		if e.release != nil {
			e.release()
		}
		log.Warn().Str("component", "memguard").Str("label", e.rec.Label).Str("instance", e.rec.Instance).
			Str("unit", e.rec.Unit).Time("deadline", e.rec.Deadline).
			Msg("helper process reaped past its deadline")
	}
	return len(due)
}

// reaperEvery is the reaper's period.
const reaperEvery = 30 * time.Second

var reaperOnce sync.Once

// StartHelperReaper runs ReapHelpers every reaperEvery until ctx ends
// (once per process).
func StartHelperReaper(ctx context.Context) {
	reaperOnce.Do(func() {
		go func() {
			t := time.NewTicker(reaperEvery)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					ReapHelpers()
				}
			}
		}()
	})
}

// helperUnitRe parses memscope.ScopeUnitName: <label>-agent-<pid>-<seq>.scope.
var helperUnitRe = regexp.MustCompile(`^(.+)-agent-(\d+)-(\d+)\.scope$`)

// helperSuffixes are the "what" parts of helper labels (HelperLabel and
// the labels the helpers pass): a scope named after one is a helper, not
// an agent (agents are "omp", "omp-rpc", "opencode-serve", "claude", …).
var helperSuffixes = []string{"-models", "-version", "-export", "-import", "-sync", "-catalog",
	"-broker-token", "-usage", "-auth-broker", "-auth", "-cli"}

func isHelperLabel(label string) bool {
	for _, s := range helperSuffixes {
		if strings.HasSuffix(label, s) {
			return true
		}
	}
	return false
}

// Seams for tests: list / stop the user's systemd scopes.
var (
	listUserScopes = func(ctx context.Context) ([]string, error) {
		out, err := safeexec.CommandContext(ctx, "systemctl", "--user", "list-units", "--type=scope", "--all", "--plain", "--no-legend", "--no-pager").Output()
		if err != nil {
			return nil, err
		}
		var units []string
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			if f := strings.Fields(sc.Text()); len(f) > 0 {
				units = append(units, f[0])
			}
		}
		return units, nil
	}
	stopUserScope = func(ctx context.Context, unit string) error {
		return safeexec.CommandContext(ctx, "systemctl", "--user", "stop", unit).Run()
	}
	// ownerPidAlive reports whether pid still runs. A reload hands the port to
	// the new process while the old one drains its turns — its helpers are
	// still in use and are not "left behind".
	ownerPidAlive = func(pid int) bool {
		p, err := os.FindProcess(pid)
		return err == nil && p.Signal(syscall.Signal(0)) == nil
	}
)

// CleanupStaleHelperScopes stops helper scopes a PREVIOUS wick process
// left behind (a crash or a hard restart while a model listing ran): the
// unit names a helper label and a pid that is neither this process nor
// still running (a draining predecessor keeps its helpers). Agent
// scopes are left alone (crash recovery owns those). systemd backend only;
// returns how many were stopped.
func CleanupStaleHelperScopes(ctx context.Context) int {
	if memscopeBackend() != memscope.BackendSystemd {
		return 0
	}
	units, err := listUserScopes(ctx)
	if err != nil {
		return 0
	}
	self := os.Getpid()
	n := 0
	for _, u := range units {
		m := helperUnitRe.FindStringSubmatch(u)
		if m == nil || !isHelperLabel(m[1]) {
			continue
		}
		if pid, _ := strconv.Atoi(m[2]); pid == self || ownerPidAlive(pid) {
			continue
		}
		if err := stopUserScope(ctx, u); err != nil {
			log.Warn().Err(err).Str("unit", u).Msg("agents.memguard: stale helper scope not stopped")
			continue
		}
		n++
		log.Warn().Str("component", "memguard").Str("unit", u).Str("label", m[1]).
			Msg("stale helper scope from a previous wick process stopped")
	}
	return n
}
