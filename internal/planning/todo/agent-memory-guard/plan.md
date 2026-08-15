# Agent Memory Guard — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop wick from being OOM-killed when an agent (claude/codex) or a tool subprocess balloons — put each agent in its own systemd scope inside a sibling `agents.slice`, so the kernel kills only the offender, and wick survives to report why.

**Architecture:** The kernel enforces, wick observes. Each spawn is wrapped in `systemd-run --user --scope --slice=agents.slice` with `MemoryMax` + `MemoryHigh=infinity` + `MemorySwapMax=0`. Wick installs the slice unit itself (user-scope, no sudo). On exit, wick reads the scope's `memory.events` to distinguish an OOM kill from an ordinary SIGKILL, and reports it as a new `ExitOOM` reason. Ships default-off; a `/proc`-based report command and RAM-based spawn admission work with no systemd at all (Termux/Android).

**Tech Stack:** Go 1.25, cgroup v2 via systemd transient scopes, zerolog, `pkg/safeexec`, existing `internal/agents/{provider,pool,config}` packages.

**Spec:** [design.md](design.md)

## Global Constraints

- All user-facing UI text and every `desc=` in `wick:"..."` tags in **English**.
- No "qiscus" in examples/placeholders — generic names (abc.com, example.com).
- zerolog: `l := log.With().Str("component", "x").Logger()`, then `l.Debug()...`. Never `log.Debug()` directly.
- Never edit `_templ.go` — edit `.templ` and regenerate.
- Linux-only mechanisms ship as `_linux.go` + `_other.go` no-op pairs. **Windows must compile and pass tests** — it is the development platform.
- Default **off**: an install that does not opt in behaves byte-identically to today — no slice unit written, no `oom_score_adj`, no argv wrapping, no goroutine.
- `MemoryHigh=infinity` and `MemorySwapMax=0` on every scope. Non-negotiable: `MemoryHigh` throttles instead of killing and caused a 116-minute production stall.
- Do not commit. The user commits.
- Run `go build ./...` after every task; run `go test ./<touched pkg>/...` before ticking a step.

---

### Task 1: `oomscore` — bias the OOM killer toward agents

**Files:**
- Create: `internal/agents/provider/oomscore/oomscore.go`
- Create: `internal/agents/provider/oomscore/oomscore_linux.go`
- Create: `internal/agents/provider/oomscore/oomscore_other.go`
- Test: `internal/agents/provider/oomscore/oomscore_test.go`

**Interfaces:**
- Produces: `oomscore.Adjust(pid int, score int) error`, `oomscore.AdjustSelf(score int) error`, `oomscore.Available() bool`. Task 6 calls `Adjust` on the spawned child.
- Consumes: nothing.

Constants: `oomscore.AgentScore = 800` (agent, prefer as victim), `oomscore.DaemonScore = -500` (wick, avoid).

The Linux implementation writes to `/proc/<pid>/oom_score_adj`. A root override is injected for tests via an unexported package var so the test never touches real `/proc`.

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/oomscore/oomscore_test.go`:

```go
package oomscore

import (
	"os"
	"path/filepath"
	"testing"
)

// Adjust writes the score where the kernel reads it. The test points the
// package at a temp root so it never touches a real process.
func TestAdjust_WritesScore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "4412"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := setProcRoot(root)
	defer restore()

	if err := Adjust(4412, AgentScore); err != nil {
		t.Fatalf("Adjust: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "4412", "oom_score_adj"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "800" {
		t.Fatalf("score = %q, want %q", got, "800")
	}
}

// A process that already exited must not turn into a spawn failure — the
// guard is advisory, and the agent is already running by the time we set it.
func TestAdjust_MissingProcessIsError(t *testing.T) {
	root := t.TempDir()
	restore := setProcRoot(root)
	defer restore()

	if err := Adjust(9999, AgentScore); err == nil {
		t.Fatal("Adjust on a missing pid returned nil, want an error the caller can log and ignore")
	}
}

// Out-of-range scores are a programming error, not something to pass to
// the kernel: the valid range is -1000..1000.
func TestAdjust_RejectsOutOfRange(t *testing.T) {
	root := t.TempDir()
	restore := setProcRoot(root)
	defer restore()

	for _, score := range []int{-1001, 1001} {
		if err := Adjust(1, score); err == nil {
			t.Fatalf("score %d accepted, want rejection", score)
		}
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/oomscore/...`
Expected: FAIL — package does not exist (`no Go files`).

- [ ] **Step 3: Write the cross-platform surface**

`internal/agents/provider/oomscore/oomscore.go`:

```go
// Package oomscore biases the kernel OOM killer's victim selection.
//
// The kernel picks a victim by badness score, and wick — holding session
// state and buffers — is a fat target sitting next to the agents it
// spawned. Writing a high oom_score_adj on each agent and a low one on
// wick itself tells the kernel which of them the operator would rather
// lose. It kills nothing on its own; it only orders the queue.
//
// This is the one layer of the memory guard that needs neither systemd
// nor cgroup delegation — just a file write — so it is also the only one
// that works on Termux/Android, where lmkd reads the very same knob.
package oomscore

import "fmt"

// Score bounds accepted by the kernel for oom_score_adj.
const (
	minScore = -1000
	maxScore = 1000
)

// AgentScore biases an agent subprocess toward being chosen first.
// DaemonScore biases wick itself away from selection.
const (
	AgentScore  = 800
	DaemonScore = -500
)

// validate rejects scores the kernel would refuse, so a caller bug
// surfaces here rather than as an opaque write error.
func validate(score int) error {
	if score < minScore || score > maxScore {
		return fmt.Errorf("oom score %d out of range [%d,%d]", score, minScore, maxScore)
	}
	return nil
}

// AdjustSelf biases the calling process (wick) away from OOM selection.
func AdjustSelf(score int) error { return Adjust(selfPid(), score) }
```

- [ ] **Step 4: Write the Linux implementation**

`internal/agents/provider/oomscore/oomscore_linux.go`:

```go
//go:build linux || android

package oomscore

import (
	"os"
	"path/filepath"
	"strconv"
)

// procRoot is /proc in production and a temp dir under test.
var procRoot = "/proc"

// setProcRoot points the package at a different root and returns a
// restore func. Test-only, but defined here so the production path and
// the test path resolve paths through exactly the same code.
func setProcRoot(root string) func() {
	prev := procRoot
	procRoot = root
	return func() { procRoot = prev }
}

func selfPid() int { return os.Getpid() }

// Available reports whether oom_score_adj is writable for this process.
func Available() bool {
	_, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(selfPid()), "oom_score_adj"))
	return err == nil
}

// Adjust writes score to /proc/<pid>/oom_score_adj.
//
// Callers treat a failure as advisory: by the time this runs the agent is
// already spawned, and refusing to run it unguarded would trade a memory
// risk for a hard outage.
func Adjust(pid int, score int) error {
	if err := validate(score); err != nil {
		return err
	}
	p := filepath.Join(procRoot, strconv.Itoa(pid), "oom_score_adj")
	return os.WriteFile(p, []byte(strconv.Itoa(score)), 0o644)
}
```

- [ ] **Step 5: Write the non-Linux no-op**

`internal/agents/provider/oomscore/oomscore_other.go`:

```go
//go:build !linux && !android

package oomscore

import (
	"errors"
	"os"
)

// ErrUnsupported is returned on platforms with no oom_score_adj. Callers
// log it at debug and carry on — this is the documented degraded path,
// not a failure.
var ErrUnsupported = errors.New("oom_score_adj not supported on this platform")

var procRoot = ""

func setProcRoot(root string) func() {
	prev := procRoot
	procRoot = root
	return func() { procRoot = prev }
}

func selfPid() int { return os.Getpid() }

// Available always reports false off Linux.
func Available() bool { return false }

// Adjust validates its argument, then reports the platform has no such knob.
func Adjust(pid int, score int) error {
	if err := validate(score); err != nil {
		return err
	}
	return ErrUnsupported
}
```

- [ ] **Step 6: Make the test cross-platform**

The three tests above assert Linux behaviour. On Windows `Adjust` returns `ErrUnsupported`, so add a build-tagged split: rename the file to `oomscore_linux_test.go` with `//go:build linux || android`, and add `oomscore_other_test.go`:

```go
//go:build !linux && !android

package oomscore

import (
	"errors"
	"testing"
)

// Off Linux the package must degrade cleanly rather than pretend to work.
func TestAdjust_UnsupportedPlatform(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true on a platform with no oom_score_adj")
	}
	if err := Adjust(1, AgentScore); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Adjust err = %v, want ErrUnsupported", err)
	}
}

// Validation is shared code and must run everywhere, so a bad score is
// caught on the development platform too.
func TestAdjust_RejectsOutOfRangeEverywhere(t *testing.T) {
	if err := Adjust(1, 1001); errors.Is(err, ErrUnsupported) {
		t.Fatal("out-of-range score reported as unsupported; want a validation error")
	}
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/agents/provider/oomscore/...`
Expected: PASS (on Windows, the `_other` tests run; on Linux, the `_linux` ones).

- [ ] **Step 8: Verify the whole tree still builds**

Run: `go build ./...`
Expected: no output.

---

### Task 2: `memscope` — render the slice, wrap the argv, probe availability

**Files:**
- Create: `internal/agents/provider/memscope/memscope.go` (types, argv + unit rendering — pure, cross-platform)
- Create: `internal/agents/provider/memscope/install_linux.go`
- Create: `internal/agents/provider/memscope/install_other.go`
- Test: `internal/agents/provider/memscope/memscope_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `memscope.Limits{PerScopeMB, AggregateMB int}`
  - `memscope.Opts{Unit, Slice string, LimitMB int}`
  - `memscope.WrapArgv(bin string, args []string, o Opts) (string, []string)` — pure; returns the binary and argv to exec.
  - `memscope.RenderSlice(aggregateMB int) string` — pure; the unit file body.
  - `memscope.EnsureSlice(aggregateMB int) error` — writes `~/.config/systemd/user/agents.slice` + `daemon-reload`, idempotent.
  - `memscope.Available() bool` — cached probe.
  - `memscope.SliceName = "agents.slice"`, `memscope.ScopeUnitName(provider string, seq int) string`.

Task 6 calls `WrapArgv`; task 3 reads scopes created with these names.

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/memscope/memscope_test.go`:

