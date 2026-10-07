package opencode

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider/cliserver"
	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/pkg/safeexec"
)

// server.go runs one `opencode serve` per instance and hands turns to it.
//
// Measured on the 2 vCPU host: `opencode run` costs 6-6.5 s and 610-790 MB
// per turn; a warm server answers in ~1.7 s and holds ~520-615 MB shared by
// every session on it. The server is started lazily by the first turn,
// reached on 127.0.0.1 with a random port and password, and killed by the
// idle reaper once no turn has used it for the instance's idle window.
//
// Key: instance name + a hash of the server's env. The env carries
// everything that makes two servers differ — XDG dirs (the account), the
// inline config (extra MCP servers, foreign MCP names switched off for the
// workspace), the instance Env. The per-session wick MCP token is NOT in
// it: tokens are minted per spawn and revoked when the spawn exits, so a
// token baked into the server would die after the first turn and would
// serve every later session as whoever came first. Each turn instead
// registers its own token under a per-session MCP name (POST /mcp) and
// the prompt denies every other session's wick tools (see remote.go).

const (
	// DefaultServerIdle is the idle window when the instance sets none.
	DefaultServerIdle = cliserver.DefaultIdle
	// DefaultServerTurns caps turns running at once on one server; the
	// rest queue. Two keeps a server under ~800 MB on the 3.6 GB host.
	DefaultServerTurns = 2

	serverUser     = "opencode"
	serverBootWait = 60 * time.Second
	serverKillWait = 3 * time.Second
)

// serverSpec is what one server is started from.
type serverSpec struct {
	instance string
	bin      string
	env      []string // full env (scrubbed OS env + instance + wick-added)
	dir      string
	idle     time.Duration
	turns    int
	// wrap turns (bin, args) into the argv actually executed (memory
	// scope); nil = run bin directly.
	wrap func(bin string, args []string) (string, []string, string)
}

func (s serverSpec) key() string {
	h := sha256.New()
	env := append([]string(nil), s.env...)
	sort.Strings(env)
	_, _ = io.WriteString(h, s.bin+"\x00")
	for _, e := range env {
		_, _ = io.WriteString(h, e+"\x00")
	}
	return s.instance + "/" + hex.EncodeToString(h.Sum(nil))[:16]
}

// group is the sweep group: one per instance data folder (account).
func (s serverSpec) group() string { return s.instance + "\x00" + s.dir }

// serverHandle is a started server as the manager sees it.
type serverHandle struct {
	url      string
	password string
	pid      int
	scope    string
	kill     func()
	done     <-chan struct{} // closed when the process has exited
}

func (h *serverHandle) Pid() int              { return h.pid }
func (h *serverHandle) Kill()                 { h.kill() }
func (h *serverHandle) Done() <-chan struct{} { return h.done }

type startFunc func(ctx context.Context, spec serverSpec, password string) (*serverHandle, error)

// manager is the shared cliserver.Manager (lazy start, idle reaper, crash
// restart, stale sweep, turn slots) fed with opencode's HTTP servers.
type manager struct {
	*cliserver.Manager[*serverHandle]
	start startFunc
}

func newManager(start startFunc) *manager {
	return &manager{Manager: cliserver.New[*serverHandle]("opencode", DefaultServerTurns), start: start}
}

var servers = newManager(startServe)

// ShutdownServers kills every opencode server. Called on wick shutdown so
// no server outlives the process that supervises it.
func ShutdownServers() { servers.shutdown() }

// leaseServer is the server a lease holds.
type leaseServer struct {
	h *serverHandle
	l *cliserver.Lease[*serverHandle]
}

func (s *leaseServer) dead() bool { return s.l.Dead() }

// lease is one turn's hold on a server: while held the server is never
// reaped. release exactly once.
type lease struct {
	s *leaseServer
}

func (l *lease) release()                           { l.s.l.Release() }
func (l *lease) waitSlot(ctx context.Context) error { return l.s.l.WaitSlot(ctx) }

// acquire returns a lease on the server for spec, starting it when there
// is none (or the previous one died). A failed start is not cached: the
// next turn tries again.
func (m *manager) acquire(ctx context.Context, spec serverSpec) (*lease, error) {
	// Group = instance + data folder: a new key sweeps the stale servers
	// of ITS folder only (config changed), while each account folder of
	// the instance keeps its own server — without the folder, a turn on
	// account a2 marked main's server stale and they killed each other.
	cs := cliserver.Spec{Instance: spec.instance, Group: spec.group(), Key: spec.key(), Idle: spec.idle, Turns: spec.turns}
	cl, err := m.Acquire(ctx, cs, func(ctx context.Context) (*serverHandle, error) {
		return m.start(ctx, spec, randomPassword())
	})
	if err != nil {
		if errors.Is(err, cliserver.ErrShuttingDown) {
			return nil, errors.New("opencode: shutting down")
		}
		return nil, err
	}
	return &lease{s: &leaseServer{h: cl.H, l: cl}}, nil
}

