package agentmemory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

// healthTTL bounds how long a health-probe result is trusted, so a status
// poll doesn't hit the daemon on every request.
const healthTTL = 5 * time.Second

// verTTL bounds how long a resolved binary version is trusted. Resolving it
// costs a process spawn, and the binary only changes on an install.
const verTTL = time.Hour

// Manager owns the lifecycle of one backend's daemon: install, start, stop,
// and the health probe that answers "is it actually up?". Construct via
// newManager (done by Register).
//
// No reverse proxy here, unlike airouter.Manager — the Agent Memory panel is
// wick's own FE talking to the backend's API, not an embedded dashboard.
type Manager struct {
	desc Descriptor

	// act serialises whole lifecycle SEQUENCES — start-and-wait, stop,
	// restart — where mu only guards the fields each step touches. The
	// watchdog and a person pressing Stop are two callers of the same
	// three methods, and without this an operator's Stop could land
	// between the watchdog's spawn and its readiness wait, leaving a
	// daemon running that wick believes it stopped (PLAN §25.3 guard 4).
	act sync.Mutex

	mu  sync.Mutex
	cmd *exec.Cmd
	// exited is closed by the reaper goroutine when cmd's process ends.
	// It is how "wick spawned it" is told apart from "wick spawned it and
	// it is still alive" — the difference between the watchdog's dead and
	// hung states (PLAN §25.2). nil when no process of ours was started.
	exited   chan struct{}
	starting bool         // true between spawn and first healthy probe
	port     atomic.Int32 // bound loopback port (0 until first start)
	pref     atomic.Int32 // preferred port; descriptor default until configured
	opt      LaunchOptions
	// started is the unix-ms spawn time of the daemon wick is running, 0
	// when no process of ours is up. Only wick's own spawn is timed: an
	// adopted daemon's start is not ours to know, and guessing it would
	// put an invented uptime on the Overview card.
	started atomic.Int64

	log  zerolog.Logger
	logs *logBuffer

	healthMu sync.Mutex
	healthOK bool
	healthAt time.Time

	verMu     sync.Mutex
	verCached string
	verAt     time.Time

	// operatorStop records that a PERSON stopped this daemon, so the
	// watchdog leaves it down. Without it the Stop button lies: the
	// daemon comes back within half a minute and nothing says why
	// (PLAN §25.3 guard 1). Cleared by an explicit start.
	operatorStop atomic.Bool

	// external* back the authenticated route that lets something outside
	// wick reach this daemon (external.go). All three are nil until
	// RegisterRoutes wires them, and every gate reads nil as closed.
	externalAllowed func() bool
	externalToken   func() string
	daemonToken     func() string
	ext             externalStats
}

func newManager(d Descriptor) *Manager {
	logger := log.With().Str("component", "agentmemory").Str("backend", d.ID).Logger()
	m := &Manager{desc: d, log: logger, logs: newLogBuffer()}
	m.pref.Store(int32(d.PrefPort))
	logger.Info().Int("pref_port", d.PrefPort).Msg("agentmemory: manager configured")
	return m
}

// Desc exposes the descriptor this manager drives.
func (m *Manager) Desc() Descriptor { return m.desc }

// BoundPort is the port the daemon is listening on — the preferred port until
// the first start remaps it.
func (m *Manager) BoundPort() int {
	if p := int(m.port.Load()); p != 0 {
		return p
	}
	return m.PrefPort()
}

// PrefPort is the port the next start tries first: the configured one, else
// the descriptor's default.
func (m *Manager) PrefPort() int {
	if p := int(m.pref.Load()); p > 0 {
		return p
	}
	return m.desc.PrefPort
}

// SetPrefPort records the port the daemon should prefer. 0 restores the
// descriptor's default. Takes effect on the next start — a running daemon
// keeps the port it bound, which is why the panel shows both numbers.
func (m *Manager) SetPrefPort(p int) {
	if p < 0 {
		p = 0
	}
	m.pref.Store(int32(p))
	// A daemon wick did not spawn is only reachable via the preferred port
	// (nothing recorded a bound one), so drop a stale bound port when the
	// preference changes and no process of ours is running.
	if !m.spawnedHere() {
		m.port.Store(0)
		m.invalidateHealth()
	}
}