```go
package memscope

import (
	"strings"
	"testing"
)

// The two properties that separate a clean kill from an outage. A
// MemoryHigh in a scope throttles instead of killing and produced a
// 116-minute production stall; swap turns a fast death into thrashing.
// Assert them explicitly rather than trusting review.
func TestWrapArgv_PinsHighAndSwap(t *testing.T) {
	_, argv := WrapArgv("/usr/bin/claude", []string{"--foo"}, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 1536,
	})
	joined := strings.Join(argv, " ")

	for _, want := range []string{
		"-p", "MemoryHigh=infinity", "MemorySwapMax=0", "MemoryMax=1536M",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q missing %q", joined, want)
		}
	}
}

// The wrapper must hand off to the real binary with its arguments intact
// and clearly separated by --, or the agent gets the wrong argv.
func TestWrapArgv_PassesThroughBinaryAndArgs(t *testing.T) {
	bin, argv := WrapArgv("/usr/bin/claude", []string{"--output-format", "stream-json"}, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 1024,
	})
	if bin != "systemd-run" {
		t.Fatalf("bin = %q, want systemd-run", bin)
	}
	sep := -1
	for i, a := range argv {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		t.Fatal("argv has no -- separator before the real command")
	}
	rest := strings.Join(argv[sep+1:], " ")
	if rest != "/usr/bin/claude --output-format stream-json" {
		t.Fatalf("command after -- = %q", rest)
	}
}

// A zero limit means "no per-scope ceiling" (measure mode): the scope is
// still created so memory.peak is readable, but MemoryMax is not set.
func TestWrapArgv_ZeroLimitOmitsMemoryMax(t *testing.T) {
	_, argv := WrapArgv("/usr/bin/claude", nil, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 0,
	})
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "MemoryMax=") {
		t.Fatalf("argv %q sets MemoryMax despite a zero limit", joined)
	}
	if !strings.Contains(joined, "MemoryHigh=infinity") {
		t.Fatalf("argv %q dropped MemoryHigh even with no ceiling", joined)
	}
}

// The slice unit carries the aggregate ceiling and the same two pins.
func TestRenderSlice(t *testing.T) {
	got := RenderSlice(2048)
	for _, want := range []string{
		"[Slice]", "MemoryMax=2048M", "MemoryHigh=infinity", "MemorySwapMax=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("slice unit missing %q:\n%s", want, got)
		}
	}
}

// Rendering must be stable so the idempotent installer does not rewrite
// an unchanged file (and daemon-reload on every spawn stays free).
func TestRenderSlice_Stable(t *testing.T) {
	if RenderSlice(2048) != RenderSlice(2048) {
		t.Fatal("RenderSlice is not deterministic")
	}
}

// Aggregate 0 = unlimited: the slice exists for grouping and measurement
// but imposes no total ceiling.
func TestRenderSlice_ZeroIsUnlimited(t *testing.T) {
	got := RenderSlice(0)
	if strings.Contains(got, "MemoryMax=") {
		t.Fatalf("aggregate 0 still set MemoryMax:\n%s", got)
	}
}

// Scope names must be unique per spawn, or systemd refuses the second one
// while the first is alive.
func TestScopeUnitName_Unique(t *testing.T) {
	if ScopeUnitName("claude", 1) == ScopeUnitName("claude", 2) {
		t.Fatal("scope unit names collide across spawns")
	}
	if !strings.HasPrefix(ScopeUnitName("codex", 3), "codex-agent-") {
		t.Fatalf("name %q does not identify the provider", ScopeUnitName("codex", 3))
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/memscope/...`
Expected: FAIL — no Go files.

- [ ] **Step 3: Implement the pure core**

`internal/agents/provider/memscope/memscope.go`:

```go
// Package memscope places each agent spawn in its own systemd transient
// scope inside a sibling slice, so the kernel's memory ceiling applies to
// that agent's whole process tree — grandchildren included — and a kill
// reaches only that agent.
//
// The slice is a SIBLING of wick's own service, never a child of it. That
// placement is the point: agent memory never counts toward wick's cgroup,
// so wick is never the fat process the OOM killer picks, and an aggregate
// ceiling on the slice can never put wick inside the blast radius.
//
// Everything here is user-scope — no root, no sudo, no change to wick's
// own unit.
package memscope

import (
	"fmt"
	"strconv"
)

// SliceName is the systemd slice every agent scope is placed in.
const SliceName = "agents.slice"

// Limits carries the two ceilings: one scope's own, and the aggregate
// across the slice. 0 means unlimited at that level.
type Limits struct {
	PerScopeMB   int
	AggregateMB  int
}

// Opts describes one scope to create.
type Opts struct {
	Unit    string // transient unit name, unique while alive
	Slice   string // defaults to SliceName when empty
	LimitMB int    // 0 = no MemoryMax (measure mode)
}

// ScopeUnitName builds a per-spawn unit name that identifies the provider
// and stays unique for as long as the scope lives.
func ScopeUnitName(provider string, seq int) string {
	return provider + "-agent-" + strconv.Itoa(seq)
}

// WrapArgv returns the binary and argv that run cmd inside a new scope.
//
// MemoryHigh=infinity and MemorySwapMax=0 are always set, at every limit
// including none. MemoryHigh throttles allocation instead of killing —
// a process past it stalls indefinitely while holding its slot, which is
// how one production incident became a 116-minute outage rather than a
// clean kill. Swap does the same more slowly. Neither is a tuning knob.
func WrapArgv(bin string, args []string, o Opts) (string, []string) {
	slice := o.Slice
	if slice == "" {
		slice = SliceName
	}
	argv := []string{
		"--user", "--scope", "--quiet", "--collect",
		"--slice=" + slice,
		"--unit=" + o.Unit,
		"-p", "MemoryHigh=infinity",
		"-p", "MemorySwapMax=0",
	}
	if o.LimitMB > 0 {
		argv = append(argv, "-p", "MemoryMax="+strconv.Itoa(o.LimitMB)+"M")
	}
	argv = append(argv, "--", bin)
	argv = append(argv, args...)
	return "systemd-run", argv
}

// RenderSlice returns the slice unit body carrying the aggregate ceiling.
// aggregateMB 0 renders no MemoryMax: the slice still groups agents for
// measurement, but imposes no total ceiling.
func RenderSlice(aggregateMB int) string {
	max := ""
	if aggregateMB > 0 {
		max = fmt.Sprintf("MemoryMax=%dM\n", aggregateMB)
	}
	return "[Unit]\n" +
		"Description=Agent sessions (claude, codex, ...), isolated from the wick daemon\n" +
		"Before=slices.target\n" +
		"\n" +
		"[Slice]\n" +
		max +
		// See WrapArgv: throttling is not an acceptable substitute for killing.
		"MemoryHigh=infinity\n" +
		"MemorySwapMax=0\n"
}
```

- [ ] **Step 4: Run the pure tests**

Run: `go test ./internal/agents/provider/memscope/...`
Expected: PASS.

- [ ] **Step 5: Write the installer test**

Append to `memscope_test.go`:

```go
// EnsureSlice writes the unit only when the content would change, so
// calling it on every spawn costs nothing.
func TestEnsureSliceAt_IdempotentWrite(t *testing.T) {
	dir := t.TempDir()

	changed, err := ensureSliceAt(dir, 2048)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !changed {
		t.Fatal("first write reported no change")
	}

	changed, err = ensureSliceAt(dir, 2048)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Fatal("unchanged content rewrote the unit file")
	}

	changed, err = ensureSliceAt(dir, 4096)
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if !changed {
		t.Fatal("a changed aggregate limit did not rewrite the unit")
	}
}
```

- [ ] **Step 6: Implement the installer**

`internal/agents/provider/memscope/install_linux.go`:

```go
//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/pkg/safeexec"
)

// ensureSliceAt writes the slice unit into dir when its content differs
// from what is already there. Reports whether it wrote.
//
// Split from EnsureSlice so the write logic is testable without a systemd
// user session or a real home directory.
func ensureSliceAt(dir string, aggregateMB int) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(dir, SliceName)
	want := RenderSlice(aggregateMB)

	if cur, err := os.ReadFile(target); err == nil && string(cur) == want {
		return false, nil
	}
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureSlice makes the agents slice exist with the configured aggregate
// ceiling, reloading systemd only when the unit actually changed.
//
// daemon-reload does not restart running units, so this never disturbs a
// session in flight. Membership is fixed at spawn: sessions started before
// a limit change keep their old placement until they end.
func EnsureSlice(aggregateMB int) error {
	l := log.With().Str("component", "memscope").Logger()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")

	changed, err := ensureSliceAt(dir, aggregateMB)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	l.Info().Int("aggregate_mb", aggregateMB).Msg("agents.slice updated")
	if err := safeexec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		l.Warn().Err(err).Msg("daemon-reload failed; slice limits apply on next reload")
	}
	return nil
}

var (
	probeOnce sync.Once
	probeOK   bool
)

// Available reports whether this process can create a transient scope at
// all — systemd-run on PATH plus a reachable user bus. Probed once by
// actually creating a throwaway scope, because that is the only question
// that matters and the only answer that cannot be wrong.
func Available() bool {
	probeOnce.Do(func() {
		err := safeexec.Command("systemd-run",
			"--user", "--scope", "--quiet", "--collect",
			"-p", "MemoryMax=64M", "--", "/bin/true").Run()
		probeOK = err == nil
		if !probeOK {
			log.With().Str("component", "memscope").Logger().
				Info().Err(err).Msg("scope isolation unavailable; agents run unguarded")
		}
	})
	return probeOK
}
```

`internal/agents/provider/memscope/install_other.go`:

```go
//go:build !linux && !android

package memscope

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrUnsupported is the documented degraded path off Linux, not a failure.
var ErrUnsupported = errors.New("systemd scopes not supported on this platform")

// ensureSliceAt exists off Linux so the write logic stays under test on
// the development platform. It writes a file and nothing else.
func ensureSliceAt(dir string, aggregateMB int) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(dir, SliceName)
	want := RenderSlice(aggregateMB)
	if cur, err := os.ReadFile(target); err == nil && string(cur) == want {
		return false, nil
	}
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureSlice is a no-op off Linux.
func EnsureSlice(aggregateMB int) error { return ErrUnsupported }

// Available always reports false off Linux.
func Available() bool { return false }
```

