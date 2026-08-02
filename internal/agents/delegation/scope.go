package delegation

import (
	"sort"

	"github.com/yogasw/wick/internal/entity"
)

// ResolveScoped returns the roles visible to a session in projectID: every
// global role, with any role the project defines under the same key
// substituted in.
//
// Pure by design — no DB, no context. The MCP roster and the web UI both
// call it, so the two surfaces cannot disagree about what a project can
// see, and the rule stays testable without a database.
//
// An empty projectID resolves to the global scope alone, which is exactly
// the behaviour that predates scoping.
func ResolveScoped(profiles []entity.AgentProfile, projectID string) []entity.AgentProfile {
	byKey := make(map[string]entity.AgentProfile, len(profiles))
	for _, p := range profiles {
		if p.ProjectID != "" && p.ProjectID != projectID {
			continue // belongs to a different project
		}
		// A project row outranks a global one under the same key. Checked
		// against what is already stored rather than relying on query
		// order, which is not guaranteed.
		if existing, ok := byKey[p.Key]; ok && existing.ProjectID != "" {
			continue
		}
		byKey[p.Key] = p
	}
	out := make([]entity.AgentProfile, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	// Map iteration is random; callers render this list and diff it in
	// tests, so the order has to be deterministic.
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
