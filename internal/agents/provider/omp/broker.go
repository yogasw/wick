package omp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// broker.go runs omp's own auth broker for an instance whose login other
// instances use (provider.Instance.AuthFrom, see provider/authshare.go).
//
// `omp --profile <owner> auth-broker serve --bind=127.0.0.1:<port>` serves
// the owner profile's credential store; `auth-broker token --json` prints
// the bearer token it checks (kept in the owner profile, created on first
// use). A sharer's spawn gets OMP_AUTH_BROKER_URL + OMP_AUTH_BROKER_TOKEN
// and keeps its own --profile: omp then reads (and the broker refreshes)
// the owner's credentials.
//
// The broker is a cliserver like the RPC processes: started lazily by the
// first sharer turn, held by a lease for that turn, killed by the idle
// reaper once no turn used it for the sharer's idle window, restarted on
// death. It rebinds its previous port when it can, so a live sharer RPC
// process (whose key hashes the URL) survives a broker restart.
//
// The token is a credential: it never reaches a log line, the spawn's
// recorded env or the UI.

const (
	envBrokerURL   = "OMP_AUTH_BROKER_URL"
	envBrokerToken = "OMP_AUTH_BROKER_TOKEN"

	brokerBootWait = 60 * time.Second
)

// brokerSpec is what one broker is started from.
type brokerSpec struct {
	owner   string // owner instance name
	bin     string
	profile string
	env     []string
	idle    time.Duration
	wrap    func(bin string, args []string) (string, []string, string)
}

func (s brokerSpec) key() string {
	h := sha256.New()
	env := append([]string(nil), s.env...)
	sort.Strings(env)
	_, _ = io.WriteString(h, s.bin+"\x00"+s.profile+"\x00")
	for _, e := range env {
		_, _ = io.WriteString(h, e+"\x00")
	}
	return s.owner + "/broker/" + hex.EncodeToString(h.Sum(nil))[:16]
}

// brokerHandle is a started broker as the manager sees it.
type brokerHandle struct {
	url   string
	token string
	pid   int
	kill  func()
	done  <-chan struct{}
}

func (h *brokerHandle) Pid() int              { return h.pid }
func (h *brokerHandle) Kill()                 { h.kill() }
func (h *brokerHandle) Done() <-chan struct{} { return h.done }

var (
	brokers = cliserver.New[*brokerHandle]("omp-broker", 1)

	// startBrokerFn starts a broker; swapped in tests.
	startBrokerFn = startBroker

	// lastPort remembers each broker key's port so a restart rebinds it.
	lastPortMu sync.Mutex
	lastPort   = map[string]int{}
)

// brokerEnv is the env a sharer spawn adds to use its owner's login, and
// the release for the broker lease (call once the turn is over). An
// instance with its own login gets nothing and a no-op release.
func brokerEnv(ctx context.Context, ins provider.Instance, opt provider.SpawnOptions, bin string) ([]string, func(), error) {
	owner, ok := provider.AuthOwner(ins)
	if !ok {
		return nil, func() {}, nil
	}
	profile := provider.OMPProfile(owner)
	if !provider.ValidOMPProfile(profile) {
		return nil, nil, fmt.Errorf("login owner %s: profile %q is not a valid omp profile name", owner.Name, profile)
	}
	env := append(envscrub.ScrubOSEnv(), provider.AccountEnv(owner)...)
	env = append(env, "CLAUDE_CONFIG_DIR=", "PI_CONFIG_FILES=", envBrokerURL+"=", envBrokerToken+"=")
	spec := brokerSpec{owner: owner.Name, bin: bin, profile: profile, env: env, idle: serverIdle(ins, opt),
		wrap: func(b string, a []string) (string, []string, string) {
			return opt.MemGuard.Wrap(b, a, "omp-broker", opt.SpawnSeq)
		}}
	key := spec.key()
	l, err := brokers.Acquire(ctx, cliserver.Spec{Instance: owner.Name, Group: owner.Name + "/broker", Key: key, Idle: spec.idle},
		func(ctx context.Context) (*brokerHandle, error) { return startBrokerFn(ctx, spec, key) })
	if err != nil {
		return nil, nil, fmt.Errorf("auth broker of %s: %w", owner.Name, err)
	}
	if l.Fresh {
		log.Info().Str("owner", owner.Name).Str("sharer", ins.Name).Int("pid", l.H.Pid()).Str("url", l.H.url).Msg("agents.omp: auth broker started")
	}
	return []string{envBrokerURL + "=" + l.H.url, envBrokerToken + "=" + l.H.token}, l.Release, nil
}

