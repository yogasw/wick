package provider

// Agent-memory spawn injection. The concrete backend registry lives in
// internal/agents/agentmemory; this package can't import it (that package
// imports this one), so the resolve-and-contribute logic is injected at boot
// via SetMemorySpawn — the same decoupling the AI-router hook uses next door.
// The spawners call MemorySpawnContribution and stay ignorant of which memory
// backend is selected.

// MemoryContribution is the extra CLI args + child env a memory backend
// injects at spawn time for one instance / agent-type. Typically an MCP
// server pointing at the backend, plus the hook wiring when capture is on.
type MemoryContribution struct {
	Args []string
	Env  []string
}

var (
	memorySpawnFn   func(ins *Instance, t Type, folder string) (MemoryContribution, error)
	memoryPreviewFn func(ins Instance, t Type) (MemoryContribution, string, error)
)

// SetMemorySpawn wires the boot-time hook that resolves an instance's selected
// memory backend and returns the CLI args + env it needs. Called once from
// agentmemory.Init.
//
// It clears the preview hook, because the two are one wiring: a caller that
// replaces how spawning resolves — a test with a stub backend — means the
// preview to go with it, not whatever was wired before.
func SetMemorySpawn(fn func(*Instance, Type, string) (MemoryContribution, error)) {
	memorySpawnFn = fn
	memoryPreviewFn = nil
}

// SetMemoryPreview wires the HYPOTHETICAL resolver — the wiring an instance
// WOULD get, for the providers page's preview block. Set after SetMemorySpawn.
func SetMemoryPreview(fn func(Instance, Type) (MemoryContribution, string, error)) {
	memoryPreviewFn = fn
}

// MemoryPreviewContribution resolves the contribution an instance would be
// given, without requiring that it could be given right now.
//
// The two paths differ in exactly one thing and it matters: a SPAWN refuses to
// resolve an address it cannot verify, because a real session handed the wrong
// one reads and writes somebody else's store. A PREVIEW starts no session, and
// emptying it whenever the daemon happens to be down told operators that a
// provider type had no memory wiring at all.
//
// Unwired, previewing falls back to spawning — which is what it was before the
// split, and what keeps a caller that replaced only the spawn resolver honest.
// The second return is the note to show beside the block when nothing would
// actually receive the wiring.
func MemoryPreviewContribution(ins *Instance, t Type) (MemoryContribution, string, error) {
	if ins == nil {
		return MemoryContribution{}, "", nil
	}
	if memoryPreviewFn != nil {
		return memoryPreviewFn(*ins, t)
	}
	c, err := MemorySpawnContribution(ins, t, "")
	return c, "", err
}

// MemorySpawnContribution returns the args + env for an instance wired to its
// selected memory backend. Empty when the instance doesn't use agent memory or
// the hook is unwired.
//
// folder is the directory the session runs in. It is passed because a PROJECT
// can now keep memory out of itself, or opt itself into a trial while the
// other projects stay quiet, and the folder is what a spawn has to identify
// its project with — the id never reaches this layer. An empty folder means
// "no project in particular", which follows the instance the way it always
// did.
func MemorySpawnContribution(ins *Instance, t Type, folder string) (MemoryContribution, error) {
	if memorySpawnFn == nil || ins == nil || !ins.UseAgentMemory {
		return MemoryContribution{}, nil
	}
	return memorySpawnFn(ins, t, folder)
}
