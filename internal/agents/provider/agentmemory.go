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

var memorySpawnFn func(ins *Instance, t Type) (MemoryContribution, error)

// SetMemorySpawn wires the boot-time hook that resolves an instance's selected
// memory backend and returns the CLI args + env it needs. Called once from
// agentmemory.Init.
func SetMemorySpawn(fn func(*Instance, Type) (MemoryContribution, error)) { memorySpawnFn = fn }

// MemorySpawnContribution returns the args + env for an instance wired to its
// selected memory backend. Empty when the instance doesn't use agent memory or
// the hook is unwired.
func MemorySpawnContribution(ins *Instance, t Type) (MemoryContribution, error) {
	if memorySpawnFn == nil || ins == nil || !ins.UseAgentMemory {
		return MemoryContribution{}, nil
	}
	return memorySpawnFn(ins, t)
}
