// Package plugin is the host side of service plugins: a kind=service binary
// under plugins/services/<key>/ is kept running by a Supervisor (spawned at
// boot, restarted with backoff after a crash, put to sleep after an idle
// limit when auto-off applies and woken by the next request) and
// reverse-proxied at /x/{key}/* with auth decided per manifest route.
package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// LogLines is how many trailing log lines a service keeps for its page.
const LogLines = 200

// Backoff bounds between restarts of a crashed service.
var (
	BackoffMin = time.Second
	BackoffMax = 30 * time.Second
	// stableAfter resets the backoff once a process has stayed up this long.
	stableAfter = time.Minute
	pollEvery   = 200 * time.Millisecond
	// StopGrace is how long a process gets after SIGTERM before SIGKILL.
	StopGrace = 5 * time.Second
	// stopWait bounds how long Stop waits for the supervise loop to unwind
	// (a spawn still in its handshake).
	stopWait = 15 * time.Second
	// idleCheckEvery is how often a running auto-off service is checked
	// for idleness.
	idleCheckEvery = 15 * time.Second
	// WakeTimeout bounds how long a request waits for a sleeping service to
	// boot before it fails.
	WakeTimeout = 30 * time.Second
)

// ErrNotRunning is returned for a request while the service is down.
var ErrNotRunning = errors.New("service plugin not running")

// ErrWakeTimeout is returned when a sleeping service did not boot within
// WakeTimeout.
var ErrWakeTimeout = errors.New("service plugin did not wake up in time")

// RingLog keeps the last n lines written to it.
type RingLog struct {
	mu    sync.Mutex
	n     int
	lines []string
	part  []byte
}

// NewRingLog keeps n lines.
func NewRingLog(n int) *RingLog { return &RingLog{n: n} }

func (l *RingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.part = append(l.part, p...)
	for {
		i := bytes.IndexByte(l.part, '\n')
		if i < 0 {
			break
		}
		l.add(string(l.part[:i]))
		l.part = l.part[i+1:]
	}
	return len(p), nil
}

// Printf adds one host-side line (spawn, exit, restart).
func (l *RingLog) Printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.add(time.Now().Format("15:04:05") + " [wick] " + fmt.Sprintf(format, a...))
}

func (l *RingLog) add(s string) {
	l.lines = append(l.lines, strings.TrimRight(s, "\r"))
	if over := len(l.lines) - l.n; over > 0 {
		l.lines = append([]string(nil), l.lines[over:]...)
	}
}

// Lines returns a copy of the kept lines, oldest first.
func (l *RingLog) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// spawnFn starts one service process serving HTTP on socket with env added
// and stderr captured. exited is closed when the process is gone.
type spawnFn func(binary, socket string, env []string, stderr io.Writer) (kill func(), conn wickplugin.ToolConn, exited <-chan struct{}, err error)