// PID is the process id of the daemon wick spawned, or 0 when the daemon is
// not ours (started by hand, or surviving a wick restart). Used to read the
// process's RSS — a number Yoga asked to see rather than have hidden, since a
// memory backend is exactly the sort of thing that quietly grows (PLAN §18.5).
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}

// StartedAtMS is when wick spawned the running daemon (unix ms), 0 when the
// process is not ours. The panel turns it into an uptime; a zero renders as
// "unknown" rather than as a daemon that just started.
func (m *Manager) StartedAtMS() int64 {
	if !m.spawnedHere() {
		return 0
	}
	return m.started.Load()
}

// LaunchOpts returns the options the next start will use.
func (m *Manager) LaunchOpts() LaunchOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opt
}

// BaseURL is the loopback base URL of the daemon, e.g.
// "http://127.0.0.1:49374". It is what an instance that sets no explicit
// server URL talks to.
func (m *Manager) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.BoundPort())
}

// SetLaunchOptions records the knobs the next start uses. Port is resolved at
// start and ignored here. Takes effect on the next start — a running daemon
// keeps the options it was started with.
//
// It rebuilds the struct rather than assigning it so Port cannot be smuggled
// in, and EVERY other field has to be carried across explicitly. It used to
// carry only DataDir and EnableWeb, which silently dropped the bearer token
// and the whole tuning block on the floor: ApplySettings decrypted the token
// at the last moment, handed it over, and the daemon was started without it.
// Thirty tuning fields in the Settings tab did nothing, and nothing said so —
// the same failure as a Save that answers OK and persists nothing.
func (m *Manager) SetLaunchOptions(opt LaunchOptions) {
	m.mu.Lock()
	m.opt = LaunchOptions{
		DataDir:   strings.TrimSpace(opt.DataDir),
		EnableWeb: opt.EnableWeb,
		AuthToken: opt.AuthToken,
		Tuning:    opt.Tuning,
	}
	m.mu.Unlock()
}

// SetDataDir records the store location alone, leaving the other launch
// options untouched. The store is a DAEMON-level setting, not a per-instance
// one (PLAN §19): one daemon already holds every workspace and project, and
// the separation that matters happens on the workspace/project axis. Takes
// effect on the next start.
func (m *Manager) SetDataDir(dir string) {
	m.mu.Lock()
	m.opt.DataDir = strings.TrimSpace(dir)
	m.mu.Unlock()
}

// ── install ──────────────────────────────────────────────────────────

// Install puts the backend's binary on the machine, into a directory wick
// owns — no sudo, no /usr/bin, and no change to anybody's PATH. The path is
// then what every exec path resolves to (ResolveBackendBin), which is the
// whole point: before this, a daemon could be answering on HTTP while every
// CLI-backed read said "executable file not found in $PATH".
//
// A backend wick has no install route for answers ErrInstallNotSupported —
// a different thing from a failed install, and the panel says so differently.
func (m *Manager) Install(ctx context.Context) (string, error) {
	kind := m.desc.InstallKind
	if kind == "" {
		kind = InstallManual
	}
	if kind != InstallGitHubRelease || m.desc.ReleaseRepo == "" {
		return "", fmt.Errorf("%w: put %q on PATH yourself (%s)", ErrInstallNotSupported, m.desc.BinName, m.desc.GitHubURL)
	}
	ri := releaseInstall{
		repo:    m.desc.ReleaseRepo,
		bin:     m.desc.BinName,
		dest:    BinDir(),
		goos:    hostGOOS(),
		goarch:  hostGOARCH(),
		api:     githubAPIBase,
		logLine: func(l string) { m.log.Info().Msg("agentmemory: install: " + l) },
	}
	out, err := ri.run(ctx)
	if err != nil {
		m.log.Error().Err(err).Msg("agentmemory: install failed")
		return out, err
	}
	// The version cache is resolved from the binary; a fresh install must
	// not be reported under the previous answer ("" = not installed).
	m.verMu.Lock()
	m.verCached, m.verAt = "", time.Time{}
	m.verMu.Unlock()
	return out, nil
}

