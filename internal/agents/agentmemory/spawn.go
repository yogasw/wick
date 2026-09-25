package agentmemory

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Init wires the provider-side spawn injection so agent spawners can reach a
// memory backend without importing this package (which would be an import
// cycle — agentmemory imports provider). Call once at boot, after the backend
// subpackages have registered.
func Init() {
	provider.SetMemorySpawn(spawnContribution)
}

// spawnContribution resolves an instance's selected memory backend and builds
// the CLI args + env. Backs provider.MemorySpawnContribution.
func spawnContribution(ins *provider.Instance, t provider.Type) (provider.MemoryContribution, error) {
	id := backendID(ins.AgentMemoryProvider)
	be, ok := Get(id)
	if !ok {
		return provider.MemoryContribution{}, fmt.Errorf("agentmemory: unknown backend %q", id)
	}
	if be.Desc.Hook == nil {
		return provider.MemoryContribution{}, fmt.Errorf("agentmemory: %s cannot be wired into spawns", id)
	}
	args, env, err := be.Desc.Hook.Contribute(t, *ins, SpawnConn{
		ServerURL: ServerURL(be, *ins),
		AuthKey:   resolveKey(*ins),
		BinPath:   be.Mgr.BinPath(),
		// The store the daemon was last STARTED with, not whatever an
		// instance saved: a capture hook writes into the same store the MCP
		// side reads from, and the daemon owns that choice (PLAN §19).
		DataDir: strings.TrimSpace(be.Mgr.LaunchOpts().DataDir),
		// Same reasoning: the tuning the daemon was STARTED with, so the
		// hooks a spawn installs agree with the server they report to.
		Tuning: be.Mgr.LaunchOpts().Tuning,
	})
	if err != nil {
		return provider.MemoryContribution{}, err
	}
	return provider.MemoryContribution{Args: args, Env: env}, nil
}

// ServerURL resolves the base URL an instance talks to: its own setting when
// set (a remote server is allowed), else the managed daemon's loopback URL —
// which is http://127.0.0.1:<PrefPort> until a start remaps the port. Any
// trailing slash is dropped so callers can append a path.
func ServerURL(be *Backend, ins provider.Instance) string {
	if u := strings.TrimSpace(ins.AgentMemoryServerURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.TrimRight(be.Mgr.BaseURL(), "/")
}

// resolveKey returns the plaintext auth token for an instance, or "" when it
// sets none. Decryption failure yields "" rather than leaking the raw stored
// token into argv/env.
func resolveKey(ins provider.Instance) string { return resolveSecret(ins.AgentMemoryAuthKey) }

// resolveSecret turns a stored token into plaintext. Shared by the spawn
// wiring and by the daemon's own bearer token in ApplySettings — both hold the
// value the same encrypted way, and both must fail to "" rather than pass the
// raw stored token along.
func resolveSecret(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ""
	}
	if secretDecrypter == nil {
		return tok
	}
	plain, err := secretDecrypter(tok)
	if err != nil || strings.TrimSpace(plain) == "" {
		return ""
	}
	return plain
}

// secretDecrypter turns a stored wick_cenc_/wick_enc_ token back into
// plaintext. nil until wired; when nil, tokens pass through unchanged (safe
// for dev/tests where encryption is disabled).
var secretDecrypter func(string) (string, error)

// SetSecretDecrypter wires the boot-time secret decrypter used to unwrap a
// stored backend auth token at spawn. Backed by configs.Service.DecryptSecret.
func SetSecretDecrypter(fn func(string) (string, error)) { secretDecrypter = fn }