func spawnProcess(binary, socket string, env []string, stderr io.Writer) (func(), wickplugin.ToolConn, <-chan struct{}, error) {
	client, raw, err := connplugin.Spawn(connplugin.SpawnSpec{
		Binary:    binary,
		SocketDir: filepath.Dir(socket),
		Plugins:   wickplugin.ToolVersionedPlugins,
		Dispense:  wickplugin.ToolPluginName,
		Env:       append([]string{wickplugin.EnvToolSocket + "=" + socket}, env...),
		Stderr:    stderr,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	conn, ok := raw.(wickplugin.ToolConn)
	if !ok {
		client.Kill()
		return nil, nil, nil, fmt.Errorf("plugin %s is not a service plugin", binary)
	}
	exited := make(chan struct{})
	go func() {
		for !client.Exited() {
			time.Sleep(pollEvery)
		}
		close(exited)
	}()
	pid := 0
	if rc := client.ReattachConfig(); rc != nil {
		pid = rc.Pid
	}
	kill := func() { terminate(pid, exited, StopGrace); client.Kill() }
	return kill, conn, exited, nil
}

// terminate sends SIGTERM to pid and waits up to grace for exited; the
// caller force-kills after. Platforms without SIGTERM go straight to kill.
func terminate(pid int, exited <-chan struct{}, grace time.Duration) {
	if pid <= 0 {
		return
	}
	p, err := os.FindProcess(pid)
	if err != nil || p.Signal(syscall.SIGTERM) != nil {
		return
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-exited:
	case <-t.C:
	}
}

// State of a supervised service.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateBackoff  = "backoff" // crashed; waiting to restart
	// StateSleeping: stopped by auto-off after the idle limit; the next
	// request starts it again. Not a crash, so no backoff.
	StateSleeping = "sleeping"
)

// Status is what the admin page shows.
type Status struct {
	State     string    `json:"state"`
	Restarts  int       `json:"restarts"`
	StartedAt time.Time `json:"started_at,omitempty"`
	NextStart time.Time `json:"next_start,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	// SleptAt is when auto-off last put the service to sleep.
	SleptAt time.Time `json:"slept_at,omitempty"`
	// LastActive is when a request or remote turn last touched it.
	LastActive time.Time `json:"last_active,omitempty"`
	// LastWakeMS is how long the last wake took, from the request that
	// woke it until the process was ready.
	LastWakeMS int64 `json:"last_wake_ms,omitempty"`
}

// Supervisor keeps one service plugin process running: spawn, push config,
// watch for exit, restart with backoff 1s→30s until Stop.
type Supervisor struct {
	key, binary, sockDir string
	spawn                spawnFn
	// env is called before every spawn (callback token, base URL).
	env func() []string
	// cfg returns the service's config, pushed after every spawn.
	cfg func() map[string]string
	// autoOff reports whether the service may sleep and its idle limit
	// (nil = never).
	autoOff func() (bool, time.Duration)
	Logs    *RingLog

	// active counts requests in flight through Transport (a remote turn's
	// event stream stays open for the whole turn).
	active atomic.Int64

	mu      sync.Mutex
	status  Status
	rt      http.RoundTripper
	conn    wickplugin.ToolConn
	kill    func()
	cancel  context.CancelFunc
	wakeNow chan struct{}
	// done is closed when the supervise loop has returned.
	done chan struct{}
	// lastActive is when a request last started or ended.
	lastActive time.Time
	// waking is set by the request that wakes a sleeping service; woken is
	// closed once that boot has finished (ok or not), so every request
	// that arrived meanwhile waits for the same boot.
	waking    bool
	wakeStart time.Time
	woken     chan struct{}
}

func newSupervisor(key, binary, sockDir string, spawn spawnFn) *Supervisor {
	return &Supervisor{key: key, binary: binary, sockDir: sockDir, spawn: spawn, Logs: NewRingLog(LogLines),
		status: Status{State: StateStopped}}
}

// Status returns the current status.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.LastActive = s.lastActive
	return st
}

// Transport returns the round tripper of the running process. A sleeping
// service is woken and the call waits (up to WakeTimeout) for it to boot;
// concurrent callers share that one boot. A service stopped by the admin
// or crashed is not woken.
func (s *Supervisor) Transport() (http.RoundTripper, error) {
	deadline := time.Now().Add(WakeTimeout)
	for {
		s.mu.Lock()
		switch {
		case s.status.State == StateRunning && s.rt != nil:
			s.lastActive = time.Now()
			rt := &activityTransport{rt: s.rt, s: s}
			s.mu.Unlock()
			return rt, nil
		case s.status.State == StateSleeping || (s.status.State == StateStarting && s.waking):
			if !s.waking {
				s.waking, s.wakeStart = true, time.Now()
				s.Logs.Printf("waking up on request")
				select {
				case s.wakeNow <- struct{}{}:
				default:
				}
			}
			if s.woken == nil {
				s.woken = make(chan struct{})
			}
			woken := s.woken
			s.mu.Unlock()
			t := time.NewTimer(time.Until(deadline))
			select {
			case <-woken:
				t.Stop()
			case <-t.C:
				return nil, ErrWakeTimeout
			}
		default:
			s.mu.Unlock()
			return nil, ErrNotRunning
		}
	}
}

// finishWake releases the requests waiting on a wake once its boot ended.
func (s *Supervisor) finishWake() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waking && s.status.State == StateRunning {
		s.status.LastWakeMS = time.Since(s.wakeStart).Milliseconds()
		s.Logs.Printf("woke up in %s", time.Since(s.wakeStart).Round(time.Millisecond))
	}
	s.waking = false
	if s.woken != nil {
		close(s.woken)
		s.woken = nil
	}
}

func (s *Supervisor) touch(delta int64) {
	s.active.Add(delta)
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

// activityTransport counts a request as active until its response body is
// closed, so a long stream (SSE, a remote turn) keeps the service awake.
type activityTransport struct {
	rt http.RoundTripper
	s  *Supervisor
}

func (a *activityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	a.s.touch(1)
	resp, err := a.rt.RoundTrip(r)
	if err != nil {
		a.s.touch(-1)
		return nil, err
	}
	resp.Body = &activityBody{ReadCloser: resp.Body, done: sync.OnceFunc(func() { a.s.touch(-1) })}
	return resp, nil
}

type activityBody struct {
	io.ReadCloser
	done func()
}

func (b *activityBody) Close() error {
	err := b.ReadCloser.Close()
	b.done()
	return err
}

// Start begins supervising (no-op when already started; wakes a sleeping
// service).
func (s *Supervisor) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		if s.status.State == StateSleeping {
			s.Logs.Printf("woken by admin")
			select {
			case s.wakeNow <- struct{}{}:
			default:
			}
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wakeNow = make(chan struct{}, 1)
	s.done = make(chan struct{})
	s.status.State = StateStarting
	go func(done chan struct{}) {
		defer close(done)
		s.loop(ctx, s.wakeNow)
	}(s.done)
}

// Stop terminates the process (SIGTERM, then SIGKILL after StopGrace),
// removes its socket and stops restarting it. It returns once the supervise
// loop is gone, so no spawn still in flight outlives it.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel, kill, done := s.cancel, s.kill, s.done
	s.cancel, s.kill, s.rt, s.conn, s.done = nil, nil, nil, nil, nil
	s.status.State = StateStopped
	s.status.NextStart = time.Time{}
	s.waking = false
	if s.woken != nil {
		close(s.woken)
		s.woken = nil
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if kill != nil {
		kill()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(stopWait):
			s.Logs.Printf("stop: supervise loop still busy after %s", stopWait)
		}
	}
}

// Reconfigure pushes the current config to the running process. A plugin
// that rejects the push is restarted so it boots with the new config. A
// stopped service picks the config up on its next start.
func (s *Supervisor) Reconfigure() {
	s.mu.Lock()
	conn := s.conn
	running := s.status.State == StateRunning
	s.mu.Unlock()
	if !running || conn == nil || s.cfg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := conn.Configure(ctx, s.cfg())
	cancel()
	if err != nil {
		s.Logs.Printf("config push failed (%v); restarting", err)
		s.Restart()
		return
	}
	s.Logs.Printf("config updated")
}

// Restart stops and starts again, resetting the backoff.
func (s *Supervisor) Restart() {
	s.Stop()
	s.mu.Lock()
	s.status.Restarts = 0
	s.mu.Unlock()
	s.Start()
}

func (s *Supervisor) loop(ctx context.Context, wake chan struct{}) {
	backoff := BackoffMin
	for {
		started := time.Now()
		exited, err := s.runOnce(ctx)
		s.finishWake()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.Logs.Printf("start failed: %v", err)
		} else {
			slept, done := s.watch(ctx, exited)
			if done {
				return
			}
			if slept {
				select {
				case <-wake:
				case <-ctx.Done():
					return
				}
				backoff = BackoffMin
				continue
			}
			s.Logs.Printf("process exited")
		}
		if time.Since(started) >= stableAfter {
			backoff = BackoffMin
		}
		s.mu.Lock()
		if s.cancel == nil { // stopped meanwhile
			s.mu.Unlock()
			return
		}
		s.rt, s.kill, s.conn = nil, nil, nil
		s.status.State = StateBackoff
		s.status.Restarts++
		if err != nil {
			s.status.LastError = err.Error()
		} else {
			s.status.LastError = "process exited"
		}
		s.status.NextStart = time.Now().Add(backoff)
		s.mu.Unlock()
		s.Logs.Printf("restarting in %s", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-wake:
			t.Stop()
		case <-ctx.Done():
			t.Stop()
			return
		}
		if backoff *= 2; backoff > BackoffMax {
			backoff = BackoffMax
		}
	}
}

// watch waits for the running process to exit, putting it to sleep when
// auto-off applies and it has been idle for the limit. slept reports a
// sleep (not a crash); done that the supervisor was stopped.
func (s *Supervisor) watch(ctx context.Context, exited <-chan struct{}) (slept, done bool) {
	t := time.NewTicker(idleCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-exited:
			return false, false
		case <-ctx.Done():
			return false, true
		case <-t.C:
			if !s.trySleep() {
				continue
			}
			select {
			case <-exited:
			case <-ctx.Done():
				return false, true
			}
			return true, false
		}
	}
}

// trySleep stops the process gracefully (as an admin stop does) when the
// service may auto-off and nothing has touched it for the idle limit.
func (s *Supervisor) trySleep() bool {
	if s.autoOff == nil {
		return false
	}
	on, idle := s.autoOff()
	if !on || idle <= 0 || s.active.Load() > 0 {
		return false
	}
	s.mu.Lock()
	if s.status.State != StateRunning || s.active.Load() > 0 || time.Since(s.lastActive) < idle {
		s.mu.Unlock()
		return false
	}
	select { // a stale wake must not undo this sleep
	case <-s.wakeNow:
	default:
	}
	kill := s.kill
	s.rt, s.kill, s.conn = nil, nil, nil
	s.status.State = StateSleeping
	s.status.SleptAt = time.Now()
	s.mu.Unlock()
	s.Logs.Printf("idle for %s; sleeping until the next request", idle)
	if kill != nil {
		kill()
	}
	return true
}

func (s *Supervisor) runOnce(ctx context.Context) (<-chan struct{}, error) {
	s.mu.Lock()
	s.status.State = StateStarting
	s.mu.Unlock()
	_ = os.MkdirAll(s.sockDir, 0o700)
	socket := filepath.Join(s.sockDir, fmt.Sprintf("svc-%s-%d.sock", s.key, time.Now().UnixNano()%1e9))
	var env []string
	if s.env != nil {
		env = s.env()
	}
	kill, conn, exited, err := s.spawn(s.binary, socket, env, s.Logs)
	if err != nil {
		return nil, err
	}
	killAll := func() { kill(); _ = os.Remove(socket) }
	if s.cfg != nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := conn.Configure(cctx, s.cfg())
		cancel()
		if err != nil {
			killAll()
			return nil, fmt.Errorf("configure: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		killAll()
		return nil, ctx.Err()
	}
	s.kill, s.rt, s.conn = killAll, unixTransport(socket), conn
	s.status.State = StateRunning
	s.status.StartedAt = time.Now()
	s.lastActive = s.status.StartedAt
	s.status.NextStart = time.Time{}
	s.Logs.Printf("running")
	return exited, nil
}

func unixTransport(socket string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second,
		DisableCompression: true,
	}
}
