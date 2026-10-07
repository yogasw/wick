package omp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
)

// fakeBrokers swaps in a broker manager whose start never execs omp.
func fakeBrokers(t *testing.T) (starts *int, specs *[]brokerSpec) {
	t.Helper()
	n, got := 0, []brokerSpec{}
	prevM, prevStart := brokers, startBrokerFn
	brokers = cliserver.New[*brokerHandle]("omp-broker", 1)
	startBrokerFn = func(ctx context.Context, spec brokerSpec, key string) (*brokerHandle, error) {
		n++
		got = append(got, spec)
		done := make(chan struct{})
		return &brokerHandle{url: "http://127.0.0.1:4242", token: "tok-secret", pid: 1, done: done,
			kill: func() {
				select {
				case <-done:
				default:
					close(done)
				}
			}}, nil
	}
	restore := provider.SwapAuthInstanceLookup(func(tp provider.Type, name string) (provider.Instance, error) {
		if tp == provider.TypeOMP && name == "owner" {
			return provider.Instance{Type: provider.TypeOMP, Name: "owner", OMPConfig: &provider.OMPConfig{Profile: "owner-p"}}, nil
		}
		return provider.Instance{}, errors.New("not found")
	})
	t.Cleanup(func() { brokers.Shutdown(); brokers, startBrokerFn = prevM, prevStart; restore() })
	return &n, &got
}

func TestBrokerEnvForSharer(t *testing.T) {
	starts, specs := fakeBrokers(t)
	sharer := provider.Instance{Type: provider.TypeOMP, Name: "sharer", AuthFrom: "owner", OMPConfig: &provider.OMPConfig{Profile: "sharer-p"}}

	env, release, err := brokerEnv(context.Background(), sharer, provider.SpawnOptions{}, "/bin/omp")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, envBrokerURL+"=http://127.0.0.1:4242") || !slices.Contains(env, envBrokerToken+"=tok-secret") {
		t.Fatalf("broker env = %v", env)
	}
	if (*specs)[0].profile != "owner-p" || !slices.Contains((*specs)[0].env, "OMP_PROFILE=owner-p") {
		t.Errorf("broker not run on the owner's profile: %+v", (*specs)[0].profile)
	}
	// A second turn shares the running broker.
	_, release2, err := brokerEnv(context.Background(), sharer, provider.SpawnOptions{}, "/bin/omp")
	if err != nil {
		t.Fatal(err)
	}
	if *starts != 1 {
		t.Errorf("broker started %d times, want 1", *starts)
	}
	release()
	release2()

	// An instance with its own login gets nothing and no broker.
	own := provider.Instance{Type: provider.TypeOMP, Name: "owner"}
	if env, rel, err := brokerEnv(context.Background(), own, provider.SpawnOptions{}, "/bin/omp"); err != nil || env != nil {
		t.Errorf("own login: env %v err %v", env, err)
	} else {
		rel()
	}
}

// The RPC path hands the broker vars to omp, keeps the sharer's profile,
// and never records the token in the spawn's env.
func TestRPCSpawnGetsBrokerEnv(t *testing.T) {
	fakeBrokers(t)
	var got rpcSpec
	prev := startRPCFn
	startRPCFn = func(ctx context.Context, spec rpcSpec) (*rpcConn, error) {
		got = spec
		return nil, errors.New("fake: not started")
	}
	t.Cleanup(func() { startRPCFn = prev; rpcServers.Shutdown(); rpcServers = newRPCManager() })

	sharer := provider.Instance{Type: provider.TypeOMP, Name: "sharer", AuthFrom: "owner", OMPConfig: &provider.OMPConfig{Profile: "sharer-p"}}
	opt := provider.SpawnOptions{Instance: &sharer, SessionID: "s1", Workspace: t.TempDir()}
	env, release, err := brokerEnv(context.Background(), sharer, opt, "/bin/omp")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Spawner{}.spawnRPC(context.Background(), opt, sharer, "/bin/omp", "", "", "", nil, env, release)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.env, envBrokerToken+"=tok-secret") || !slices.Contains(got.env, envBrokerURL+"=http://127.0.0.1:4242") {
		t.Errorf("rpc env lacks the broker vars")
	}
	if i := slices.Index(got.args, "--profile"); i < 0 || got.args[i+1] != "sharer-p" {
		t.Errorf("sharer lost its own profile: %v", got.args)
	}
	for _, kv := range p.Env() {
		if strings.Contains(kv, "tok-secret") {
			t.Errorf("token recorded in spawn env: %q", kv)
		}
	}
}
