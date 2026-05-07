// Package processctl owns the in-process lifecycle of the HTTP server
// and background worker: Start/Stop/IsRunning + a per-process
// IsManaged flag that says whether wick was launched via the system
// tray (and is therefore allowed to mutate its own server/worker
// state).
//
// Two consumers:
//
//   - internal/systemtray — calls Start/Stop on menu clicks and
//     subscribes to OnStateChange so tray UI redraws when state flips.
//   - internal/connectors/wickmanager — exposes Start/Stop/Status as
//     MCP ops gated by IsManaged so an LLM can only manage server/
//     worker when wick is actually the tray-resident process. `wick
//     server` / `wick worker` / headless modes leave IsManaged=false
//     so the ops return "system management unavailable in this run
//     mode".
//
// Implementation lives in headless-build-tagged files so the headless
// build (no tray) compiles a stub that always reports IsManaged=false
// and refuses Start/Stop. Tray boot calls SetManaged(true) once.
package processctl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Runner is the contract a server / worker must satisfy. Both
// internal/pkg/api.Server and internal/pkg/worker.Server already
// implement it via their existing Run(ctx) signatures (api.Server.Run
// also takes a port; the tray wraps it before plugging in here).
//
// processctl owns the goroutine + cancel handle and never calls
// NewServer itself — that keeps internal/pkg/api and internal/pkg/
// worker free to import processctl when they need IsManaged without
// dragging the whole runtime through an import cycle.
type Runner interface {
	Run(ctx context.Context) error
}

// RunnerFunc adapts a closure to Runner. Tray uses it to wrap
// api.NewServer().Run with the configured port:
//
//	processctl.SetServerRunner(processctl.RunnerFunc(func(ctx context.Context) error {
//	    return api.NewServer().Run(ctx, processctl.ServerPort())
//	}))
type RunnerFunc func(ctx context.Context) error

func (f RunnerFunc) Run(ctx context.Context) error { return f(ctx) }

// ErrAlreadyRunning is returned by Server.Start / Worker.Start when the
// component is already up. Callers check via errors.Is so wickmanager
// can map it to a stable error string for the LLM.
var ErrAlreadyRunning = errors.New("already running")

// ErrNotManaged is returned by every state-mutating method when the
// process was not launched via the tray. wick server / wick worker /
// headless never satisfy IsManaged.
var ErrNotManaged = errors.New("system management unavailable in this run mode (start wick via the tray)")

// StateChange is the event payload sent on every state transition. The
// tray subscribes to refresh its menu icon + labels; wickmanager does
// not subscribe (it polls IsRunning at op-call time).
type StateChange struct {
	ServerRunning bool
	ServerPort    int
	WorkerRunning bool
}

var (
	mu sync.Mutex

	managed bool

	serverCancel context.CancelFunc
	serverDone   chan struct{}
	serverPort   int
	serverLogger zerolog.Logger

	workerCancel context.CancelFunc
	workerDone   chan struct{}
	workerLogger zerolog.Logger

	mcpLogger    zerolog.Logger
	mcpLoggerSet bool

	serverRunner Runner
	workerRunner Runner

	listeners []func(StateChange)
)

// SetServerRunner installs the runner StartServer launches. Tray
// wires this to api.NewServer with its port closed-over; non-tray
// callers (wick server / wick worker / headless) never call it.
func SetServerRunner(r Runner) {
	mu.Lock()
	serverRunner = r
	mu.Unlock()
}

// SetWorkerRunner installs the runner StartWorker launches. Tray
// wires this to worker.NewServer.
func SetWorkerRunner(r Runner) {
	mu.Lock()
	workerRunner = r
	mu.Unlock()
}

// SetManaged marks the current process as the tray-resident one.
// Called once from systemtray.Run before any Start/Stop is reachable.
// Other entry points (wick server / wick worker / headless) never call
// this, so IsManaged stays false there.
func SetManaged(v bool) {
	mu.Lock()
	managed = v
	mu.Unlock()
}

// IsManaged reports whether the current process is the tray-resident
// one. wickmanager's tray-only ops gate on this.
func IsManaged() bool {
	mu.Lock()
	defer mu.Unlock()
	return managed
}

// SetServerLogger configures the zerolog.Logger threaded through every
// future server.Run context. Tray calls this with its server-log file
// writer; wickmanager-only callers (no tray) leave it at the default.
func SetServerLogger(l zerolog.Logger) {
	mu.Lock()
	serverLogger = l
	mu.Unlock()
}

// SetWorkerLogger is the worker-side counterpart to SetServerLogger.
func SetWorkerLogger(l zerolog.Logger) {
	mu.Lock()
	workerLogger = l
	mu.Unlock()
}

// SetMCPLogger configures the destination for the wickmanager
// connector's audit log (~/.<appName>/logs/mcp-YYYY-MM-DD.log). Tray
// calls this after opening the log file; non-tray callers (wick
// server / headless) leave it at zero, in which case wickmanager
// falls back to the global zerolog logger.
func SetMCPLogger(l zerolog.Logger) {
	mu.Lock()
	mcpLogger = l
	mcpLoggerSet = true
	mu.Unlock()
}