// ShutdownBrokers kills every auth broker (wick shutdown / upgrade).
func ShutdownBrokers() { brokers.Shutdown() }

// brokerTokenFile is where omp keeps a profile's broker bearer token:
// `<configRoot>/auth-broker.token`, configRoot = <home>/<PI_CONFIG_DIR|.omp>/
// profiles/<p> (cli auth-broker: pPe() = join(configRoot, "auth-broker.token");
// `auth-broker token` and `serve` create it on first use).
func brokerTokenFile(home, cfgDir, profile string) string {
	return filepath.Join(ompRoot(home, cfgDir), "profiles", profile, "auth-broker.token")
}

// readBrokerToken reads the owner profile's token file — no omp process.
// "" when the file does not exist yet (or is empty).
func readBrokerToken(spec brokerSpec) string {
	home, _ := homeDir()
	if home == "" {
		return ""
	}
	cfg := envValue(spec.env, "PI_CONFIG_DIR")
	if cfg == "" {
		cfg = os.Getenv("PI_CONFIG_DIR")
	}
	b, err := os.ReadFile(brokerTokenFile(home, cfg, spec.profile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// brokerToken is the owner profile's bearer token; swapped in tests. Read
// from the profile's token file when it exists (no process); only a profile
// that never had one runs `omp auth-broker token`, which creates the file,
// so that happens once per profile. The token is never logged.
var brokerToken = func(ctx context.Context, spec brokerSpec) (string, error) {
	if tok := readBrokerToken(spec); tok != "" {
		return tok, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Inside the memory guard ("omp-broker-token" scope, the owner
	// profile's instance limit), like every omp process wick starts.
	owner := provider.InstanceForAccountEnv([]string{"OMP_PROFILE=" + spec.profile})
	cmd, release := provider.HelperCommand(ctx, owner, provider.HelperLabel(provider.TypeOMP, "broker-token"),
		spec.bin, "--profile", spec.profile, "auth-broker", "token", "--json")
	defer release()
	cmd.Env = spec.env
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("omp auth-broker token: %w", err)
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.Token == "" {
		return "", errors.New("omp auth-broker token: no token in its output")
	}
	return v.Token, nil
}

func brokerPort(key string) (int, error) {
	lastPortMu.Lock()
	prev := lastPort[key]
	lastPortMu.Unlock()
	if prev > 0 {
		if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(prev)); err == nil {
			_ = ln.Close()
			return prev, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// startBroker execs `omp auth-broker serve` and waits until it answers an
// authorized /v1/snapshot.
func startBroker(ctx context.Context, spec brokerSpec, key string) (*brokerHandle, error) {
	token, err := brokerToken(ctx, spec)
	if err != nil {
		return nil, err
	}
	port, err := brokerPort(key)
	if err != nil {
		return nil, err
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	args := []string{"--profile", spec.profile, "auth-broker", "serve", "--bind=" + addr}
	bin, argv := spec.bin, args
	if spec.wrap != nil {
		bin, argv, _ = spec.wrap(spec.bin, args)
	}
	// Not CommandContext: the broker outlives the turn that started it.
	cmd := safeexec.Command(bin, argv...)
	cmd.Env = spec.env
	hideConsole(cmd)
	procgroup.Apply(cmd)
	dieWithParent(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start omp auth-broker: %w", err)
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			log.Debug().Str("owner", spec.owner).Str("line", sc.Text()).Msg("agents.omp: auth-broker")
		}
	}()
	go func() { _ = cmd.Wait(); close(done) }()
	pid := cmd.Process.Pid
	kill := func() {
		signalGroup(pid, false)
		select {
		case <-done:
		case <-time.After(rpcKillWait):
		}
		signalGroup(pid, true)
	}
	h := &brokerHandle{url: "http://" + addr, token: token, pid: pid, kill: kill, done: done}
	if err := waitBroker(ctx, h); err != nil {
		kill()
		return nil, err
	}
	lastPortMu.Lock()
	lastPort[key] = port
	lastPortMu.Unlock()
	return h, nil
}

func waitBroker(ctx context.Context, h *brokerHandle) error {
	c := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(brokerBootWait)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/v1/snapshot", nil)
		req.Header.Set("Authorization", "Bearer "+h.token)
		if resp, err := c.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			if resp.StatusCode == http.StatusUnauthorized {
				return errors.New("omp auth-broker refused its own token")
			}
		}
		select {
		case <-h.done:
			return errors.New("omp auth-broker exited during boot")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("omp auth-broker not ready in time")
}