// Installed reports whether the backend's binary resolves on PATH.
func (m *Manager) Installed() bool { return m.BinPath() != "" }

// BinPath is the absolute path of the backend's binary, "" when it resolves
// nowhere. Spawn wiring needs the path and not just the yes/no of Installed:
// a capture hook is a command the AGENT's shell runs, and a bare name there
// would resolve against the agent's PATH rather than wick's.
//
// The resolution itself lives in ResolveBackendBin — wick's own installed
// copy first, PATH second — so a binary wick downloaded is found by every
// exec path without anyone editing a shell profile (see install.go).
func (m *Manager) BinPath() string {
	if m.desc.BinName == "" {
		return ""
	}
	p, err := ResolveBackendBin(m.desc.BinName)
	if err != nil {
		return ""
	}
	return p
}

// version returns the binary's reported version, TTL-cached: resolving it is
// a process spawn and Status is polled.
func (m *Manager) version(ctx context.Context) string {
	m.verMu.Lock()
	defer m.verMu.Unlock()
	if !m.verAt.IsZero() && time.Since(m.verAt) < verTTL {
		return m.verCached
	}
	m.verCached = m.installedVersion(ctx)
	m.verAt = time.Now()
	return m.verCached
}

// installedVersion returns the binary's reported version, or "" when the
// binary is missing or doesn't answer.
//
// ai-memory 2.4.0 prints "ai-memory 2.4.0" on one line (verified 2026-09-25),
// so this takes the last whitespace-separated token of the first line. An
// unusual format from some other backend yields that backend's last token
// rather than an error — a wrong-looking version beats an empty panel.
func (m *Manager) installedVersion(ctx context.Context) string {
	bin := m.BinPath()
	if bin == "" {
		return ""
	}
	out, err := safeexec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return ""
	}
	fields := strings.Fields(line)
	return fields[len(fields)-1]
}

// ── process lifecycle ────────────────────────────────────────────────

// allocPort returns a free loopback port at or after start, so a taken
// preferred port (another backend, or an ai-memory the user started himself)
// doesn't stop the daemon from coming up.
func allocPort(start int) int {
	if start <= 0 {
		start = 49374
	}
	for p := start; p < start+128; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			return p
		}
	}
	return start
}

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return nil
	}
	if m.desc.Launch == nil {
		return fmt.Errorf("agentmemory: %s has no launch command", m.desc.ID)
	}
	bin := m.BinPath()
	if bin == "" {
		return fmt.Errorf("%s is not installed — install it from the panel, or put %q on PATH", m.desc.DisplayName, m.desc.BinName)
	}
	port := allocPort(m.PrefPort())
	m.port.Store(int32(port))
	m.logs.Reset()
	m.invalidateHealth()

	opt := m.opt
	opt.Port = port
	args, extraEnv := m.desc.Launch(opt)

	cmd := safeexec.Command(bin, args...)
	cmd.Stdout = m.logs
	cmd.Stderr = m.logs
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	m.log.Info().Str("bin", bin).Int("port", port).Str("data_dir", opt.DataDir).Msg("agentmemory: spawning")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", m.desc.DisplayName, err)
	}
	m.cmd = cmd
	// One reaper per spawn. It exists for two reasons: a child that exits
	// on its own is reaped instead of becoming a zombie, and the closed
	// channel is what lets the watchdog see that the process wick spawned
	// is GONE rather than merely unhealthy (PLAN §25.2).
	done := make(chan struct{})
	m.exited = done
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	m.started.Store(time.Now().UnixMilli())
	m.log.Info().Int("pid", cmd.Process.Pid).Msg("agentmemory: spawned")
	return nil
}

// reapTimeout bounds how long stop waits for a killed daemon to be reaped.
// A SIGKILLed process is gone in milliseconds; this is only here so a child
// wedged in uninterruptible I/O cannot hold the lock for the whole process.
const reapTimeout = 5 * time.Second

