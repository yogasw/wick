package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// DefaultHold bounds how long a request waits for a cold plugin to finish
// its handshake before the host answers 503.
const DefaultHold = 10 * time.Second

// ErrNotReady is returned by Acquire when the plugin did not come up within
// the hold window (or failed to start).
var ErrNotReady = errors.New("tool plugin not ready")

// spawnFn starts one tool plugin process serving HTTP on socket. alive
// reports false once the process has exited.
type spawnFn func(binary, socket string) (kill func(), conn wickplugin.ToolConn, alive func() bool, err error)

func spawnProcess(binary, socket string) (func(), wickplugin.ToolConn, func() bool, error) {
	client, raw, err := connplugin.Spawn(connplugin.SpawnSpec{
		Binary:   binary,
		Plugins:  wickplugin.ToolVersionedPlugins,
		Dispense: wickplugin.ToolPluginName,
		Env:      []string{wickplugin.EnvToolSocket + "=" + socket},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	conn, ok := raw.(wickplugin.ToolConn)
	if !ok {
		client.Kill()
		return nil, nil, nil, fmt.Errorf("plugin %s is not a tool plugin", binary)
	}
	return client.Kill, conn, func() bool { return !client.Exited() }, nil
}

// proc is one running plugin process.
type proc struct {
	kill    func()
	conn    wickplugin.ToolConn
	alive   func() bool
	socket  string
	rt      http.RoundTripper
	cfgHash string
}

// Runner owns the lifecycle of one tool plugin: at most one process, spawned
// on first use (singleflight, so N concurrent cold requests cause one spawn),
// idle-killed by the pool sweeper unless keep_warm or a request (including a
// long-lived SSE/websocket one) is still in flight.
type Runner struct {
	key, binary, sockDir string
	keepWarm             bool
	hold                 time.Duration
	spawn                spawnFn
	now                  func() time.Time

	mu       sync.Mutex
	proc     *proc
	sf       singleflight.Group
	inflight atomic.Int64
	lastUsed atomic.Int64 // unix nanos
	spawns   atomic.Int64
}

func newRunner(key, binary, sockDir string, keepWarm bool, spawn spawnFn) *Runner {
	return &Runner{key: key, binary: binary, sockDir: sockDir, keepWarm: keepWarm,
		hold: DefaultHold, spawn: spawn, now: time.Now}
}

// Inflight is the number of requests currently being proxied.
func (r *Runner) Inflight() int64 { return r.inflight.Load() }

// Running reports whether a live process is attached.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proc != nil && r.proc.alive()
}

// Acquire returns a transport to the running plugin (spawning it if needed)
// with cfg pushed, and a release func the caller must call when the request
// — including any streamed body — is finished. It waits at most the hold
// window for a cold start; the spawn keeps going in the background so the
// next request finds it ready.
func (r *Runner) Acquire(ctx context.Context, cfg map[string]string) (http.RoundTripper, func(), error) {
	r.inflight.Add(1)
	r.touch()
	release := func() { r.touch(); r.inflight.Add(-1) }
	p, err := r.ensure(ctx)
	if err != nil {
		release()
		return nil, nil, err
	}
	if h := hashCfg(cfg); h != p.cfgHash {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := p.conn.Configure(cctx, cfg)
		cancel()
		if err != nil {
			release()
			return nil, nil, fmt.Errorf("%w: configure: %v", ErrNotReady, err)
		}
		r.mu.Lock()
		p.cfgHash = h
		r.mu.Unlock()
	}
	return p.rt, release, nil
}

func (r *Runner) touch() { r.lastUsed.Store(r.now().UnixNano()) }

func (r *Runner) current() *proc {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil && !r.proc.alive() {
		r.proc.kill()
		r.proc = nil
	}
	return r.proc
}

func (r *Runner) ensure(ctx context.Context) (*proc, error) {
	if p := r.current(); p != nil {
		return p, nil
	}
	ch := r.sf.DoChan("spawn", func() (any, error) {
		if p := r.current(); p != nil {
			return p, nil
		}
		return r.start()
	})
	t := time.NewTimer(r.hold)
	defer t.Stop()
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotReady, res.Err)
		}
		return res.Val.(*proc), nil
	case <-t.C:
		return nil, fmt.Errorf("%w: still starting after %s", ErrNotReady, r.hold)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runner) start() (*proc, error) {
	_ = os.MkdirAll(r.sockDir, 0o700)
	socket := filepath.Join(r.sockDir, fmt.Sprintf("tool-%s-%d.sock", r.key, time.Now().UnixNano()))
	r.spawns.Add(1)
	kill, conn, alive, err := r.spawn(r.binary, socket)
	if err != nil {
		return nil, err
	}
	p := &proc{kill: func() { kill(); _ = os.Remove(socket) }, conn: conn, alive: alive, socket: socket,
		rt: unixTransport(socket)}
	r.mu.Lock()
	r.proc = p
	r.mu.Unlock()
	r.touch()
	return p, nil
}

// Warm spawns the process now (keep_warm tools at boot). Errors are returned
// for logging; the next request retries.
func (r *Runner) Warm() error {
	_, err := r.ensure(context.Background())
	return err
}

// sweep kills the process when it has been idle past ttl. It never kills a
// keep_warm tool or one with a request in flight.
func (r *Runner) sweep(ttl time.Duration) bool {
	if r.keepWarm || r.inflight.Load() > 0 {
		return false
	}
	if r.now().Sub(time.Unix(0, r.lastUsed.Load())) < ttl {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc == nil || r.inflight.Load() > 0 {
		return false
	}
	r.proc.kill()
	r.proc = nil
	return true
}

// Stop kills the process unconditionally.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		r.proc.kill()
		r.proc = nil
	}
}

func unixTransport(socket string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:        16,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 16,
	}
}

func hashCfg(cfg map[string]string) string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\x00", k, cfg[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
