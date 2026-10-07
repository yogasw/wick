package omp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/pkg/safeexec"
)

// rpc.go keeps one `omp --mode rpc --no-ui` per wick session and speaks its
// stdio protocol (oh-my-pi src/modes/rpc: rpc-types.ts, rpc-mode.ts,
// rpc-client.ts). Every stdout line is one JSON frame:
//
//	{"type":"ready",...}                        once, when commands are accepted
//	{"type":"response","id":..,"success":..}    answer to the command with that id
//	{"type":"prompt_result","id":..,"status":..} a prompt's work settled (completed|aborted|error)
//	{"type":"session_settled"}                  nothing left that could wake the session
//	anything else                               an AgentSessionEvent (same objects `-p --mode json` prints)
//
// Protocol v1 is kept (no negotiate_protocol): v2 splits frames over 1 MiB
// into rpc_chunk frames, v1 shrinks them instead, which is what print mode
// does to its stream too.
//
// One process per wick SESSION, not per instance: the process holds exactly
// one active agent session, and its events carry no session id — two wick
// sessions sharing a process could not tell their frames apart, and
// switch_session detaches a run that is still going (rpc-mode.ts
// "The detached run publishes no terminal agent_end"). The cwd, the soul
// (--append-system-prompt) and the MCP token are per session as well, so
// the process key would split per session anyway.

const (
	rpcBootWait = 60 * time.Second
	rpcKillWait = 3 * time.Second
)

// rpcCompactWait bounds the compact command: omp answers only once the
// summary is written, minutes on a big session — under rpcCallWait wick
// reported a timeout for a compaction omp went on to finish.
var rpcCompactWait = 10 * time.Minute

// rpcCallWait bounds every other command's response (var: shortened in
// tests).
var rpcCallWait = 30 * time.Second

// rpcFrame is the envelope fields wick routes on.
type rpcFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	// Error is a string on a failed response, an object
	// ({message,...}) on prompt_result — see errText.
	Error json.RawMessage `json:"error"`
	Data  json.RawMessage `json:"data"`
	// prompt_result
	AgentInvoked   bool   `json:"agentInvoked"`
	Status         string `json:"status"`
	SessionSettled bool   `json:"sessionSettled"`
	// command_output (a slash command's text)
	Text string `json:"text"`
}

// errText is the frame's error as text, whichever shape it came in.
func (f rpcFrame) errText() string {
	if len(f.Error) == 0 || string(f.Error) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(f.Error, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(f.Error, &o) == nil && o.Message != "" {
		return o.Message
	}
	return string(f.Error)
}

// rpcConn is one live omp RPC process; it is the cliserver.Handle.
type rpcConn struct {
	pid   int
	scope string
	done  chan struct{}
	kill  func()
	// token is the wick MCP credential baked into this process's env.
	token string

	wmu sync.Mutex
	w   io.WriteCloser

	mu sync.Mutex
	// pinned is "<omp session id>#<account>" once `/session pin` took.
	pinned  string
	nextID  int
	pending map[string]chan rpcFrame
	sub     func(line []byte, f rpcFrame)
	// sessionID is the omp session the process has open (from get_state).
	sessionID string
	turns     int
}

func (c *rpcConn) Pid() int              { return c.pid }
func (c *rpcConn) Kill()                 { c.kill() }
func (c *rpcConn) Done() <-chan struct{} { return c.done }

func (c *rpcConn) dead() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// subscribe routes every non-response frame to fn until the returned
// func is called. One subscriber at a time: a process runs one turn.
func (c *rpcConn) subscribe(fn func(line []byte, f rpcFrame)) func() {
	c.mu.Lock()
	c.sub = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.sub = nil
		c.mu.Unlock()
	}
}

// send writes one command and returns its id without waiting.
func (c *rpcConn) send(cmd map[string]any) (string, chan rpcFrame, error) {
	c.mu.Lock()
	c.nextID++
	id := "wick-" + strconv.Itoa(c.nextID)
	ch := make(chan rpcFrame, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	cmd["id"] = id
	b, err := json.Marshal(cmd)
	if err != nil {
		c.forget(id)
		return "", nil, err
	}
	c.wmu.Lock()
	_, err = c.w.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return "", nil, fmt.Errorf("omp rpc write: %w", err)
	}
	return id, ch, nil
}

func (c *rpcConn) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// call sends cmd and waits for its response; a failed response is an error.
func (c *rpcConn) call(ctx context.Context, cmd map[string]any) (rpcFrame, error) {
	return c.callWait(ctx, cmd, rpcCallWait)
}