func (m *Manager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return
	}
	pid := m.cmd.Process.Pid
	_ = m.cmd.Process.Kill()
	// Wait belongs to the reaper goroutine started with the process —
	// calling it here too would be "Wait was already called". Waiting on
	// the channel keeps the same guarantee: stop returns once the child
	// is actually gone.
	if m.exited != nil {
		select {
		case <-m.exited:
		case <-time.After(reapTimeout):
			m.log.Warn().Int("pid", pid).Msg("agentmemory: killed daemon did not exit within the reap timeout")
		}
	}
	m.cmd = nil
	m.exited = nil
	m.started.Store(0)
	m.invalidateHealth()
	m.log.Info().Int("pid", pid).Msg("agentmemory: stopped")
}

// StopProcess kills the daemon WITHOUT recording an intent: shutdown hooks
// and the watchdog's own restart use it, and neither is a person deciding the
// daemon should stay down.
func (m *Manager) StopProcess() {
	m.act.Lock()
	defer m.act.Unlock()
	m.stop()
}

// StopByOperator kills the daemon and records that a person asked for it, so
// the watchdog does not undo the decision. This is what the Stop button
// calls; everything else uses StopProcess (PLAN §25.3 guard 1).
func (m *Manager) StopByOperator() {
	m.act.Lock()
	defer m.act.Unlock()
	m.operatorStop.Store(true)
	m.stop()
}

// OperatorStopped reports whether the daemon is down because someone stopped
// it. Surfaced in the watchdog payload so the panel can say why nothing is
// being restarted.
func (m *Manager) OperatorStopped() bool { return m.operatorStop.Load() }

// StartAndWait spawns the daemon (if not already running) and blocks until it
// answers its health path or ctx expires.
//
// Any explicit start clears the operator-stop marker: asking for the daemon
// to run is the other half of having asked for it to stop, and leaving the
// marker set would keep the watchdog quiet about a daemon someone has since
// started on purpose.
func (m *Manager) StartAndWait(ctx context.Context) error {
	m.act.Lock()
	defer m.act.Unlock()
	return m.startAndWait(ctx)
}

// startAndWait is the sequence itself. Callers hold act.
func (m *Manager) startAndWait(ctx context.Context) error {
	m.operatorStop.Store(false)
	m.setStarting(true)
	defer m.setStarting(false)
	if err := m.start(); err != nil {
		return err
	}
	return m.waitReady(ctx)
}

// Restart stops the daemon and starts it again, waiting for readiness — the
// way a changed data dir or web-UI toggle takes effect, and how the watchdog
// clears a hung one.
func (m *Manager) Restart(ctx context.Context) error {
	m.act.Lock()
	defer m.act.Unlock()
	m.stop()
	return m.startAndWait(ctx)
}

// ChildAlive reports whether the process wick spawned is still running.
//
// It is not the same question as healthy(): a daemon that exited leaves
// nothing to probe, while a daemon that is wedged answers no probe and is
// very much alive. The watchdog needs both to tell dead from hung, and the
// two calls for the same state is the whole point (PLAN §25.2).
func (m *Manager) ChildAlive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return false
	}
	if m.exited == nil {
		return true
	}
	select {
	case <-m.exited:
		return false
	default:
		return true
	}
}

func (m *Manager) waitReady(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become ready in time", m.desc.DisplayName)
		case <-time.After(200 * time.Millisecond):
			if m.probeHealth() {
				return nil
			}
		}
	}
}

func (m *Manager) isStarting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.starting
}

func (m *Manager) setStarting(v bool) {
	m.mu.Lock()
	m.starting = v
	m.mu.Unlock()
}

// spawnedHere reports whether the running daemon is one wick itself started.
func (m *Manager) spawnedHere() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// ── health ───────────────────────────────────────────────────────────

// healthy reports whether the daemon answers on its health path, TTL-cached
// so repeated status polls don't hammer it. True for a daemon wick didn't
// spawn too — one the user started, or one that outlived a wick restart —
// which is what callers actually care about.
func (m *Manager) healthy() bool {
	m.healthMu.Lock()
	if !m.healthAt.IsZero() && time.Since(m.healthAt) < healthTTL {
		ok := m.healthOK
		m.healthMu.Unlock()
		return ok
	}
	m.healthMu.Unlock()

	ok := m.probeHealth()
	m.healthMu.Lock()
	m.healthOK = ok
	m.healthAt = time.Now()
	m.healthMu.Unlock()
	return ok
}