// retire marks every server of instance stale (server mode switched off):
// idle ones die now, busy ones when their last turn ends.
func (m *manager) retire(instance string) { m.Retire(instance) }

// reap kills idle servers; see cliserver.Manager.Reap.
func (m *manager) reap() []string { return m.Reap() }

func (m *manager) shutdown() { m.Shutdown() }

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func randomPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var listenLine = regexp.MustCompile(`listening on (https?://\S+)`)

// serveAttempts bounds how many ports one start tries.
const serveAttempts = 3

// errNotListening: serve exited before it printed "listening" — most
// often the port freePort picked was taken in between. Worth a new port.
var errNotListening = errors.New("opencode serve exited before listening")

// serveOnce is one start attempt; swapped in tests.
var serveOnce = startServeOnce

// startServe starts `opencode serve`, trying a fresh port up to
// serveAttempts times when the server dies before listening, so a port
// race costs a retry inside this start instead of failing the turn.
func startServe(ctx context.Context, spec serverSpec, password string) (*serverHandle, error) {
	var err error
	for i := 0; i < serveAttempts; i++ {
		var h *serverHandle
		h, err = serveOnce(ctx, spec, password)
		if err == nil || !errors.Is(err, errNotListening) || ctx.Err() != nil {
			return h, err
		}
		log.Warn().Err(err).Int("attempt", i+1).Str("instance", spec.instance).Msg("agents.opencode: serve did not listen; retrying on a new port")
	}
	return nil, err
}

// startServeOnce execs `opencode serve` on a kernel-chosen port and waits
// until it answers /global/health.
func startServeOnce(ctx context.Context, spec serverSpec, password string) (*serverHandle, error) {
	// --port 0 is not "any port" to opencode (it falls back to 4096), so
	// the kernel picks one here; the tiny reuse race costs a retry (see
	// startServe).
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
	bin, argv, scope := spec.bin, args, ""
	if spec.wrap != nil {
		bin, argv, scope = spec.wrap(spec.bin, args)
	}
	// Not CommandContext: the server outlives the turn that started it.
	cmd := safeexec.Command(bin, argv...)
	cmd.Dir = spec.dir
	cmd.Env = append(append([]string(nil), spec.env...), "OPENCODE_SERVER_USERNAME="+serverUser, "OPENCODE_SERVER_PASSWORD="+password)
	// The server's own directory; each request names the session's.
	cmd.Env = pinPWD(cmd.Env, spec.dir)
	hideConsole(cmd)
	procgroup.Apply(cmd)
	dieWithParent(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opencode serve: %w", err)
	}
	done := make(chan struct{})
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if !sent {
				if m := listenLine.FindStringSubmatch(line); m != nil {
					urlCh <- m[1]
					sent = true
					continue
				}
			}
			log.Debug().Str("instance", spec.instance).Str("line", line).Msg("agents.opencode: serve")
		}
	}()
	go func() { _ = cmd.Wait(); close(done) }()
	pid := cmd.Process.Pid
	kill := func() { killServer(pid, done) }

	var url string
	select {
	case url = <-urlCh:
	case <-done:
		return nil, errNotListening
	case <-time.After(serverBootWait):
		kill()
		return nil, errors.New("opencode serve did not start listening in time")
	case <-ctx.Done():
		kill()
		return nil, ctx.Err()
	}
	h := &serverHandle{url: strings.TrimRight(url, "/"), password: password, pid: pid, scope: scope, kill: kill, done: done}
	if err := waitHealthy(ctx, h); err != nil {
		kill()
		return nil, err
	}
	return h, nil
}

func waitHealthy(ctx context.Context, h *serverHandle) error {
	c := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(serverBootWait)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/global/health", nil)
		req.SetBasicAuth(serverUser, h.password)
		if resp, err := c.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-h.done:
			return errors.New("opencode serve exited during boot")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("opencode serve not healthy in time")
}

// killServer terminates the server's process group, forcing it after a
// short grace.
func killServer(pid int, done <-chan struct{}) {
	signalServerGroup(pid, false)
	select {
	case <-done:
	case <-time.After(serverKillWait):
	}
	signalServerGroup(pid, true)
}
