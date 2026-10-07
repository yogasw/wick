//go:build livetest

package omp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
)

// TestLiveTwoRPC runs two omp instances side by side on the real binary:
// two wick sessions → two RPC processes, each on its own --profile; B takes
// its login from A (AuthFrom), so ONE auth broker starts for A and B's
// process gets its URL while the token stays out of every log line and the
// recorded env. Both processes and the broker are reaped after a short
// idle window, and a profile with no login or model fails its turn with
// omp's words instead of hanging. The models are a models.yml entry at a
// dead endpoint (no login, no hosted model). Opt-in, inside a
// memory-capped scope:
//
//	systemd-run --user --scope -p MemoryMax=2G env \
//	  WICK_LIVE_OMP_BIN=/path/omp WICK_LIVE_DIR=/scratch \
//	  WICK_LIVE_OMP_NATIVES=/scratch/copy/of/.omp/natives \
//	  go test -tags livetest -run TestLiveTwoRPC -v ./internal/agents/provider/omp/
//
// WICK_LIVE_DIR/home is HOME, so the throwaway profiles live under it.
// omp unpacks ~360 MB of natives on its first run; WICK_LIVE_OMP_NATIVES
// (a scratch copy, never the real ~/.omp) is linked in to skip that.
func TestLiveTwoRPC(t *testing.T) {
	bin, dir := os.Getenv("WICK_LIVE_OMP_BIN"), os.Getenv("WICK_LIVE_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_LIVE_OMP_BIN and WICK_LIVE_DIR")
	}
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("DEAD_KEY", "x")
	t.Setenv("WICK_PORT", "")
	prevHome := homeDir
	homeDir = func() (string, error) { return home, nil }
	if n := os.Getenv("WICK_LIVE_OMP_NATIVES"); n != "" {
		_ = os.MkdirAll(filepath.Join(home, ".omp"), 0o755)
		_ = os.Symlink(n, filepath.Join(home, ".omp", "natives"))
	}

	// Every log line of the run is kept to look for the broker token.
	logs := &lockedBuf{}
	prevLog := log.Logger
	log.Logger = zerolog.New(io.MultiWriter(logs, os.Stderr)).With().Timestamp().Logger()

	prevRPC, prevBrokers, prevStart := rpcServers, brokers, startBrokerFn
	rpcServers = newRPCManager()
	rpcServers.Every = time.Second
	brokers = cliserver.New[*brokerHandle]("omp-broker", 1)
	brokers.Every = time.Second
	var bmu sync.Mutex
	var started []*brokerHandle
	startBrokerFn = func(ctx context.Context, spec brokerSpec, key string) (*brokerHandle, error) {
		h, err := startBroker(ctx, spec, key)
		if h != nil {
			bmu.Lock()
			started = append(started, h)
			bmu.Unlock()
		}
		return h, err
	}
	t.Cleanup(func() {
		rpcServers.Shutdown()
		brokers.Shutdown()
		rpcServers, brokers, startBrokerFn = prevRPC, prevBrokers, prevStart
		homeDir = prevHome
		log.Logger = prevLog
	})

	models := "providers:\n  dead:\n    baseUrl: http://127.0.0.1:9/v1\n    apiKey: DEAD_KEY\n    api: openai-completions\n" +
		"    models:\n      - id: dead-model\n        name: Dead\n        api: openai-completions\n        reasoning: false\n        input: [text]\n        contextWindow: 8000\n        maxTokens: 256\n"
	a := provider.Instance{Type: provider.TypeOMP, Name: "livetest-a", OMPConfig: &provider.OMPConfig{Profile: "wick-livetest-a"}}
	b := provider.Instance{Type: provider.TypeOMP, Name: "livetest-b", AuthFrom: a.Name, OMPConfig: &provider.OMPConfig{Profile: "wick-livetest-b"}}
	for _, ins := range []provider.Instance{a, b} {
		agentDir := profileAgentDir(home, "", provider.OMPProfile(ins))
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "models.yml"), []byte(models), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	restore := provider.SwapAuthInstanceLookup(func(typ provider.Type, name string) (provider.Instance, error) {
		if typ == a.Type && name == a.Name {
			return a, nil
		}
		return provider.Instance{}, os.ErrNotExist
	})
	t.Cleanup(restore)

	const idle = 5 * time.Second
	ws := filepath.Join(dir, "ws")
	_ = os.MkdirAll(ws, 0o755)
	// One turn: boots (or reuses) the session's process, waits for the
	// run to start, then aborts it — the dead endpoint never answers.
	turn := func(ins provider.Instance, msg string) (provider.Process, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{
			SessionID: "live-" + ins.Name, Workspace: ws, SessionDir: filepath.Join(dir, "sess-"+ins.Name),
			InitialMessage: msg, ModelID: "dead/dead-model", Instance: &ins, IdleTimeout: idle})
		if err != nil {
			return nil, err
		}
		if err := waitFor(bufio.NewReader(p.Stdout()), `"type":"agent_start"`, 90*time.Second); err != nil {
			_ = p.Kill()
			return p, err
		}
		_ = p.Kill()
		if err := p.Wait(); !errors.Is(err, context.Canceled) {
			return p, errors.New("aborted turn Wait = " + errString(err))
		}
		return p, nil
	}

	var wg sync.WaitGroup
	procs := map[string]provider.Process{}
	var pmu sync.Mutex
	start := time.Now()
	for _, ins := range []provider.Instance{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := turn(ins, "hello")
			t.Logf("%s: first turn in %s err=%v", ins.Name, time.Since(start).Round(time.Millisecond), err)
			if err != nil {
				t.Errorf("%s: %v", ins.Name, err)
				return
			}
			pmu.Lock()
			procs[ins.Name] = p
			pmu.Unlock()
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	// Two sessions → two processes, each on its own profile.
	keys := rpcServers.Keys()
	if len(keys) != 2 {
		t.Fatalf("manager holds %d RPC processes, want 2: %v", len(keys), keys)
	}
	pids := map[string]int{}
	for _, k := range keys {
		c, _ := rpcServers.Live(k)
		name := strings.SplitN(k, "/", 2)[0]
		pids[name] = c.Pid()
		argv := procCmdline(c.Pid())
		t.Logf("%s: rpc pid %d rss=%dMB argv has --profile %s", name, c.Pid(), procRSS(c.Pid()), argAfter(argv, "--profile"))
		want := "wick-" + name
		if got := argAfter(argv, "--profile"); got != want {
			t.Errorf("%s: --profile %q, want %q", name, got, want)
		}
	}
	if pids["livetest-a"] == 0 || pids["livetest-a"] == pids["livetest-b"] {
		t.Fatalf("the two sessions share a process: %v", pids)
	}

	// B shares A's login: one broker, for A, and only B's env points at it.
	bmu.Lock()
	bs := append([]*brokerHandle(nil), started...)
	bmu.Unlock()
	if len(bs) != 1 || brokers.Len() != 1 {
		t.Fatalf("brokers started %d, held %d; want 1 and 1", len(bs), brokers.Len())
	}
	br := bs[0]
	if argv := procCmdline(br.pid); argAfter(argv, "--profile") != "wick-livetest-a" || !strings.Contains(strings.Join(argv, " "), "auth-broker serve") {
		t.Errorf("broker argv %q is not A's auth-broker serve", argv)
	}
	t.Logf("broker pid %d url %s rss=%dMB (token %d chars, not shown)", br.pid, br.url, procRSS(br.pid), len(br.token))
	envA, envB := procEnv(t, pids["livetest-a"]), procEnv(t, pids["livetest-b"])
	if envB[envBrokerURL] != br.url {
		t.Errorf("B's %s=%q, want the broker's %s", envBrokerURL, envB[envBrokerURL], br.url)
	}
	if envB[envBrokerToken] != br.token {
		t.Error("B's process does not carry the broker token")
	}
	if envA[envBrokerURL] != "" || envA[envBrokerToken] != "" {
		t.Error("A (the owner) got broker env")
	}
	for name, p := range procs {
		for _, kv := range p.Env() {
			if strings.Contains(kv, br.token) || strings.HasPrefix(kv, envBrokerToken+"=") {
				t.Errorf("%s: recorded spawn env holds the broker token (%s=…)", name, strings.SplitN(kv, "=", 2)[0])
			}
		}
	}

	// A second turn of B reuses its process and the same broker.
	if _, err := turn(b, "again"); err != nil {
		t.Fatalf("second turn of B: %v", err)
	}
	if c, ok := rpcServers.Live(keyOf(t, "livetest-b")); !ok || c.Pid() != pids["livetest-b"] {
		t.Error("B's second turn started another process")
	}
	bmu.Lock()
	nb := len(started)
	bmu.Unlock()
	if nb != 1 {
		t.Errorf("B's second turn started another broker (%d)", nb)
	}

	// No login and no model: the turn fails with omp's words, no hang.
	bad := provider.Instance{Type: provider.TypeOMP, Name: "livetest-nologin", OMPConfig: &provider.OMPConfig{Profile: "wick-livetest-nologin"}}
	t0 := time.Now()
	pb, err := Spawner{Binary: bin}.Spawn(context.Background(), provider.SpawnOptions{
		SessionID: "live-nologin", Workspace: ws, SessionDir: filepath.Join(dir, "sess-nologin"),
		InitialMessage: "x", Instance: &bad, IdleTimeout: idle})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(pb.Stdout())
	werr := pb.Wait()
	t.Logf("unauthenticated turn: %s wait_err=%v error=%q", time.Since(t0).Round(time.Millisecond), werr, errorText(out))
	if werr != nil || !bytes.Contains(out, []byte(`"stopReason":"error"`)) {
		t.Errorf("unauthenticated turn did not end in a clean error: wait=%v out=%s", werr, out)
	}
	if time.Since(t0) > 90*time.Second {
		t.Errorf("unauthenticated turn took %s — hang", time.Since(t0))
	}

	// Reaped once idle: the reaper ticks every second, the window is 5s.
	idleFrom := time.Now()
	deadline := idleFrom.Add(idle + 20*time.Second)
	for (rpcServers.Len() > 0 || brokers.Len() > 0) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	if rpcServers.Len() != 0 || brokers.Len() != 0 {
		t.Fatalf("after the idle window: %d RPC processes, %d brokers still held", rpcServers.Len(), brokers.Len())
	}
	t.Logf("manager empty %s after the last turn ended", time.Since(idleFrom).Round(100*time.Millisecond))
	for name, pid := range pids {
		if !waitGone(pid, 10*time.Second) {
			t.Errorf("%s: pid %d still running after reap", name, pid)
		}
	}
	select {
	case <-br.done:
	case <-time.After(10 * time.Second):
		t.Errorf("broker pid %d still running after reap", br.pid)
	}
	t.Logf("both RPC processes and the broker exited %s after the last turn", time.Since(idleFrom).Round(100*time.Millisecond))

	if n := strings.Count(logs.String(), "auth broker started"); n != 1 {
		t.Errorf("%d 'auth broker started' log lines, want 1", n)
	}
	if strings.Contains(logs.String(), br.token) {
		t.Error("the broker token appears in the logs")
	}
	t.Logf("log lines scanned: %d bytes, broker token absent", logs.Len())
}