// MCPLogger returns the wickmanager audit logger configured via
// SetMCPLogger. The bool is false when no logger was set; callers
// then default to log.Logger / zerolog.Ctx(ctx).
func MCPLogger() (zerolog.Logger, bool) {
	mu.Lock()
	defer mu.Unlock()
	return mcpLogger, mcpLoggerSet
}

// SetPort configures the HTTP listen port StartServer will bind. Tray
// resolves config.Load().App.Port at boot and passes it in here.
func SetPort(port int) {
	mu.Lock()
	serverPort = port
	mu.Unlock()
}

// Subscribe registers a callback fired after every Start/Stop. Used
// by the tray to refresh its icon + menu labels — non-tray callers
// don't subscribe. Returned func unsubscribes.
func Subscribe(fn func(StateChange)) func() {
	mu.Lock()
	listeners = append(listeners, fn)
	idx := len(listeners) - 1
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if idx < len(listeners) {
			listeners[idx] = nil
		}
	}
}

func snapshot() StateChange {
	return StateChange{
		ServerRunning: serverCancel != nil,
		ServerPort:    serverPort,
		WorkerRunning: workerCancel != nil,
	}
}

func notifyLocked() {
	s := snapshot()
	for _, fn := range listeners {
		if fn != nil {
			go fn(s)
		}
	}
}

// IsServerRunning reports whether the server goroutine is up.
func IsServerRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return serverCancel != nil
}

// IsWorkerRunning reports whether the worker goroutine is up.
func IsWorkerRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return workerCancel != nil
}

// ServerPort returns the configured listen port. 0 before SetPort.
func ServerPort() int {
	mu.Lock()
	defer mu.Unlock()
	return serverPort
}

// StartServer launches the HTTP server in this process. Returns
// ErrAlreadyRunning when one is already up, ErrNotManaged when the
// process is not tray-resident, or a wrapped bind error when the
// pre-flight listener check fails.
func StartServer() error {
	mu.Lock()
	if !managed {
		mu.Unlock()
		return ErrNotManaged
	}
	if serverCancel != nil {
		mu.Unlock()
		return ErrAlreadyRunning
	}
	port := serverPort
	logger := serverLogger
	srv := serverRunner
	mu.Unlock()

	if srv == nil {
		return errors.New("server runner not configured (call processctl.SetServerRunner)")
	}

	// Pre-flight bind check so port collisions surface synchronously
	// to the caller (tray menu / MCP op response). Tiny race window
	// between closing this listener and the runner binding the same
	// port — acceptable for UX feedback.
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("port %d in use: %w", port, err)
	}
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = logger.WithContext(ctx)

	mu.Lock()
	serverCancel = cancel
	serverDone = make(chan struct{})
	mu.Unlock()

	go func() {
		mu.Lock()
		done := serverDone
		mu.Unlock()
		defer close(done)

		if err := srv.Run(ctx); err != nil {
			log.Error().Err(err).Msg("server")
		}
		mu.Lock()
		serverCancel = nil
		mu.Unlock()
		mu.Lock()
		notifyLocked()
		mu.Unlock()
	}()
	mu.Lock()
	notifyLocked()
	mu.Unlock()
	return nil
}

// StopServer cancels the server goroutine and waits for it to drain.
// No-op when nothing is running. Always reports nil — used by tray
// menu and wickmanager system_server_stop.
func StopServer() error {
	mu.Lock()
	if !managed {
		mu.Unlock()
		return ErrNotManaged
	}
	cancel := serverCancel
	done := serverDone
	mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	if done != nil {
		<-done
	}
	return nil
}

// StartWorker launches the background worker in this process. Returns
// ErrAlreadyRunning / ErrNotManaged the same way StartServer does.
func StartWorker() error {
	mu.Lock()
	if !managed {
		mu.Unlock()
		return ErrNotManaged
	}
	if workerCancel != nil {
		mu.Unlock()
		return ErrAlreadyRunning
	}
	logger := workerLogger
	srv := workerRunner
	mu.Unlock()

	if srv == nil {
		return errors.New("worker runner not configured (call processctl.SetWorkerRunner)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = logger.WithContext(ctx)

	mu.Lock()
	workerCancel = cancel
	workerDone = make(chan struct{})
	mu.Unlock()

	go func() {
		mu.Lock()
		done := workerDone
		mu.Unlock()
		defer close(done)

		if err := srv.Run(ctx); err != nil {
			log.Error().Err(err).Msg("worker")
		}
		mu.Lock()
		workerCancel = nil
		mu.Unlock()
		mu.Lock()
		notifyLocked()
		mu.Unlock()
	}()
	mu.Lock()
	notifyLocked()
	mu.Unlock()
	return nil
}

// StopWorker cancels the worker goroutine and waits for it to drain.
// No-op when nothing is running.
func StopWorker() error {
	mu.Lock()
	if !managed {
		mu.Unlock()
		return ErrNotManaged
	}
	cancel := workerCancel
	done := workerDone
	mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	if done != nil {
		<-done
	}
	return nil
}

// StopAll halts both. Used by tray Quit and the updater's apply-then-
// restart path. Ignores ErrNotManaged so non-tray callers (which
// shouldn't reach here) get a no-op.
func StopAll() {
	_ = StopServer()
	_ = StopWorker()
}