- [ ] **Step 7: Run the tests and build**

Run: `go test ./internal/agents/provider/memscope/... && go build ./...`
Expected: PASS, no build output.

---

### Task 3: `memscope` readback — `memory.peak` and `memory.events`

**Files:**
- Modify: `internal/agents/provider/memscope/memscope.go` (add `Stats` type)
- Create: `internal/agents/provider/memscope/read_linux.go`
- Create: `internal/agents/provider/memscope/read_other.go`
- Test: `internal/agents/provider/memscope/read_test.go`

**Interfaces:**
- Consumes: `memscope.SliceName`, `ScopeUnitName` (task 2).
- Produces: `memscope.Stats{PeakBytes uint64, OOMKills int, Known bool}`, `memscope.ReadStats(unit string) Stats`, `memscope.ReadStatsAt(root, unit string) Stats`.

Task 4 turns `Stats.OOMKills > 0` into `ExitOOM`; task 12 renders peaks.

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/memscope/read_test.go`:

```go
package memscope

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScope(t *testing.T, root, unit, events, peak string) {
	t.Helper()
	dir := filepath.Join(root, SliceName, unit+".scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if events != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte(events), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if peak != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.peak"), []byte(peak), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The whole point of the readback: an OOM kill is indistinguishable from
// any other SIGKILL by exit code alone, so oom_kill is the only evidence.
func TestReadStatsAt_DetectsOOMKill(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-1",
		"low 0\nhigh 0\nmax 12\noom 3\noom_kill 1\n", "1610612736\n")

	got := ReadStatsAt(root, "claude-agent-1")
	if !got.Known {
		t.Fatal("stats reported unknown despite a readable scope")
	}
	if got.OOMKills != 1 {
		t.Fatalf("OOMKills = %d, want 1", got.OOMKills)
	}
	if got.PeakBytes != 1610612736 {
		t.Fatalf("PeakBytes = %d, want 1610612736", got.PeakBytes)
	}
}

// A scope that hit its ceiling but was never killed is not an OOM.
func TestReadStatsAt_MaxWithoutKillIsNotOOM(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-2", "max 5\noom_kill 0\n", "500\n")

	got := ReadStatsAt(root, "claude-agent-2")
	if got.OOMKills != 0 {
		t.Fatalf("OOMKills = %d, want 0", got.OOMKills)
	}
}

// --collect reaps a scope as soon as its last process exits, so the read
// races the reap. A missing scope must read as "unknown", never as a
// confident "not OOM" and never as a false OOM.
func TestReadStatsAt_MissingScopeIsUnknown(t *testing.T) {
	got := ReadStatsAt(t.TempDir(), "claude-agent-gone")
	if got.Known {
		t.Fatal("a missing scope reported Known=true")
	}
	if got.OOMKills != 0 {
		t.Fatalf("a missing scope reported %d kills", got.OOMKills)
	}
}

