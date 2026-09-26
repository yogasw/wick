package agentmemory

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Init wires the provider-side spawn injection so agent spawners can reach a
// memory backend without importing this package (which would be an import
// cycle — agentmemory imports provider). Call once at boot, after the backend
// subpackages have registered.
func Init() {
	provider.SetMemorySpawn(spawnContribution)
	// After the spawn hook, which clears this one: the pair is wired
	// together so a replacement of one is never left beside a stale other.
	provider.SetMemoryPreview(PreviewContribution)
}

// spawnContribution resolves an instance's selected memory backend and builds
// the CLI args + env. Backs provider.MemorySpawnContribution.
//
// folder is the session's working directory, and the FIRST thing checked: a
// project that has memory switched off — or one that is not in a trial the
// host is running — gets no MCP server and no capture hooks at all, which is
// the honest meaning of "off" (neither recall nor record). See
// projectpolicy.go for the whole rule.
func spawnContribution(ins *provider.Instance, t provider.Type, folder string) (provider.MemoryContribution, error) {
	if !ProjectAllowedForFolder(folder) {
		return provider.MemoryContribution{}, nil
	}
	id := backendID(ins.AgentMemoryProvider)
	be, ok := Get(id)
	if !ok {
		return provider.MemoryContribution{}, fmt.Errorf("agentmemory: unknown backend %q", id)
	}
	if be.Desc.Hook == nil {
		return provider.MemoryContribution{}, fmt.Errorf("agentmemory: %s cannot be wired into spawns", id)
	}
	// No URL, no wiring. wick used to fill this gap with the PREFERRED port,
	// which is a guess that points at a real address: here it pointed at a
	// different ai-memory with a different store, and every session after a
	// handover recalled and captured there (adopt.go).
	//
	// The gap is not an error, though, and that distinction matters. A memory
	// daemon that is down must not stop agents from running — it means they
	// run without memory, which is the same contribution a project with
	// memory switched off already gets. It is logged rather than swallowed,
	// and the panel shows the daemon as not running, because a session that
	// silently forgets is the failure this whole feature exists to catch.
	server := ServerURL(be, *ins)
	if server == "" {
		noteSpawnWithoutMemory()
		log.Warn().
			Str("component", "agentmemory").
			Str("backend", id).
			Str("instance", InstanceRef{Type: string(ins.Type), Name: ins.Name}.Label()).
			Msg("agentmemory: no reachable daemon — this session gets no memory rather than a guessed URL")
		return provider.MemoryContribution{}, nil
	}
	return contributionAt(be, ins, t, server)
}

