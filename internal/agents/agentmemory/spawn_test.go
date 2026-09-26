package agentmemory

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

type fakeHook struct{}

func (fakeHook) Contribute(t provider.Type, ins provider.Instance, conn SpawnConn) (args, env []string, err error) {
	return []string{"--server", conn.ServerURL}, []string{"SERVER=" + conn.ServerURL, "KEY=" + conn.AuthKey}, nil
}

// registerTestBackend registers a backend under a unique id (Register is
// idempotent, so re-registering the same id in another test would silently
// keep the first one).
func registerTestBackend(t *testing.T, id string, port int) {
	t.Helper()
	Register(Descriptor{ID: id, DisplayName: id, PrefPort: port, HealthPath: "/healthz", Hook: fakeHook{}})
}

func TestMemorySpawnContributionSkipsInstancesWithMemoryOff(t *testing.T) {
	registerTestBackend(t, "testmem-off", 41100)
	Init()

	got, err := provider.MemorySpawnContribution(&provider.Instance{
		UseAgentMemory:      false,
		AgentMemoryProvider: "testmem-off",
	}, provider.TypeClaude, "")
	if err != nil {
		t.Fatalf("contribution err: %v", err)
	}
	if len(got.Args) != 0 || len(got.Env) != 0 {
		t.Fatalf("instance with agent memory off should contribute nothing, got %+v", got)
	}
}

// withBoundPort makes a registered backend's manager report a daemon on port,
// the way it would after wick started one itself.
func withBoundPort(t *testing.T, id string, port int) {
	t.Helper()
	be, ok := Get(id)
	if !ok {
		t.Fatalf("backend %q is not registered", id)
	}
	be.Mgr.port.Store(int32(port))
	t.Cleanup(func() { be.Mgr.port.Store(0) })
}

func TestMemorySpawnContributionResolvesServerURL(t *testing.T) {
	registerTestBackend(t, "testmem-url", 41200)
	Init()

	// No explicit server URL → the daemon's REAL loopback base. Note the
	// port is 41201 while the preference is 41200: the two differ on purpose,
	// because handing out the preference is exactly the bug (adopt.go).
	withBoundPort(t, "testmem-url", 41201)
	contrib, err := provider.MemorySpawnContribution(&provider.Instance{
		UseAgentMemory:      true,
		AgentMemoryProvider: "testmem-url",
	}, provider.TypeClaude, "")
	if err != nil {
		t.Fatalf("contribution err: %v", err)
	}
	joined := strings.Join(contrib.Env, " ")
	if !strings.Contains(joined, "SERVER=http://127.0.0.1:41201") {
		t.Fatalf("the URL must be the bound port, not the preference: %v", contrib.Env)
	}
	if strings.Contains(joined, "41200") {
		t.Fatalf("the preferred port must not reach a spawn: %v", contrib.Env)
	}
	if len(contrib.Args) == 0 {
		t.Fatalf("hook args dropped: %+v", contrib)
	}

	// Explicit server URL wins, trailing slash trimmed so callers can append.
	// It is unaffected by any of this: the instance said where to go.
	contrib, err = provider.MemorySpawnContribution(&provider.Instance{
		UseAgentMemory:       true,
		AgentMemoryProvider:  "testmem-url",
		AgentMemoryServerURL: "http://memhost:8080/",
	}, provider.TypeClaude, "")
	if err != nil {
		t.Fatalf("contribution err: %v", err)
	}
	if joined := strings.Join(contrib.Env, " "); !strings.Contains(joined, "SERVER=http://memhost:8080 ") {
		t.Fatalf("explicit server URL not used verbatim (minus trailing slash): %v", contrib.Env)
	}
}

// The heart of it: with no daemon wick can address, a spawn gets NO memory
// wiring — not a URL built from the preferred port.
//
// It is not an error either. A memory daemon that is down must not stop agents
// from running; it means they run without memory, which is what the panel is
// for saying out loud.
func TestMemorySpawnContributionWiresNothingWhenThePortIsUnknown(t *testing.T) {
	registerTestBackend(t, "testmem-down", 41300)
	Init()

	got, err := provider.MemorySpawnContribution(&provider.Instance{
		UseAgentMemory:      true,
		AgentMemoryProvider: "testmem-down",
	}, provider.TypeClaude, "")
	if err != nil {
		t.Fatalf("a daemon that is down is not a spawn failure: %v", err)
	}
	if len(got.Args) != 0 || len(got.Env) != 0 {
		t.Fatalf("no reachable daemon must wire nothing, got %+v", got)
	}
	// And in particular, nothing built from the preference.
	if strings.Contains(strings.Join(got.Env, " "), "41300") {
		t.Fatalf("the preferred port leaked into a spawn: %v", got.Env)
	}
}

func TestMemorySpawnContributionDefaultsToAIMemoryBackend(t *testing.T) {
	// An instance that names no backend resolves to ai-memory, the default —
	// registered here under its real id so the lookup is the real one.
	Register(Descriptor{ID: "ai-memory", DisplayName: "ai-memory", PrefPort: 49374, HealthPath: "/healthz", Hook: fakeHook{}})
	Init()
	withBoundPort(t, "ai-memory", 49375)

	contrib, err := provider.MemorySpawnContribution(&provider.Instance{UseAgentMemory: true}, provider.TypeClaude, "")
	if err != nil {
		t.Fatalf("contribution err: %v", err)
	}
	if joined := strings.Join(contrib.Env, " "); !strings.Contains(joined, "SERVER=http://127.0.0.1:49375") {
		t.Fatalf("empty provider should resolve to ai-memory on its bound port: %v", contrib.Env)
	}
	if id := backendID(""); id != "ai-memory" {
		t.Fatalf("backendID(\"\") = %q, want ai-memory", id)
	}
}

func TestMemorySpawnContributionUnknownBackendErrors(t *testing.T) {
	Init()
	_, err := provider.MemorySpawnContribution(&provider.Instance{
		UseAgentMemory:      true,
		AgentMemoryProvider: "does-not-exist",
	}, provider.TypeClaude, "")
	if err == nil {
		t.Fatal("expected error for unknown backend id")
	}
}

func TestResolveKeyDecryptsAndFailsClosed(t *testing.T) {
	t.Cleanup(func() { secretDecrypter = nil })

	// A backend with no daemon token of its own, so every case here
	// exercises the instance's key alone (inheritance has its own file).
	keyBe := testBackend(&fakeData{})

	// Unwired decrypter → the stored value passes through (dev/test setups
	// store plaintext).
	secretDecrypter = nil
	if got := resolveKey(keyBe, provider.Instance{AgentMemoryAuthKey: "plain"}); got != "plain" {
		t.Fatalf("unwired decrypter should pass through, got %q", got)
	}

	SetSecretDecrypter(func(s string) (string, error) { return "unwrapped:" + s, nil })
	if got := resolveKey(keyBe, provider.Instance{AgentMemoryAuthKey: "wick_cenc_x"}); got != "unwrapped:wick_cenc_x" {
		t.Fatalf("decrypted key not used, got %q", got)
	}

	// A failing decrypt must not leak the stored token into argv/env.
	SetSecretDecrypter(func(s string) (string, error) { return "", errors.New("decrypt failed") })
	if got := resolveKey(keyBe, provider.Instance{AgentMemoryAuthKey: "wick_cenc_x"}); got != "" {
		t.Fatalf("failed decrypt leaked the stored token: %q", got)
	}
}