// Kernel files gain fields across versions; an unparseable line must not
// panic or invent a kill.
func TestReadStatsAt_MalformedIsSafe(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-3", "garbage\noom_kill\noom_kill xyz\n", "not-a-number\n")

	got := ReadStatsAt(root, "claude-agent-3")
	if got.OOMKills != 0 {
		t.Fatalf("malformed events produced %d kills", got.OOMKills)
	}
	if got.PeakBytes != 0 {
		t.Fatalf("malformed peak produced %d bytes", got.PeakBytes)
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/memscope/ -run TestReadStatsAt`
Expected: FAIL — `ReadStatsAt` undefined.

- [ ] **Step 3: Add the Stats type**

Append to `internal/agents/provider/memscope/memscope.go`:

```go
// Stats is what a scope's cgroup files report about it.
//
// Known distinguishes "read it, saw no kill" from "could not read it".
// The difference matters: --collect reaps a scope the moment its last
// process exits, so a reader arriving late finds nothing, and reporting
// that as "not an OOM" would silently mislabel the exact failure this
// package exists to explain.
type Stats struct {
	PeakBytes uint64
	OOMKills  int
	Known     bool
}
```

- [ ] **Step 4: Implement the reader**

`internal/agents/provider/memscope/read_linux.go`:

```go
//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupRoot is where the user slice lives. systemd places user-scope
// units under the user manager's own cgroup, so the slice path is
// resolved relative to this root.
var cgroupRoot = "/sys/fs/cgroup"

// ReadStats reports what the kernel recorded for a scope.
func ReadStats(unit string) Stats { return ReadStatsAt(scopeSearchRoot(), unit) }

// ReadStatsAt is ReadStats against an explicit root, for tests.
func ReadStatsAt(root, unit string) Stats {
	dir := filepath.Join(root, SliceName, unit+".scope")
	ev, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		// Reaped, or never existed. Either way: no evidence, no verdict.
		return Stats{}
	}
	st := Stats{Known: true, OOMKills: parseEventCount(string(ev), "oom_kill")}
	if peak, err := os.ReadFile(filepath.Join(dir, "memory.peak")); err == nil {
		st.PeakBytes = parseUint(string(peak))
	}
	return st
}

// parseEventCount pulls one counter out of a flat "key value" file,
// tolerating unknown keys, short lines, and non-numeric values — kernel
// files gain fields between versions and must never panic a reader.
func parseEventCount(body, key string) int {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != key {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

func parseUint(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// scopeSearchRoot resolves the directory the agents slice sits in by
// reading this process's own cgroup and walking up to the user manager,
// since the exact path embeds the uid.
func scopeSearchRoot() string {
	body, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return cgroupRoot
	}
	// cgroup v2: a single "0::<path>" line.
	for _, line := range strings.Split(string(body), "\n") {
		p, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		// Walk up to the user@<uid>.service that owns the user slices.
		for dir := p; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			if strings.HasSuffix(filepath.Base(dir), ".service") &&
				strings.HasPrefix(filepath.Base(dir), "user@") {
				return filepath.Join(cgroupRoot, dir)
			}
		}
	}
	return cgroupRoot
}
```

`internal/agents/provider/memscope/read_other.go`:

```go
//go:build !linux && !android

package memscope

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadStats reports nothing off Linux — there are no cgroup files.
func ReadStats(unit string) Stats { return Stats{} }

// ReadStatsAt keeps the parsing logic under test on the development
// platform by reading the same layout from an arbitrary root.
func ReadStatsAt(root, unit string) Stats {
	dir := filepath.Join(root, SliceName, unit+".scope")
	ev, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		return Stats{}
	}
	st := Stats{Known: true, OOMKills: parseEventCount(string(ev), "oom_kill")}
	if peak, err := os.ReadFile(filepath.Join(dir, "memory.peak")); err == nil {
		st.PeakBytes = parseUint(string(peak))
	}
	return st
}

func parseEventCount(body, key string) int {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != key {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

func parseUint(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
```

- [ ] **Step 5: Run the tests and build**

Run: `go test ./internal/agents/provider/memscope/... && go build ./...`
Expected: PASS.

---

### Task 4: `ExitOOM` — a reason an operator can act on

**Files:**
- Modify: `internal/agents/provider/agent.go` (enum at :111, `exitReasonName` at :1094, `exitReasonDetail` at :1114)
- Test: `internal/agents/provider/exit_oom_test.go`

**Interfaces:**
- Consumes: `memscope.Stats` (task 3).
- Produces: `provider.ExitOOM` (new `ExitReason`), `provider.OOMDetail(peakBytes uint64, limitMB int) string`.

Task 6 sets `ExitDetail{Reason: ExitOOM, ReasonDetail: OOMDetail(...)}` on the exit path.

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/exit_oom_test.go`:

```go
package provider

import (
	"strings"
	"testing"
)

// ExitOOM must be a distinct reason, not folded into ExitError: the pool
// and the UI both branch on it, and "exited abnormally" is exactly the
// unhelpful message this reason exists to replace.
func TestExitOOM_IsDistinct(t *testing.T) {
	if ExitOOM == ExitError || ExitOOM == ExitClean {
		t.Fatal("ExitOOM collides with an existing reason")
	}
	if got := exitReasonName(ExitOOM); got != "oom" {
		t.Fatalf("exitReasonName(ExitOOM) = %q, want %q", got, "oom")
	}
	if got := exitReasonDetail(ExitOOM); got == "" || got == "unknown" {
		t.Fatalf("exitReasonDetail(ExitOOM) = %q, want a real sentence", got)
	}
}

// Adding a reason must not renumber the existing ones — they are
// persisted in spawn logs, so a shifted iota silently rewrites history.
func TestExitReasons_StableOrder(t *testing.T) {
	for i, want := range []struct {
		r    ExitReason
		name string
	}{
		{ExitClean, "clean"},
		{ExitIdle, "idle_ttl"},
		{ExitStopped, "stopped"},
		{ExitError, "error"},
		{ExitRespawn, "respawn"},
	} {
		if int(want.r) != i {
			t.Fatalf("%s moved to %d, want %d — old spawn logs now misread", want.name, want.r, i)
		}
	}
}

// The message has to name numbers. "Agent stopped" leaves an operator
// with nothing to change; a peak and a ceiling point straight at the knob.
func TestOOMDetail_NamesNumbers(t *testing.T) {
	got := OOMDetail(1610612736, 1024) // 1.5 GiB peak against a 1024 MB limit
	if !strings.Contains(got, "1.5 GB") {
		t.Fatalf("detail %q does not report the peak in human units", got)
	}
	if !strings.Contains(got, "1024 MB") {
		t.Fatalf("detail %q does not report the limit", got)
	}
}

// With no readable peak the sentence must still be true and useful,
// never a fabricated zero.
func TestOOMDetail_UnknownPeak(t *testing.T) {
	got := OOMDetail(0, 1024)
	if strings.Contains(got, "0 B") || strings.Contains(got, "0.0") {
		t.Fatalf("detail %q reports a fake zero peak", got)
	}
	if !strings.Contains(got, "1024 MB") {
		t.Fatalf("detail %q dropped the limit", got)
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/ -run 'TestExitOOM|TestOOMDetail|TestExitReasons'`
Expected: FAIL — `ExitOOM` undefined.

- [ ] **Step 3: Add the reason**

In `internal/agents/provider/agent.go`, append to the const block (after `ExitRespawn`, never between existing entries — the iota values are written into spawn logs):

```go
	// ExitOOM is a kill by the kernel for exceeding a memory ceiling,
	// established by reading the scope's memory.events oom_kill counter.
	// Distinct from ExitError because the remedy is specific — raise the
	// limit or shrink the work — and because an exit code alone cannot
	// tell an OOM kill from any other SIGKILL.
	ExitOOM
```

Add to `exitReasonName`:

```go
	case ExitOOM:
		return "oom"
```

Add to `exitReasonDetail`:

```go
	case ExitOOM:
		return "killed by the kernel for exceeding its memory limit"
```

- [ ] **Step 4: Add `OOMDetail`**

Create `internal/agents/provider/exit_oom.go`:

```go
package provider

import "fmt"

// OOMDetail builds the human sentence for an OOM kill.
//
// It names both numbers on purpose. "Agent stopped" gives an operator
// nothing to act on; a measured peak beside the ceiling it broke points
// straight at the setting to change.
//
// peakBytes 0 means the scope was reaped before it could be read — say
// nothing about the peak rather than report a zero that never happened.
func OOMDetail(peakBytes uint64, limitMB int) string {
	if peakBytes == 0 {
		return fmt.Sprintf(
			"killed by the kernel for exceeding its %d MB memory limit. "+
				"Raise the limit in provider settings, or split the work into smaller steps.",
			limitMB)
	}
	return fmt.Sprintf(
		"used %s, over its %d MB limit. "+
			"Raise the limit in provider settings, or split the work into smaller steps.",
		humanBytes(peakBytes), limitMB)
}

// humanBytes renders a byte count the way an operator reads it.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGT"[exp])
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/agents/provider/ -run 'TestExitOOM|TestOOMDetail|TestExitReasons'`
Expected: PASS.

- [ ] **Step 6: Run the package's full suite for regressions**

Run: `go test ./internal/agents/provider/...`
Expected: PASS — the new iota entry must not disturb existing exit tests.

---

### Task 5: Config surface

**Files:**
- Modify: `internal/agents/config/general.go` (add the `Memory Guard` group after `AutoRescan`)
- Modify: `internal/agents/provider/provider.go` (add `MemoryMaxMB` to `Instance` near `MaxConcurrent` at :90; map it in the two converters at :843 and :906)
- Create: `internal/agents/config/memguard.go` (mode/method constants, defaults derivation, limit resolution)
- Test: `internal/agents/config/memguard_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.MemGuardOff/Measure/Enforce` and `config.MethodAuto/Scope/Wrapper` (string constants)
  - `config.DeriveMemoryDefaults(totalRAMBytes uint64, maxConcurrent int) MemoryDefaults`
  - `config.ResolveAgentLimitMB(instanceMB, globalMB int) int`
  - `provider.Instance.MemoryMaxMB int`

Tasks 6, 8, 9, 12 read these.

- [ ] **Step 1: Write the failing test**

`internal/agents/config/memguard_test.go`:

```go
package config

import "testing"

// Per-instance overrides global, and — unlike MaxConcurrent — it may
// EXCEED it. That asymmetry is the whole point: the instance driving a
// browser gets more without making every other agent fatter. A min() here
// would force the operator to raise the global ceiling instead.
func TestResolveAgentLimitMB(t *testing.T) {
	cases := []struct {
		name             string
		instance, global int
		want             int
	}{
		{"zero instance inherits global", 0, 2048, 2048},
		{"instance lowers", 1024, 2048, 1024},
		{"instance may exceed global", 4096, 2048, 4096},
		{"both zero is unlimited", 0, 0, 0},
		{"negative instance treated as unset", -1, 2048, 2048},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveAgentLimitMB(c.instance, c.global); got != c.want {
				t.Fatalf("ResolveAgentLimitMB(%d, %d) = %d, want %d",
					c.instance, c.global, got, c.want)
			}
		})
	}
}

// A 3 GB box and a 32 GB box must not get the same defaults, and the
// small box must not be handed a budget it does not have.
func TestDeriveMemoryDefaults_ScalesWithRAM(t *testing.T) {
	const gb = uint64(1024 * 1024 * 1024)

	small := DeriveMemoryDefaults(3*gb, 1)
	if small.AgentsTotalMB != 2048 {
		t.Fatalf("3GB aggregate = %d, want 2048 (3072 - 400 - 600, floored to MB)", small.AgentsTotalMB)
	}
	if small.AgentMaxMB != small.AgentsTotalMB {
		t.Fatalf("at MaxConcurrent=1 the per-agent limit should equal the budget, got %d vs %d",
			small.AgentMaxMB, small.AgentsTotalMB)
	}

	big := DeriveMemoryDefaults(32*gb, 4)
	if big.AgentMaxMB <= small.AgentMaxMB {
		t.Fatalf("32GB per-agent (%d) not above 3GB per-agent (%d)", big.AgentMaxMB, small.AgentMaxMB)
	}
	if big.AgentMaxMB*4 > big.AgentsTotalMB {
		t.Fatalf("per-agent %d x 4 exceeds the aggregate %d", big.AgentMaxMB, big.AgentsTotalMB)
	}
}

// A machine too small to host an agent must yield a floor, never a
// negative or zero budget that would read as "unlimited".
func TestDeriveMemoryDefaults_TinyMachineFloors(t *testing.T) {
	got := DeriveMemoryDefaults(512*1024*1024, 1)
	if got.AgentMaxMB <= 0 || got.AgentsTotalMB <= 0 {
		t.Fatalf("tiny machine produced non-positive limits: %+v", got)
	}
	if got.MinFreeMB <= 0 {
		t.Fatalf("tiny machine produced MinFreeMB=%d", got.MinFreeMB)
	}
}

// Tool subprocesses get a smaller ceiling than agents: a grep does not
// need an agent's budget, and a tight limit is what makes the failure
// recoverable rather than fatal.
func TestDeriveMemoryDefaults_ToolLimitIsSmaller(t *testing.T) {
	got := DeriveMemoryDefaults(8*1024*1024*1024, 2)
	if got.ToolMaxMB >= got.AgentMaxMB {
		t.Fatalf("tool limit %d not below agent limit %d", got.ToolMaxMB, got.AgentMaxMB)
	}
	if got.ToolMaxMB > 512 {
		t.Fatalf("tool limit %d exceeds the 512 MB cap", got.ToolMaxMB)
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/config/ -run 'TestResolve|TestDerive'`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/agents/config/memguard.go`:

```go
package config

// memguard.go holds the memory-guard vocabulary and the arithmetic behind
// its defaults. The mechanism lives in internal/agents/provider/memscope;
// this file only decides the numbers.

// Guard modes. off is genuinely nothing — no slice unit, no oom_score_adj,
// no argv wrapping — so an install that never opts in behaves exactly as
// it did before the feature existed.
const (
	MemGuardOff     = "off"
	MemGuardMeasure = "measure"
	MemGuardEnforce = "enforce"
)

// Guard methods: who wraps the spawn.
//
// wrapper means something outside wick already does it (a symlink wrapper
// on the binary), so wick measures but does not wrap. Double-wrapping is
// harmless — the kernel enforces every ceiling in the hierarchy and the
// tightest wins — so this is a preference, not a safety interlock.
const (
	MethodAuto    = "auto"
	MethodScope   = "scope"
	MethodWrapper = "wrapper"
)

// Reserves carved out of RAM before agents get a budget.
const (
	reserveOSMB   = 400
	reserveWickMB = 600
	// floorMB keeps a tiny machine from deriving a zero budget, which
	// would read as "unlimited" and invert the whole point.
	floorMB = 256
	// toolCapMB bounds a tool subprocess. A grep does not need an agent's
	// budget, and a tight ceiling is what makes its failure recoverable.
	toolCapMB = 512
)

// MemoryDefaults are the derived starting values for one machine.
type MemoryDefaults struct {
	AgentsTotalMB int
	AgentMaxMB    int
	ToolMaxMB     int
	MinFreeMB     int
}

// DeriveMemoryDefaults scales the defaults to the machine. A 3 GB box and
// a 32 GB box must not start from the same numbers.
func DeriveMemoryDefaults(totalRAMBytes uint64, maxConcurrent int) MemoryDefaults {
	totalMB := int(totalRAMBytes / (1024 * 1024))

	budget := totalMB - reserveOSMB - reserveWickMB
	if budget < floorMB {
		budget = floorMB
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}

	perAgent := budget / maxConcurrent
	if perAgent < floorMB {
		perAgent = floorMB
	}

	tool := perAgent / 4
	if tool > toolCapMB {
		tool = toolCapMB
	}
	if tool < 64 {
		tool = 64
	}

	minFree := totalMB / 8
	if minFree < floorMB {
		minFree = floorMB
	}

	return MemoryDefaults{
		AgentsTotalMB: budget,
		AgentMaxMB:    perAgent,
		ToolMaxMB:     tool,
		MinFreeMB:     minFree,
	}
}

// ResolveAgentLimitMB picks the ceiling for one spawn.
//
// Deliberately unlike MaxConcurrent, which resolves as min(provider,
// global) because slots are a shared pool. A memory ceiling is per
// process, so a per-instance value may exceed the global default — that
// is precisely how one heavy instance is accommodated without letting
// every other agent grow.
func ResolveAgentLimitMB(instanceMB, globalMB int) int {
	if instanceMB > 0 {
		return instanceMB
	}
	return globalMB
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agents/config/ -run 'TestResolve|TestDerive'`
Expected: PASS.

- [ ] **Step 5: Add the config fields**

In `internal/agents/config/general.go`, after the `AutoRescan` line:

```go
	MemoryGuardMode     string `wick:"dropdown=off|measure|enforce;group=Memory Guard|Keep one runaway agent from taking the whole machine down. Start at 'measure' to learn real numbers, then switch to 'enforce'.;desc=off = no memory management at all (default). measure = put each agent in its own group and record its peak, without limiting anything. enforce = the kernel stops an agent that exceeds its limit, leaving every other agent and the server itself untouched."`
	MemoryGuardMethod   string `wick:"dropdown=auto|scope|wrapper;group=Memory Guard;desc=Who applies the limit. auto = wick applies it when the system supports it (default). scope = wick always applies it. wrapper = something outside wick already does (a wrapper script on the agent binary); wick then only measures and reports. Running both at once is safe — the tighter limit wins."`
	AgentMemoryMaxMB    int    `wick:"number;group=Memory Guard;desc=Memory limit for one agent, in MB, counting everything it starts (browsers, tools, scripts). 0 = no limit. A provider instance can set its own value that overrides this one, higher or lower."`
	AgentsTotalMemoryMB int    `wick:"number;group=Memory Guard;desc=Combined memory limit across all running agents, in MB. Acts as a backstop when several well-behaved agents add up to more than the machine has. 0 = no combined limit."`
	ToolMemoryMaxMB     int    `wick:"number;group=Memory Guard;desc=Memory limit in MB for a command an agent runs itself (grep, curl, scripts). Exceeding it fails that one command and returns an error the agent can react to — the agent keeps running. 0 = no limit."`
	MinFreeMemoryMB     int    `wick:"number;group=Memory Guard;desc=Queue a new agent instead of starting it while free memory is below this many MB. Prevents the start that pushes the machine over the edge. 0 = start regardless."`
	ProtectWickFromOOM  bool   `wick:"bool;group=Memory Guard;desc=When memory runs out system-wide, tell the kernel to stop an agent rather than wick itself. Only applies in 'enforce' mode. Recommended: on."`
```

In the `Defaults()` func (near `MaxConcurrent: 2` at :91), add:

```go
		MemoryGuardMode:    MemGuardOff,
		MemoryGuardMethod:  MethodAuto,
		ProtectWickFromOOM: true,
```

Leave the four numeric limits at zero here: they are filled from
`DeriveMemoryDefaults` at first boot (task 9), because their correct values
depend on the machine and not on this struct.

- [ ] **Step 6: Add the per-instance field**

In `internal/agents/provider/provider.go`, immediately after the `MaxConcurrent` field (:90-91):

```go
	// MemoryMaxMB caps this instance's agents in MB. 0 = follow the global
	// AgentMemoryMaxMB. Unlike MaxConcurrent this MAY exceed the global
	// value — see config.ResolveAgentLimitMB for why.
	MemoryMaxMB int
```

Map it in both converters, beside the existing `MaxConcurrent:` lines at :850 and :912:

```go
				MemoryMaxMB:       raw.MemoryMaxMB,
```
```go
		MemoryMaxMB:       ins.MemoryMaxMB,
```

Add the matching `MemoryMaxMB int` field to the `userconfig` instance struct that `raw` refers to (find it via `grep -n "MaxConcurrent" internal/userconfig/*.go`).

- [ ] **Step 7: Build and run the affected suites**

Run: `go build ./... && go test ./internal/agents/config/... ./internal/agents/provider/...`
Expected: PASS.

---

### Task 6: Wire agent spawn through the guard

**Files:**
- Create: `internal/agents/provider/memguard.go` (the decision layer every spawner calls)
- Modify: `internal/agents/provider/spawner.go` (add guard fields to `SpawnOptions`)
- Modify: `internal/agents/provider/claude/spawn.go` (:185, beside `procgroup.Apply`)
- Modify: `internal/agents/provider/codex/spawn.go` (same treatment)
- Modify: `internal/agents/provider/gemini/spawn.go` (same treatment)
- Test: `internal/agents/provider/memguard_test.go`

**Interfaces:**
- Consumes: `memscope.WrapArgv`/`EnsureSlice`/`Available`/`ReadStats` (tasks 2–3), `oomscore.Adjust` (task 1), `config.ResolveAgentLimitMB` + mode/method constants (task 5), `provider.OOMDetail` (task 4).
- Produces:
  - `provider.MemGuard{Mode, Method string, AgentLimitMB, AggregateMB int}`
  - `(MemGuard) Wrap(bin string, args []string, provider string, seq int) (string, []string, string)` — returns binary, argv, and the scope unit name ("" when not wrapped).
  - `(MemGuard) ClassifyExit(unit string, limitMB int) (ExitReason, string, bool)`
  - `SpawnOptions.MemGuard *MemGuard`

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/memguard_test.go`:

```go
package provider

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

// Mode off must be indistinguishable from the feature not existing: the
// argv reaches exec untouched and no scope name is claimed.
func TestMemGuard_OffDoesNotWrap(t *testing.T) {
	g := MemGuard{Mode: config.MemGuardOff, Method: config.MethodScope, AgentLimitMB: 1024}

	bin, argv, unit := g.Wrap("/usr/bin/claude", []string{"--foo"}, "claude", 1)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary", bin)
	}
	if len(argv) != 1 || argv[0] != "--foo" {
		t.Fatalf("argv = %v, want it untouched", argv)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty when unwrapped", unit)
	}
}

// Method wrapper means something outside wick already wraps. Wick must
// not wrap again by itself here — not because double-wrapping is unsafe
// (it is not), but because this is the operator saying who owns it.
func TestMemGuard_WrapperMethodDefersToExternal(t *testing.T) {
	g := MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodWrapper, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary under method=wrapper", bin)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty under method=wrapper", unit)
	}
}

// Measure mode creates the scope so a peak can be read, but sets no
// ceiling — turning measurement on must never change what dies.
func TestMemGuard_MeasureCreatesScopeWithoutLimit(t *testing.T) {
	if !scopeAvailableForTest() {
		t.Skip("scopes unavailable on this platform")
	}
	g := MemGuard{Mode: config.MemGuardMeasure, Method: config.MethodScope, AgentLimitMB: 1024}

	_, argv, unit := g.Wrap("/usr/bin/claude", nil, "claude", 3)
	if unit == "" {
		t.Fatal("measure mode did not create a scope; peaks would be unreadable")
	}
	if strings.Contains(strings.Join(argv, " "), "MemoryMax=") {
		t.Fatalf("measure mode set a ceiling: %v", argv)
	}
}

// An OOM verdict requires evidence. Without a readable scope the exit
// must fall through to the ordinary classification, never guess.
func TestMemGuard_ClassifyExitWithoutEvidence(t *testing.T) {
	g := MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodScope, AgentLimitMB: 1024}

	if _, _, ok := g.ClassifyExit("", 1024); ok {
		t.Fatal("classified an OOM with no scope to read")
	}
	if _, _, ok := g.ClassifyExit("claude-agent-does-not-exist", 1024); ok {
		t.Fatal("classified an OOM from a scope that does not exist")
	}
}
```

Add a small helper in the same file:

```go
// scopeAvailableForTest keeps the wrap tests meaningful on Linux while
// skipping them where scopes cannot exist.
func scopeAvailableForTest() bool { return memscopeAvailable() }
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/ -run TestMemGuard`
Expected: FAIL — `MemGuard` undefined.

- [ ] **Step 3: Implement the decision layer**

`internal/agents/provider/memguard.go`:

```go
package provider

import (
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/internal/agents/provider/oomscore"
)

// memguard.go is the one place that decides whether a spawn is wrapped,
// so the three CLI spawners stay free of policy and the rules cannot
// drift apart between them.

// MemGuard is the resolved memory policy for one spawn.
type MemGuard struct {
	Mode         string // config.MemGuard{Off,Measure,Enforce}
	Method       string // config.Method{Auto,Scope,Wrapper}
	AgentLimitMB int    // resolved ceiling; 0 = none
	AggregateMB  int    // slice-wide ceiling; 0 = none
	ProtectWick  bool
}

// memscopeAvailable is a seam so tests can reason about the probe.
var memscopeAvailable = memscope.Available

// wraps reports whether wick itself should wrap this spawn.
//
// Under method=wrapper the operator has said an external wrapper owns
// this. Wick honours that and only measures. Double-wrapping would be
// harmless — the kernel applies every ceiling in the hierarchy and the
// tightest wins — so this is about who owns the setting, not safety.
func (g MemGuard) wraps() bool {
	if g.Mode == config.MemGuardOff {
		return false
	}
	switch g.Method {
	case config.MethodWrapper:
		return false
	case config.MethodScope:
		return memscopeAvailable()
	default: // auto
		return memscopeAvailable()
	}
}

// Wrap returns the binary, argv, and scope unit name to use for a spawn.
// An empty unit name means the spawn was not wrapped.
func (g MemGuard) Wrap(bin string, args []string, providerName string, seq int) (string, []string, string) {
	if !g.wraps() {
		return bin, args, ""
	}
	l := log.With().Str("component", "memguard").Logger()

	if err := memscope.EnsureSlice(g.AggregateMB); err != nil {
		// A missing slice means the aggregate ceiling is absent, but the
		// per-scope ceiling still applies. Degrade, do not refuse to spawn.
		l.Warn().Err(err).Msg("could not ensure agents.slice; per-scope limits still apply")
	}

	limit := g.AgentLimitMB
	if g.Mode == config.MemGuardMeasure {
		// Measure records peaks and changes nothing else.
		limit = 0
	}

	unit := memscope.ScopeUnitName(providerName, seq)
	wbin, wargv := memscope.WrapArgv(bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limit,
	})
	l.Debug().Str("unit", unit).Int("limit_mb", limit).Msg("agent spawn wrapped in scope")
	return wbin, wargv, unit
}

// BiasChild pushes the kernel toward killing this agent rather than wick.
// Advisory: the agent is already running, so a failure here is logged and
// never turned into a spawn error.
func (g MemGuard) BiasChild(pid int) {
	if g.Mode != config.MemGuardEnforce || !g.ProtectWick || pid <= 0 {
		return
	}
	if err := oomscore.Adjust(pid, oomscore.AgentScore); err != nil {
		log.With().Str("component", "memguard").Logger().
			Debug().Err(err).Int("pid", pid).Msg("could not bias agent oom score")
	}
}

// ClassifyExit reports whether this exit was an OOM kill, with a sentence
// naming the numbers. The bool is false whenever there is no evidence —
// an exit code alone cannot distinguish an OOM kill from any other
// SIGKILL, so a guess here would mislabel the very failure this exists to
// explain.
func (g MemGuard) ClassifyExit(unit string, limitMB int) (ExitReason, string, bool) {
	if unit == "" {
		return ExitError, "", false
	}
	st := memscope.ReadStats(unit)
	if !st.Known || st.OOMKills == 0 {
		return ExitError, "", false
	}
	return ExitOOM, OOMDetail(st.PeakBytes, limitMB), true
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agents/provider/ -run TestMemGuard`
Expected: PASS.

- [ ] **Step 5: Thread it through `SpawnOptions`**

In `internal/agents/provider/spawner.go`, append to `SpawnOptions`:

```go
	// MemGuard is the resolved memory policy for this spawn. nil = the
	// guard is off or the caller is a test fake; spawners then behave
	// exactly as they did before the guard existed.
	MemGuard *MemGuard

	// SpawnSeq is a monotonic counter used to name the scope unit
	// uniquely — systemd refuses a duplicate name while the first is
	// alive.
	SpawnSeq int
```

- [ ] **Step 6: Wire the claude spawner**

In `internal/agents/provider/claude/spawn.go`, find where `cmd` is built (around :185 where `procgroup.Apply(cmd)` is called). Immediately BEFORE the `exec.Command`/`safeexec.Command` construction, wrap the binary and args:

```go
	scopeUnit := ""
	if opt.MemGuard != nil {
		bin, args, scopeUnit = opt.MemGuard.Wrap(bin, args, "claude", opt.SpawnSeq)
	}
```

Keep `procgroup.Apply(cmd)` exactly where it is — teardown depends on it and task 7 asserts it still works through a scope.

After a successful `cmd.Start()`, bias the child:

```go
	if opt.MemGuard != nil {
		opt.MemGuard.BiasChild(cmd.Process.Pid)
	}
```

Store `scopeUnit` on the returned process struct so the agent's exit path can classify it. Add a `scopeUnit string` field to the process type in this file and a `ScopeUnit() string` accessor; add `ScopeUnit() string` to the `Process` interface in `spawner.go`, returning `""` from the fake spawner in `fake_spawner_test.go` and any other implementer (`grep -rn "func.*Argv() \[\]string" internal/` finds them all).

- [ ] **Step 7: Repeat for codex and gemini**

Apply the identical treatment in `internal/agents/provider/codex/spawn.go` and `internal/agents/provider/gemini/spawn.go`, passing `"codex"` / `"gemini"` as the provider name.

- [ ] **Step 8: Classify the exit**

In `internal/agents/provider/agent.go`, in the reader-exit path that builds `ExitDetail` (around :1071), before the existing classification:

```go
	if a.cfg.MemGuard != nil && a.proc != nil {
		if r, detail, ok := a.cfg.MemGuard.ClassifyExit(a.proc.ScopeUnit(), a.cfg.MemGuard.AgentLimitMB); ok {
			reason, stderrTail = r, detail
		}
	}
```

Add `MemGuard *MemGuard` to the agent's config struct and populate it from the pool factory (`internal/agents/pool/factory.go`) from `config.GeneralConfig` plus `config.ResolveAgentLimitMB(instance.MemoryMaxMB, general.AgentMemoryMaxMB)`.

- [ ] **Step 9: Build and run the full agent suite**

Run: `go build ./... && go test ./internal/agents/...`
Expected: PASS.

---

### Task 7: Teardown regression — `kill(-pgid)` must still reap the tree

**Files:**
- Create: `internal/agents/provider/memscope/teardown_integration_test.go`

**Interfaces:**
- Consumes: `memscope.WrapArgv` (task 2).
- Produces: nothing. This task is a gate, not a feature.

This is the task that decides whether the feature ships. Wick stops a session
with `kill(-pgid)` across the whole process group. Routing the spawn through
`systemd-run` inserts a process between wick and the agent — if that breaks the
process-group relationship, wick keeps its memory guard and loses the ability to
stop a session at all, which is a worse bug than the one being fixed.

- [ ] **Step 1: Write the integration test**

`internal/agents/provider/memscope/teardown_integration_test.go`:

```go
//go:build linux && integration

package memscope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Wick tears a session down with kill(-pgid). systemd-run inserts a
// process between wick and the agent, so this asserts the relationship
// survives it. The production preflight verifies the same property; this
// test exists so a later refactor cannot break it silently.
func TestKillProcessGroupReapsTreeThroughScope(t *testing.T) {
	if !Available() {
		t.Skip("no systemd user session")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "tree.sh")
	pidFile := filepath.Join(dir, "tree.pid")

	body := "#!/bin/bash\n" +
		"sleep 300 &\n" +
		"( sleep 300 & wait ) &\n" +
		"echo $$ > " + pidFile + "\n" +
		"sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	bin, argv := WrapArgv("/bin/bash", []string{script}, Opts{
		Unit: "wick-teardown-test", Slice: SliceName, LimitMB: 200,
	})
	cmd := exec.Command(bin, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()

	var treePid int
	for i := 0; i < 40; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				treePid = p
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if treePid == 0 {
		t.Fatal("test tree never started")
	}

	pgid, err := syscall.Getpgid(treePid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if got := countInGroup(t, pgid); got == 0 {
		t.Fatal("no processes in the group before the kill")
	}

	// Exactly what wick does to stop a session.
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill(-pgid): %v", err)
	}
	time.Sleep(1 * time.Second)

	if left := countInGroup(t, pgid); left != 0 {
		t.Fatalf("%d processes survived kill(-pgid) through a scope — session teardown is broken", left)
	}
}

// countInGroup counts live processes in a process group by walking /proc,
// avoiding a dependency on pgrep being installed.
func countInGroup(t *testing.T, pgid int) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	n := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		got, err := syscall.Getpgid(pid)
		if err == nil && got == pgid {
			n++
		}
	}
	return n
}
```

- [ ] **Step 2: Run it on a Linux box with a user session**

Run: `go test -tags integration ./internal/agents/provider/memscope/ -run TestKillProcessGroup -v`
Expected: PASS. On Windows it does not build (tagged), which is correct.

- [ ] **Step 3: If it fails, stop**

A failure here means scope wrapping breaks session teardown. Do not proceed to
enabling the guard; report the failure and revisit the mechanism. The rest of the
plan is worthless if wick cannot stop a session.

- [ ] **Step 4: Confirm the Windows build is unaffected**

Run: `go build ./... && go vet ./internal/agents/provider/memscope/`
Expected: clean.

---

### Task 8: Guard wick-provider tool subprocesses

**Files:**
- Modify: `internal/agents/provider/wick/tool_shell.go` (:193)
- Modify: `internal/agents/provider/wick/tool_shell_bg.go` (:137)
- Create: `internal/agents/provider/wick/toolguard.go`
- Test: `internal/agents/provider/wick/toolguard_test.go`

**Interfaces:**
- Consumes: `memscope.WrapArgv` (task 2), `memscope.ReadStats` (task 3), `config.MemGuard*` (task 5).
- Produces: `wick.wrapToolCmd(bin string, args []string, limitMB int, seq int) (string, []string, string)`, `wick.toolOOMMessage(limitMB int) string`.

The wick provider runs shell commands itself. A `grep -r` over a large tree or a
`cat` of a huge file is an unguarded direct child of wick — the same failure as an
agent ballooning, with none of the isolation.

Unlike an agent, exceeding the limit must NOT kill the agent: the tool call fails
and returns an error the model can read and react to. That is what makes a tight
tool ceiling tolerable.

- [ ] **Step 1: Write the failing test**

`internal/agents/provider/wick/toolguard_test.go`:

```go
package wick

import (
	"strings"
	"testing"
)

// A zero limit means the operator turned tool guarding off; the command
// must run exactly as before.
func TestWrapToolCmd_ZeroLimitDoesNotWrap(t *testing.T) {
	bin, args, unit := wrapToolCmd("/bin/sh", []string{"-c", "grep -r x ."}, 0, 1)
	if bin != "/bin/sh" || unit != "" {
		t.Fatalf("bin=%q unit=%q, want the command untouched", bin, unit)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want them untouched", args)
	}
}

// The message must tell the model what to do differently, because the
// model is the one that decides whether to retry with a narrower scope.
func TestToolOOMMessage_IsActionable(t *testing.T) {
	got := toolOOMMessage(512)
	if !strings.Contains(got, "512 MB") {
		t.Fatalf("message %q does not name the limit", got)
	}
	low := strings.ToLower(got)
	if !strings.Contains(low, "narrow") && !strings.Contains(low, "smaller") {
		t.Fatalf("message %q does not suggest a way forward", got)
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/agents/provider/wick/ -run 'TestWrapToolCmd|TestToolOOM'`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/agents/provider/wick/toolguard.go`:

```go
package wick

import (
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// toolguard.go bounds the shell commands the wick provider runs itself.
//
// Unlike an agent subprocess, a tool that exceeds its limit must not take
// the agent down: the call fails, the model reads the error, and it can
// retry with a narrower scope. That recoverability is what makes a tight
// ceiling here reasonable — a grep does not need an agent's budget.

var toolSeq atomic.Int64

// wrapToolCmd bounds one tool subprocess. limitMB 0 leaves the command
// exactly as it was.
func wrapToolCmd(bin string, args []string, limitMB int, seq int) (string, []string, string) {
	if limitMB <= 0 || !memscope.Available() {
		return bin, args, ""
	}
	unit := "wick-tool-" + strconv.Itoa(seq)
	wbin, wargv := memscope.WrapArgv(bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limitMB,
	})
	return wbin, wargv, unit
}

// nextToolSeq yields a unique scope suffix; systemd refuses a duplicate
// unit name while the first is still alive.
func nextToolSeq() int { return int(toolSeq.Add(1)) }

// toolOOMMessage is what the model sees. It names the ceiling and points
// at the fix, because the model is the one that decides what to try next.
func toolOOMMessage(limitMB int) string {
	return fmt.Sprintf(
		"command stopped: it exceeded the %d MB memory limit for tool commands. "+
			"Retry with a narrower scope (fewer files, a smaller range, or streamed output).",
		limitMB)
}
```

- [ ] **Step 4: Wire the two call sites**

In `internal/agents/provider/wick/tool_shell.go` at :193, replace:

```go
	cmd := safeexec.Command(bin, "-c", cmdline)
```

with:

```go
	tbin, targs, toolScope := wrapToolCmd(bin, []string{"-c", cmdline}, e.toolMemoryMaxMB, nextToolSeq())
	cmd := safeexec.Command(tbin, targs...)
```

After the command finishes and returns a non-zero status, check for an OOM before
building the error result:

```go
	if toolScope != "" {
		if st := memscope.ReadStats(toolScope); st.Known && st.OOMKills > 0 {
			return toolOOMMessage(e.toolMemoryMaxMB), nil
		}
	}
```

Apply the identical change at `tool_shell_bg.go:137`.

Thread `toolMemoryMaxMB` onto the engine struct from `config.GeneralConfig.ToolMemoryMaxMB`.

- [ ] **Step 5: Run the tests and build**

Run: `go test ./internal/agents/provider/wick/... && go build ./...`
Expected: PASS.

---

### Task 9: Spawn admission on available memory

**Files:**
- Create: `internal/agents/pool/admission.go`
- Create: `internal/pkg/sysmem/sysmem.go` + `sysmem_linux.go` + `sysmem_other.go`
- Test: `internal/agents/pool/admission_test.go`, `internal/pkg/sysmem/sysmem_test.go`

**Interfaces:**
- Consumes: `config.MinFreeMemoryMB` (task 5).
- Produces: `sysmem.Available() (uint64, bool)`, `sysmem.Total() (uint64, bool)`, `(p *Pool) memoryAdmits() bool`.

This layer works with no systemd at all — it reads `/proc/meminfo` — so it is one of
the pieces that protects Termux/Android, where scopes are unavailable.

- [ ] **Step 1: Write the failing tests**

`internal/pkg/sysmem/sysmem_test.go`:

```go
package sysmem

import "testing"

// MemAvailable is the kernel's own estimate of what a new process can
// get. MemFree is not a substitute — it excludes reclaimable cache and
// would refuse spawns on a perfectly healthy machine.
func TestParseMeminfo(t *testing.T) {
	body := "MemTotal:        3082240 kB\n" +
		"MemFree:          123456 kB\n" +
		"MemAvailable:    1258291 kB\n" +
		"Buffers:           12345 kB\n"

	total, avail := parseMeminfo(body)
	if total != 3082240*1024 {
		t.Fatalf("total = %d, want %d", total, uint64(3082240)*1024)
	}
	if avail != 1258291*1024 {
		t.Fatalf("available = %d, want %d", avail, uint64(1258291)*1024)
	}
}

// A kernel without MemAvailable (very old) must report zero rather than
// silently falling back to MemFree, which would be a different number
// wearing the same name.
func TestParseMeminfo_NoAvailableField(t *testing.T) {
	_, avail := parseMeminfo("MemTotal: 100 kB\nMemFree: 50 kB\n")
	if avail != 0 {
		t.Fatalf("available = %d, want 0 when MemAvailable is absent", avail)
	}
}
```

`internal/agents/pool/admission_test.go`:

```go
package pool

import "testing"

// Below the floor, a spawn queues instead of starting. This is the layer
// that prevents the start that pushes the machine over the edge.
func TestMemoryAdmits(t *testing.T) {
	cases := []struct {
		name        string
		minFreeMB   int
		availBytes  uint64
		availKnown  bool
		want        bool
	}{
		{"plenty free admits", 512, 2 * 1024 * 1024 * 1024, true, true},
		{"below floor refuses", 512, 100 * 1024 * 1024, true, false},
		{"exactly at floor admits", 512, 512 * 1024 * 1024, true, true},
		{"zero floor disables the check", 0, 1024, true, true},
		// Unknown availability must not become a silent spawn ban: the
		// guard is advisory, and refusing every spawn would be a worse
		// failure than the one it prevents.
		{"unknown availability admits", 512, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := memoryAdmits(c.minFreeMB, c.availBytes, c.availKnown)
			if got != c.want {
				t.Fatalf("memoryAdmits(%d, %d, %v) = %v, want %v",
					c.minFreeMB, c.availBytes, c.availKnown, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run them, confirm they fail**

Run: `go test ./internal/pkg/sysmem/... ./internal/agents/pool/ -run 'TestParseMeminfo|TestMemoryAdmits'`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `sysmem`**

`internal/pkg/sysmem/sysmem.go`:

```go
// Package sysmem reports machine memory. It reads /proc/meminfo on Linux
// and reports "unknown" elsewhere, so callers must handle absence rather
// than assume a number.
package sysmem

import (
	"strconv"
	"strings"
)

// parseMeminfo extracts MemTotal and MemAvailable as bytes. A missing
// field yields 0, which callers read as unknown.
//
// MemAvailable, not MemFree: MemFree excludes reclaimable page cache, so
// using it would refuse spawns on a machine that is entirely healthy.
func parseMeminfo(body string) (total, available uint64) {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	return total, available
}
```

`internal/pkg/sysmem/sysmem_linux.go`:

```go
//go:build linux || android

package sysmem

import "os"

var meminfoPath = "/proc/meminfo"

func read() (total, available uint64) {
	b, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0, 0
	}
	return parseMeminfo(string(b))
}

// Total reports total RAM in bytes; ok is false when unknown.
func Total() (uint64, bool) {
	t, _ := read()
	return t, t > 0
}

// Available reports the kernel's estimate of allocatable memory in bytes.
func Available() (uint64, bool) {
	_, a := read()
	return a, a > 0
}
```

`internal/pkg/sysmem/sysmem_other.go`:

```go
//go:build !linux && !android

package sysmem

// Total reports unknown off Linux.
func Total() (uint64, bool) { return 0, false }

// Available reports unknown off Linux. Callers must admit the spawn
// rather than block it — an advisory guard that cannot read the machine
// must not become a spawn ban.
func Available() (uint64, bool) { return 0, false }
```

- [ ] **Step 4: Implement admission**

`internal/agents/pool/admission.go`:

```go
package pool

import (
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/pkg/sysmem"
)

// admission.go refuses a spawn while the machine is already short of
// memory, so the queue absorbs the pressure instead of the OOM killer.
//
// It reads /proc/meminfo and needs no cgroup or systemd, which makes it
// one of the two layers that still protect Termux/Android.

// memoryAdmits decides whether there is room to start another agent.
//
// Unknown availability admits: this guard is advisory, and turning "I
// cannot read the machine" into "nothing may ever start" would be a worse
// failure than the one being prevented.
func memoryAdmits(minFreeMB int, availBytes uint64, availKnown bool) bool {
	if minFreeMB <= 0 || !availKnown {
		return true
	}
	return availBytes >= uint64(minFreeMB)*1024*1024
}

// memoryAdmitsNow answers for the live machine.
func (p *Pool) memoryAdmitsNow(minFreeMB int) bool {
	avail, ok := sysmem.Available()
	admits := memoryAdmits(minFreeMB, avail, ok)
	if !admits {
		log.With().Str("component", "pool").Logger().
			Info().
			Uint64("available_bytes", avail).
			Int("min_free_mb", minFreeMB).
			Msg("spawn queued: machine is low on memory")
	}
	return admits
}
```

- [ ] **Step 5: Call it from the spawn gate**

In `internal/agents/pool/pool.go`'s `spawn` (:755), after the existing capacity check
and before the factory call, refuse and queue when memory is short. Follow whatever
the capacity path already does to enqueue rather than inventing a second mechanism —
read the surrounding code and mirror it, so a memory refusal and a slot refusal behave
identically from the caller's point of view.

- [ ] **Step 6: Run the tests and build**

Run: `go test ./internal/pkg/sysmem/... ./internal/agents/pool/... && go build ./...`
Expected: PASS.

---

### Task 10: `wick memory report`

**Files:**
- Create: `cmd/cli/memory.go`
- Create: `internal/pkg/proctree/proctree.go` + `proctree_linux.go` + `proctree_other.go`
- Test: `internal/pkg/proctree/proctree_test.go`

**Interfaces:**
- Consumes: `sysmem.Total`/`Available` (task 9), `config.DeriveMemoryDefaults` (task 5).
- Produces: `proctree.Snapshot() ([]Proc, error)`, `proctree.SumSubtree(procs []Proc, root int) uint64`, `proctree.Roots(procs []Proc, names []string) []Proc`.

This is the piece that works today, with nothing enabled, on both the server and
Android. It answers "who is actually using the memory" before any limit is chosen.

- [ ] **Step 1: Write the failing test**

`internal/pkg/proctree/proctree_test.go`:

```go
package proctree

import "testing"

// Summing a subtree is the whole point: a browser started by a tool
// started by an agent is where the memory actually is, and reading only
// the agent's own RSS reports a number that is wrong by an order of
// magnitude.
func TestSumSubtree(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, Name: "init", RSSBytes: 10},
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 150},
		{PID: 200, PPID: 100, Name: "node", RSSBytes: 200},
		{PID: 300, PPID: 200, Name: "chromium", RSSBytes: 900},
		{PID: 400, PPID: 1, Name: "codex", RSSBytes: 340},
	}

	if got := SumSubtree(procs, 100); got != 1250 {
		t.Fatalf("claude subtree = %d, want 1250 (150+200+900)", got)
	}
	if got := SumSubtree(procs, 400); got != 340 {
		t.Fatalf("codex subtree = %d, want 340", got)
	}
	if got := SumSubtree(procs, 999); got != 0 {
		t.Fatalf("unknown root = %d, want 0", got)
	}
}

// A cycle in the reported parent links must not hang the walk. /proc is
// sampled non-atomically, so a stale PPID can point anywhere.
func TestSumSubtree_TerminatesOnCycle(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 2, Name: "a", RSSBytes: 5},
		{PID: 2, PPID: 1, Name: "b", RSSBytes: 5},
	}
	done := make(chan uint64, 1)
	go func() { done <- SumSubtree(procs, 1) }()

	select {
	case got := <-done:
		if got != 10 {
			t.Fatalf("cyclic subtree = %d, want 10 counted once each", got)
		}
	default:
		// Give the goroutine a moment before declaring a hang.
	}
}

// Roots finds the processes worth reporting by name.
func TestRoots(t *testing.T) {
	procs := []Proc{
		{PID: 100, Name: "claude"},
		{PID: 200, Name: "node"},
		{PID: 300, Name: "codex"},
	}
	got := Roots(procs, []string{"claude", "codex"})
	if len(got) != 2 {
		t.Fatalf("found %d roots, want 2", len(got))
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/pkg/proctree/...`
Expected: FAIL — no Go files.

- [ ] **Step 3: Implement the pure core**

`internal/pkg/proctree/proctree.go`:

```go
// Package proctree summarises process memory by subtree.
//
// It exists so an operator can see who is actually using memory BEFORE
// enabling any limit — it reads /proc and needs no cgroup, no systemd,
// and no configuration change. Less precise than cgroup accounting
// (shared pages are counted once per process that maps them), but precise
// enough to choose a ceiling, and available where cgroups are not.
package proctree

// Proc is one process as sampled from /proc.
type Proc struct {
	PID      int
	PPID     int
	Name     string
	RSSBytes uint64
}

// SumSubtree totals RSS for root and every descendant.
//
// Visited-tracking is not defensive dressing: /proc is sampled without a
// lock, so a process can exit and its PID be reused mid-walk, producing
// parent links that form a cycle. Without this the walk would not
// terminate.
func SumSubtree(procs []Proc, root int) uint64 {
	children := make(map[int][]Proc, len(procs))
	self := make(map[int]Proc, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
		self[p.PID] = p
	}
	if _, ok := self[root]; !ok {
		return 0
	}

	visited := make(map[int]bool, len(procs))
	var total uint64
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[pid] {
			continue
		}
		visited[pid] = true
		total += self[pid].RSSBytes
		for _, c := range children[pid] {
			if !visited[c.PID] {
				stack = append(stack, c.PID)
			}
		}
	}
	return total
}

// Roots returns processes whose name matches any of names.
func Roots(procs []Proc, names []string) []Proc {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Proc
	for _, p := range procs {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: Implement the sampler**

`internal/pkg/proctree/proctree_linux.go`:

```go
//go:build linux || android

package proctree

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var procRoot = "/proc"

// Snapshot samples every readable process. Processes that vanish mid-walk
// are skipped rather than failing the sample — a snapshot of a moving
// system is expected to be slightly stale, not impossible.
func Snapshot() ([]Proc, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "status"))
		if err != nil {
			continue // exited between ReadDir and here
		}
		p := Proc{PID: pid}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "Name:":
				p.Name = f[1]
			case "PPid:":
				p.PPID, _ = strconv.Atoi(f[1])
			case "VmRSS:":
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				p.RSSBytes = kb * 1024
			}
		}
		out = append(out, p)
	}
	return out, nil
}
```

`internal/pkg/proctree/proctree_other.go`:

```go
//go:build !linux && !android

package proctree

import "errors"

// ErrUnsupported reports that this platform has no /proc to sample.
var ErrUnsupported = errors.New("process tree sampling not supported on this platform")

// Snapshot reports nothing off Linux.
func Snapshot() ([]Proc, error) { return nil, ErrUnsupported }
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/pkg/proctree/...`
Expected: PASS.

- [ ] **Step 6: Add the CLI command**

`cmd/cli/memory.go` — a `memory` parent with a `report` subcommand, registered in
`root.go` beside the existing commands. It must:

1. Print total and available RAM from `sysmem`.
2. Snapshot with `proctree`, find roots named `claude`, `codex`, `gemini`, and print each subtree total.
3. Print the largest descendant of each root, so the actual culprit (chromium) is named.
4. Print suggested settings from `config.DeriveMemoryDefaults(total, 1)` alongside observed peaks.
5. Support `--watch <dur>` and `--for <dur>`, sampling on an interval and reporting the MAXIMUM subtree seen per root — a single snapshot almost certainly misses a browser's peak, so watch mode is what produces a defensible number.
6. On a platform without `/proc`, print `process memory reporting requires Linux` and exit 0 — a report command that errors out is worse than one that says why it cannot help.

Follow the cobra style already in `cmd/cli/task.go`.

- [ ] **Step 7: Run it and build**

Run: `go build ./... && go run . memory report`
Expected: on Windows, the unsupported notice; on Linux, a table.

---

### Task 11: systemd unit — break the OOM restart loop

**Files:**
- Modify: `internal/pkg/daemon/service_linux.go` (:163-178)
- Test: `internal/pkg/daemon/service_linux_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Behaviour change in the generated unit only.

Worth doing even if the memory guard is never enabled. Today's `Restart=on-failure`
with no limit turns one OOM into a loop: wick dies, restarts 5 s later, resumes the
session, respawns the agent, balloons again. Nothing surfaces the repetition.

`Delegate=yes` is deliberately NOT added — that was needed only by the rejected
sub-cgroup placement. A sibling slice needs nothing from wick's own unit, which is
why existing installations get the guard without touching their service file.

- [ ] **Step 1: Write the failing test**

`internal/pkg/daemon/service_linux_test.go`:

```go
//go:build linux || android

package daemon

import (
	"strings"
	"testing"
)

// Without a start limit, an OOM crash-loops invisibly: restart, resume,
// respawn, balloon, repeat. With one, the unit lands in `failed` and the
// operator sees an incident instead of a mystery.
func TestRenderUnit_HasRestartRateLimit(t *testing.T) {
	got := renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log")

	for _, want := range []string{"StartLimitIntervalSec=", "StartLimitBurst="} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing %q:\n%s", want, got)
		}
	}
}

// The sibling-slice design needs nothing from wick's own unit. Adding
// Delegate= would signal the rejected sub-cgroup placement, in which
// agent memory counts toward wick's own cgroup.
func TestRenderUnit_NoDelegate(t *testing.T) {
	if strings.Contains(renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log"), "Delegate=") {
		t.Fatal("unit sets Delegate=; the sibling slice does not need it")
	}
}

// The existing behaviour must survive the edit.
func TestRenderUnit_KeepsExistingDirectives(t *testing.T) {
	got := renderUnit("wick", "/usr/local/bin/wick", "/var/log/wick.log")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/wick all",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit lost %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `GOOS=linux go test ./internal/pkg/daemon/ -run TestRenderUnit`
Expected: FAIL — `renderUnit` undefined.

- [ ] **Step 3: Extract and extend the renderer**

In `internal/pkg/daemon/service_linux.go`, extract the inline `fmt.Sprintf` in
`installSystemd` into a testable function, and add the rate limit:

```go
// renderUnit builds the systemd user unit for the daemon.
//
// StartLimit* is not incidental: Restart=on-failure with no ceiling turns
// a single OOM into an invisible loop — wick dies, restarts, resumes the
// session, respawns the agent, and balloons again. Three failures in five
// minutes leaves the unit in `failed`, turning a loop into a visible
// incident.
func renderUnit(appName, exePath, logFile string) string {
	return fmt.Sprintf(`[Unit]
Description=%s daemon
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=3

[Service]
Type=simple
ExecStart=%s all
Restart=on-failure
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, appName, exePath, logFile, logFile)
}
```

Then in `installSystemd`, replace the inline unit construction with:

```go
	unit := renderUnit(appName, p.ExePath, p.LogFile)
```

- [ ] **Step 4: Run the tests**

Run: `GOOS=linux go test ./internal/pkg/daemon/ -run TestRenderUnit`
Expected: PASS.

- [ ] **Step 5: Build for both platforms**

Run: `go build ./... && GOOS=linux go build ./...`
Expected: clean.

---

### Task 12: Diagnostics page

**Files:**
- Create: `internal/tools/agents/memory_handler.go`
- Modify: the agents tool's route registration (find with `grep -n "HandleFunc" internal/tools/agents/handler.go`)
- Test: `internal/tools/agents/memory_handler_test.go`

**Interfaces:**
- Consumes: `memscope.ReadStats` (task 3), `sysmem` + `proctree` (tasks 9–10), `config.DeriveMemoryDefaults` (task 5).
- Produces: `GET /agents/memory` returning a JSON summary, and `POST /agents/memory/apply-suggested` writing derived defaults into config.

- [ ] **Step 1: Write the failing test**

`internal/tools/agents/memory_handler_test.go`:

```go
package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The page must state plainly when it cannot protect anything. An
// operator reading a normal-looking dashboard on a machine with no scope
// support would believe they are guarded when they are not.
func TestMemoryHandler_ReportsUnavailability(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agents/memory", nil)

	memoryReportHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		ScopesAvailable bool   `json:"scopes_available"`
		Notice          string `json:"notice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.ScopesAvailable && body.Notice == "" {
		t.Fatal("scopes unavailable but no notice explaining it")
	}
}
```

- [ ] **Step 2: Run it, confirm it fails**

Run: `go test ./internal/tools/agents/ -run TestMemoryHandler`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the handler**

`internal/tools/agents/memory_handler.go` returns JSON with:

- `scopes_available` (from `memscope.Available()`) and, when false, `notice: "scope isolation unavailable — systemd user session not reachable; agents run unguarded"`.
- `machine`: total and available bytes from `sysmem`.
- `agents`: one row per running agent — provider, session, current subtree bytes (`proctree`), peak bytes (`memscope.ReadStats`), and the limit in force.
- `suggested`: `config.DeriveMemoryDefaults(total, currentMaxConcurrent)`.

Use the zerolog pattern for any logging.

- [ ] **Step 4: Add the apply-suggested endpoint**

`POST /agents/memory/apply-suggested` writes the derived values into
`GeneralConfig` through whatever config-write path the other agents handlers already
use (`grep -n "SetConfig\|SaveGeneral" internal/tools/agents/*.go`). It must not
change `MemoryGuardMode` — filling in numbers is not the same as turning enforcement
on, and conflating them would enable killing without the operator asking.

- [ ] **Step 5: Wire the route and run the tests**

Run: `go test ./internal/tools/agents/... && go build ./...`
Expected: PASS.

- [ ] **Step 6: Full verification**

Run: `go build ./... && go test ./...`
Expected: PASS.

---

## Verification before declaring done

Run all of these and paste real output — no claim of completion without it:

```bash
go build ./...
go test ./...
GOOS=linux go build ./...
```

On a Linux box with a systemd user session:

```bash
go test -tags integration ./internal/agents/provider/memscope/... -v
```

Manual, before enabling `enforce` in production:

1. `wick memory report --watch 30s --for 1h` — confirm the numbers look sane.
2. Set mode `measure`, spawn an agent, confirm `memory.peak` is readable and nothing died.
3. Set a deliberately low `AgentMemoryMaxMB`, spawn an agent, confirm it is killed with `ExitOOM` and a message naming both numbers, and that **wick and every sibling agent survive**.