// contributionAt builds the wiring for one instance against a given address.
//
// The address is the parameter because that is the ONLY thing the spawn and
// the preview disagree about: a spawn refuses to guess one, a preview shows
// the one a spawn would use. Everything else — the hook, the key, the store,
// the tuning — has to be identical, or the block an operator reads stops
// describing what actually happens.
func contributionAt(be *Backend, ins *provider.Instance, t provider.Type, server string) (provider.MemoryContribution, error) {
	args, env, err := be.Desc.Hook.Contribute(t, *ins, SpawnConn{
		ServerURL: server,
		AuthKey:   resolveKey(be, *ins),
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

// PreviewContribution is the wiring an instance WOULD get — the providers
// page's "what wick passes to this agent" block, and the measurement behind
// "does this provider type support memory at all".
//
// It is hypothetical by construction, and keeping it that way is the point of
// its existing separately. The spawn path refuses to guess an address, because
// a real session handed the wrong one reads and writes somebody else's store.
// A preview starts no session and writes nothing; its job is to show how this
// instance is CONFIGURED. Emptying it whenever the daemon happens to be down
// told an operator that codex has no memory wiring at all — which is false,
// and is the same silence the spawn path's Health finding exists to remove. It
// was also wrong on a freshly configured host where nothing has started yet.
//
// The second return is the sentence that must be shown beside it when nothing
// is listening at that address right now. Both facts, neither hidden: this is
// the configuration, AND a session started now would get no memory.
func PreviewContribution(ins provider.Instance, t provider.Type) (provider.MemoryContribution, string, error) {
	id := backendID(ins.AgentMemoryProvider)
	be, ok := Get(id)
	if !ok {
		return provider.MemoryContribution{}, "", fmt.Errorf("agentmemory: unknown backend %q", id)
	}
	if be.Desc.Hook == nil {
		return provider.MemoryContribution{}, "", fmt.Errorf("agentmemory: %s cannot be wired into spawns", id)
	}
	// No project gate here: a preview is about the INSTANCE, and the
	// per-project switch answers a question about a folder no preview has.
	contrib, err := contributionAt(be, &ins, t, PreviewURL(be, ins))
	if err != nil {
		return provider.MemoryContribution{}, "", err
	}
	return contrib, previewReachabilityNote(be, ins), nil
}

// previewReachabilityNote is empty when the daemon is answering, and otherwise
// says what the preview cannot: that this configuration is real and a session
// started right now would still get nothing.
func previewReachabilityNote(be *Backend, ins provider.Instance) string {
	if strings.TrimSpace(ins.AgentMemoryServerURL) != "" {
		// Somebody else's server. wick does not manage it and has no
		// business reporting on whether it is up.
		return ""
	}
	if be.Mgr.BoundPort() != 0 {
		return ""
	}
	return "This is the configuration an agent would be given. No " + be.Desc.DisplayName +
		" daemon is running right now, so a session started now would get no memory at all — it would neither recall nor record. Start it from the Agent Memory panel."
}

// PreviewURL is the address a spawn WOULD use: the instance's own override, or
// the managed daemon's — its real port when one is known, and otherwise the
// port the next start would bind.
//
// That last fallback is the one the spawn path refuses to make, and the
// difference is the consequence. Guessing for a real session points it at
// whatever else holds the port; guessing for a preview describes the host as
// it is about to be, next to a note saying nothing is there yet.
func PreviewURL(be *Backend, ins provider.Instance) string {
	if u := strings.TrimSpace(ins.AgentMemoryServerURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	port := be.Mgr.BoundPort()
	if port == 0 {
		port = be.Mgr.PrefPort()
	}
	if port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// ServerURL resolves the base URL an instance talks to: its own setting when
// set (a remote server is allowed), else the managed daemon's loopback URL.
// Any trailing slash is dropped so callers can append a path.
//
// EMPTY when neither is available — see Manager.BaseURL. It used to be the
// preferred port in that case, which is what pointed agents at a daemon wick
// does not manage after a handover.
func ServerURL(be *Backend, ins provider.Instance) string {
	if u := strings.TrimSpace(ins.AgentMemoryServerURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.TrimRight(be.Mgr.BaseURL(), "/")
}

// resolveKey returns the plaintext bearer a spawn presents, or "" when there
// is none. Decryption failure yields "" rather than leaking the raw stored
// token into argv/env.
//
// An instance that sets no key of its own INHERITS the managed daemon's —
// the auto-default-but-overridable shape the rest of this block already has
// (Yoga, 2026-09-25). Without it, configuring a token in the panel's Settings
// would lock out every instance that had not also been given one by hand, and
// the failure would arrive as an unexplained 401 inside an agent turn.
//
// It inherits ONLY when the instance talks to the daemon wick manages. An
// instance pointed at somebody else's server gets exactly the key it was
// given: sending wick's own bearer to a host wick does not run would hand a
// credential to a third party because a field was left blank.
func resolveKey(be *Backend, ins provider.Instance) string {
	if k := resolveSecret(ins.AgentMemoryAuthKey); k != "" {
		return k
	}
	if strings.TrimSpace(ins.AgentMemoryServerURL) != "" {
		return ""
	}
	return resolveSecret(be.Mgr.LaunchOpts().AuthToken)
}

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