// probeHealth does the uncached check: GET <base><HealthPath> must answer 200.
// With no health path configured it degrades to a TCP connect, which only
// proves something holds the port.
//
// Note for ai-memory: the health path is "/healthz" and nothing else —
// "/", "/health", "/status" and "/version" all 404 (verified 2026-09-25), so a
// non-200-based probe would report a live daemon as down.
func (m *Manager) probeHealth() bool {
	port := m.BoundPort()
	if m.desc.HealthPath == "" {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, m.desc.HealthPath)
	client := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Drain the small body so the connection can be reused by the next probe.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// invalidateHealth drops the cached probe result so the next check reflects
// the process change that just happened. Caller holds m.mu.
func (m *Manager) invalidateHealth() {
	m.healthMu.Lock()
	m.healthAt = time.Time{}
	m.healthOK = false
	m.healthMu.Unlock()
}

// ── status ───────────────────────────────────────────────────────────

// Status is the daemon's install + run state. The control endpoints (next
// slice) serialise this as-is; nothing here touches HTTP.
type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Running   bool   `json:"running"`
	Managed   bool   `json:"managed"` // true = the process is one wick spawned
	State     string `json:"state"`   // "not-installed"|"starting"|"running"|"stopped"
	PrefPort  int    `json:"pref_port"`
	BoundPort int    `json:"bound_port"`
	BaseURL   string `json:"base_url"`
	// StartedAtMS is the spawn time of a daemon wick is running (unix ms).
	// 0 = unknown, which is the honest answer for an adopted daemon.
	StartedAtMS int64 `json:"started_at_ms,omitempty"`
}

// Status reports the current state. Running is health-probe truth, so a daemon
// started outside wick counts as running.
func (m *Manager) Status(ctx context.Context) Status {
	running := m.healthy()
	installed := m.Installed()

	state := "stopped"
	switch {
	case !installed && !running:
		state = "not-installed"
	case m.isStarting() && !running:
		state = "starting"
	case running:
		state = "running"
	}
	st := Status{
		// Installed answers ONE question: does the binary resolve. It used
		// to be `installed || running`, which quietly made a daemon someone
		// else started count as proof that wick has the binary — and on a
		// host where that was true the panel showed no Install control at
		// all while every CLI-backed read failed with "not found in $PATH".
		// A process answering on a port and an executable being present are
		// different facts; conflating them hid the one action that fixes it.
		Installed: installed,
		Running:   running,
		Managed:   m.spawnedHere(),
		State:     state,
		PrefPort:  m.PrefPort(),
		BoundPort: m.BoundPort(),
		BaseURL:   m.BaseURL(),

		StartedAtMS: m.StartedAtMS(),
	}
	if installed {
		st.Version = m.version(ctx)
	}
	return st
}

// Logs returns the retained tail of the daemon's output.
func (m *Manager) Logs() string { return m.logs.Snapshot() }

// ── log tail ─────────────────────────────────────────────────────────

// logBuffer is a thread-safe, bounded sink for the daemon's stdout+stderr. It
// satisfies io.Writer (passed as cmd.Stdout/Stderr) and keeps only the most
// recent maxLogBytes so a long-running daemon can't grow it without bound.
type logBuffer struct {
	mu  sync.Mutex
	buf []byte
}

// maxLogBytes caps the retained log tail (~64 KiB is plenty for a glance).
const maxLogBytes = 64 * 1024

func newLogBuffer() *logBuffer { return &logBuffer{} }

// Write appends p, trimming from the front to stay under maxLogBytes. The trim
// snaps to the next newline so we never show a half line.
func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > maxLogBytes {
		over := len(l.buf) - maxLogBytes
		if nl := bytes.IndexByte(l.buf[over:], '\n'); nl >= 0 {
			over += nl + 1
		}
		l.buf = append(l.buf[:0], l.buf[over:]...)
	}
	return len(p), nil
}

// Snapshot returns a copy of the retained log tail.
func (l *logBuffer) Snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// Reset clears the buffer — called on each (re)start so the panel shows only
// the current daemon's output.
func (l *logBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = l.buf[:0]
}