func keyOf(t *testing.T, instance string) string {
	t.Helper()
	for _, k := range rpcServers.Keys() {
		if strings.HasPrefix(k, instance+"/") {
			return k
		}
	}
	return ""
}

func waitFor(r *bufio.Reader, want string, d time.Duration) error {
	type res struct{ err error }
	ch := make(chan res, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if strings.Contains(line, want) {
				ch <- res{}
				return
			}
			if err != nil {
				ch <- res{errors.New("stream ended before " + want + ": " + errString(err))}
				return
			}
		}
	}()
	select {
	case v := <-ch:
		return v.err
	case <-time.After(d):
		return errors.New("no " + want + " in time")
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// errorText is the errorMessage of the stream's error line, for the log.
func errorText(out []byte) string {
	for _, ln := range strings.Split(string(out), "\n") {
		if _, v, ok := strings.Cut(ln, `"errorMessage":"`); ok {
			if i := strings.Index(v, `"`); i >= 0 {
				return v[:i]
			}
		}
	}
	return ""
}

func procCmdline(pid int) []string {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

func argAfter(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// procEnv returns only the OMP_* entries of pid's environment — the rest
// is never read out.
func procEnv(t *testing.T, pid int) map[string]string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Fatalf("read environ of %d: %v", pid, err)
	}
	out := map[string]string{}
	for _, kv := range strings.Split(string(b), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "OMP_") {
			out[k] = v
		}
	}
	return out
}

func procRSS(pid int) int {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if _, v, ok := strings.Cut(string(b), "VmRSS:"); ok {
		n, _ := strconv.Atoi(strings.Fields(v)[0])
		return n / 1024
	}
	return -1
}

func waitGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		// Gone, or a zombie waiting to be reaped.
		if err != nil || strings.Contains(string(b), ") Z ") {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuf) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
}
