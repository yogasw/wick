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

var memorySpawnFn func(ins *Instance, t Type, folder string) (MemoryContribution, error)

// SetMemorySpawn wires the boot-time hook that resolves an instance's selected
// memory backend and returns the CLI args + env it needs. Called once from
// agentmemory.Init.
func SetMemorySpawn(fn func(*Instance, Type, string) (MemoryContribution, error)) { memorySpawnFn = fn }

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
