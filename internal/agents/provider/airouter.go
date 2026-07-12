package provider

// AI-router spawn injection. The concrete router registry lives in
// internal/agents/airouter; this package can't import it (that package
// imports this one), so the resolve-and-contribute logic is injected at boot
// via SetRouterSpawn / SetRouterSlots — the same decoupling pattern the old
// SetSecretDecrypter hook used. The spawners call RouterSpawnContribution and
// stay ignorant of which router is selected.

// RouterSlot is one named model slot a provider type exposes when routed
// through an AI router. Which slots exist is router- and type-specific (claude
// has Opus/Sonnet/Haiku; codex has a primary Model + a Subagent model). The FE
// renders one model picker per slot; the router's SpawnHook maps each slot's
// chosen model onto the right CLI flag/env.
type RouterSlot struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
}

// RouterContribution is the extra CLI args + child env a router injects at
// spawn time for one instance / agent-type.
type RouterContribution struct {
	Args []string
	Env  []string
}

var (
	routerSpawnFn func(ins *Instance, t Type) (RouterContribution, error)
	routerSlotsFn func(routerID string, t Type) []RouterSlot
	routerKeyFn   func(ins *Instance) string
)

// SetRouterKeyResolver wires the boot-time resolver that returns an
// instance's plaintext router API key (for the spawn-log reveal path, which
// must unmask ANTHROPIC_AUTH_TOKEN / OPENAI_API_KEY). Decoupled from the base
// URL so it works without WICK_PORT.
func SetRouterKeyResolver(fn func(*Instance) string) { routerKeyFn = fn }

// RouterAuthKey resolves the plaintext router API key for an instance, or ""
// when the instance doesn't route through a router or the resolver is unwired.
func RouterAuthKey(ins Instance) string {
	if routerKeyFn == nil || !ins.UseAIRouter {
		return ""
	}
	return routerKeyFn(&ins)
}

// SetRouterSpawn wires the boot-time hook that resolves an instance's selected
// router and returns the CLI args + env it needs. Called once from airouter.Init.
func SetRouterSpawn(fn func(*Instance, Type) (RouterContribution, error)) { routerSpawnFn = fn }

// SetRouterSlots wires the boot-time lookup for a router's model slots.
func SetRouterSlots(fn func(routerID string, t Type) []RouterSlot) { routerSlotsFn = fn }

// RouterSpawnContribution returns the args + env for an instance routed
// through its selected AI router. Empty when the instance doesn't use a router
// or the hook is unwired.
func RouterSpawnContribution(ins *Instance, t Type) (RouterContribution, error) {
	if routerSpawnFn == nil || ins == nil || !ins.UseAIRouter {
		return RouterContribution{}, nil
	}
	return routerSpawnFn(ins, t)
}

// RouterSlots returns the model slots the given router exposes for type t.
// routerID "" falls back to the default router. Empty when unsupported/unwired.
func RouterSlots(routerID string, t Type) []RouterSlot {
	if routerSlotsFn == nil {
		return nil
	}
	return routerSlotsFn(routerID, t)
}
