// Package plugin is the host side of service plugins: a kind=service binary
// under plugins/services/<key>/ is kept running by a Supervisor (spawned at
// boot, restarted with backoff after a crash) and reverse-proxied at
// /x/{key}/* with auth decided per manifest route.
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
)

// ErrNotRunning is returned for a request while the service is down.
var ErrNotRunning = errors.New("service plugin not running")

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
		Binary:   binary,
		Plugins:  wickplugin.ToolVersionedPlugins,
		Dispense: wickplugin.ToolPluginName,
		Env:      append([]string{wickplugin.EnvToolSocket + "=" + socket}, env...),
		Stderr:   stderr,
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
	return client.Kill, conn, exited, nil
}

// State of a supervised service.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateBackoff  = "backoff" // crashed; waiting to restart
)

// Status is what the admin page shows.
type Status struct {
	State     string    `json:"state"`
	Restarts  int       `json:"restarts"`
	StartedAt time.Time `json:"started_at,omitempty"`
	NextStart time.Time `json:"next_start,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

// Supervisor keeps one service plugin process running: spawn, push config,
// watch for exit, restart with backoff 1s→30s until Stop.
type Supervisor struct {
	key, binary, sockDir string
	spawn                spawnFn
	// env is called before every spawn (callback token, base URL).
	env func() []string
	// cfg returns the service's config, pushed after every spawn.
	cfg  func() map[string]string
	Logs *RingLog

	mu      sync.Mutex
	status  Status
	rt      http.RoundTripper
	kill    func()
	cancel  context.CancelFunc
	wakeNow chan struct{}
}

func newSupervisor(key, binary, sockDir string, spawn spawnFn) *Supervisor {
	return &Supervisor{key: key, binary: binary, sockDir: sockDir, spawn: spawn, Logs: NewRingLog(LogLines),
		status: Status{State: StateStopped}}
}

// Status returns the current status.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Transport returns the round tripper of the running process.
func (s *Supervisor) Transport() (http.RoundTripper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != StateRunning || s.rt == nil {
		return nil, ErrNotRunning
	}
	return s.rt, nil
}

// Start begins supervising (no-op when already started).
func (s *Supervisor) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wakeNow = make(chan struct{}, 1)
	s.status.State = StateStarting
	go s.loop(ctx, s.wakeNow)
}

// Stop kills the process and stops restarting it.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel, kill := s.cancel, s.kill
	s.cancel, s.kill, s.rt = nil, nil, nil
	s.status.State = StateStopped
	s.status.NextStart = time.Time{}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if kill != nil {
		kill()
	}
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
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.Logs.Printf("start failed: %v", err)
		} else {
			select {
			case <-exited:
			case <-ctx.Done():
				return
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
		s.rt, s.kill = nil, nil
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
	s.kill, s.rt = killAll, unixTransport(socket)
	s.status.State = StateRunning
	s.status.StartedAt = time.Now()
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