// callWait is call with its own response deadline.
func (c *rpcConn) callWait(ctx context.Context, cmd map[string]any, wait time.Duration) (rpcFrame, error) {
	typ, _ := cmd["type"].(string)
	id, ch, err := c.send(cmd)
	if err != nil {
		return rpcFrame{}, err
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case f := <-ch:
		if !f.Success {
			return f, fmt.Errorf("omp rpc %s: %s", typ, f.errText())
		}
		return f, nil
	case <-c.done:
		c.forget(id)
		return rpcFrame{}, errors.New("omp rpc process exited")
	case <-t.C:
		c.forget(id)
		return rpcFrame{}, fmt.Errorf("omp rpc %s: no response in %s", typ, wait)
	case <-ctx.Done():
		c.forget(id)
		return rpcFrame{}, ctx.Err()
	}
}

// readLoop dispatches stdout frames until the process closes it.
func (c *rpcConn) readLoop(r io.Reader, ready chan<- struct{}) {
	sc := bufio.NewScanner(r)
	// v1 frames are at most 1 MiB (rpc-frame.ts MAX_RPC_FRAME_BYTES).
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	readySent := false
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var f rpcFrame
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &f) != nil {
			log.Debug().Str("line", string(line)).Msg("agents.omp: rpc stdout")
			continue
		}
		switch {
		case f.Type == "ready":
			if !readySent {
				readySent = true
				close(ready)
			}
			continue
		case f.Type == "response" && f.ID != "":
			c.mu.Lock()
			ch := c.pending[f.ID]
			delete(c.pending, f.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- f
				continue
			}
		}
		c.mu.Lock()
		fn := c.sub
		c.mu.Unlock()
		if fn != nil {
			fn(line, f)
		}
	}
}

// rpcSpec is what one RPC process is started from.
type rpcSpec struct {
	bin   string
	args  []string // full argv (profile, --mode rpc, isolation, cwd, soul, model, resume)
	env   []string
	dir   string
	token string
	wrap  func(bin string, args []string) (string, []string, string)
}

// startFn starts an RPC process; swapped in tests.
type startFn func(ctx context.Context, spec rpcSpec) (*rpcConn, error)

// startRPC execs omp in RPC mode and waits for its ready frame.
func startRPC(ctx context.Context, spec rpcSpec) (*rpcConn, error) {
	bin, argv, scope := spec.bin, spec.args, ""
	if spec.wrap != nil {
		bin, argv, scope = spec.wrap(spec.bin, spec.args)
	}
	// Not CommandContext: the process outlives the turn that started it.
	cmd := safeexec.Command(bin, argv...)
	cmd.Dir = spec.dir
	cmd.Env = spec.env
	hideConsole(cmd)
	procgroup.Apply(cmd)
	dieWithParent(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start omp rpc: %w", err)
	}
	c := &rpcConn{pid: cmd.Process.Pid, scope: scope, done: make(chan struct{}), w: stdin,
		token: spec.token, pending: map[string]chan rpcFrame{}}
	ready := make(chan struct{})
	// tail keeps omp's last stderr lines: a process that dies before ready
	// (no model / not logged in) says why only there.
	tail := &lineTail{max: 8}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); c.readLoop(stdout, ready) }()
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			log.Debug().Int("pid", c.pid).Str("line", line).Msg("agents.omp: rpc stderr")
			if strings.TrimSpace(line) != "" {
				tail.add(line)
			}
		}
	}()
	go func() { readers.Wait(); _ = cmd.Wait(); close(c.done) }()
	pid := c.pid
	c.kill = func() {
		_ = stdin.Close()
		signalGroup(pid, false)
		select {
		case <-c.done:
		case <-time.After(rpcKillWait):
		}
		signalGroup(pid, true)
	}
	select {
	case <-ready:
		return c, nil
	case <-c.done:
		if msg := tail.String(); msg != "" {
			return nil, fmt.Errorf("omp rpc exited before ready: %s", msg)
		}
		return nil, errors.New("omp rpc exited before ready")
	case <-time.After(rpcBootWait):
		c.kill()
		return nil, errors.New("omp rpc not ready in time")
	case <-ctx.Done():
		c.kill()
		return nil, ctx.Err()
	}
}

// lineTail is a bounded ring of the last lines written to it.
type lineTail struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, " ")
}
