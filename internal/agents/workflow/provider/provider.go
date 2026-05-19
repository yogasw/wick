// Package provider is the abstraction the workflow engine talks to for
// classify + agent nodes. Concrete impls (Claude Code, Codex, Gemini)
// satisfy Provider and register at boot via Registry. The interface
// keeps the engine decoupled from CLI spawn details.
package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Provider implementations spawn the underlying CLI/API.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	StructuredCall(ctx context.Context, req StructuredRequest) (StructuredResult, error)
	AgentCall(ctx context.Context, req AgentRequest) (AgentResult, error)
	ListSkills(ctx context.Context) ([]Skill, error)
}

// Capabilities flags what the provider supports natively.
type Capabilities struct {
	StructuredOutput bool
	Streaming        bool
	DefaultPreset    string
}

// StructuredRequest is the call shape for a classify node.
type StructuredRequest struct {
	Prompt       string
	SystemPrompt string
	Schema       map[string]any
	Preset       string
	SessionID    string
}

// StructuredResult is what the provider returns.
type StructuredResult struct {
	Raw    string
	Parsed map[string]any
	OK     bool
	Error  string
	Usage  Usage
}

// AgentRequest is the call shape for an agent node.
type AgentRequest struct {
	Prompt    string
	Preset    string
	Workspace string
	Skills    []string
	Tools     []string
	MaxTurns  int
	SessionID string
}

// AgentResult is what the provider returns for an agent call.
type AgentResult struct {
	Text       string
	ToolsUsed  []string
	SkillsUsed []string
	Usage      Usage
}

// Usage captures token + cost telemetry per call.
type Usage struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	LatencyMs    int64   `json:"latency_ms,omitempty"`
}

// Skill is one provider-specific capability bundle. Surfaced via
// `workflow_skills` MCP introspection.
type Skill struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Source      string         `json:"source"`
}

// Registry holds named providers for the engine.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	defaultID string
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider. First registered becomes the default.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
	if r.defaultID == "" {
		r.defaultID = p.Name()
	}
}

// SetDefault picks which provider classify/agent nodes use when they
// don't specify `provider:`.
func (r *Registry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[name]; !ok {
		return fmt.Errorf("provider %q not registered", name)
	}
	r.defaultID = name
	return nil
}

// Get returns a provider, falling back to the default when name is empty.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name == "" {
		name = r.defaultID
	}
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

// List returns all provider names sorted.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Describe returns introspection metadata for `workflow_providers` MCP op.
func (r *Registry) Describe() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Info{}
	for _, p := range r.providers {
		out = append(out, Info{
			Name:         p.Name(),
			Capabilities: p.Capabilities(),
			IsDefault:    p.Name() == r.defaultID,
		})
	}
	return out
}

// Info is one introspection row.
type Info struct {
	Name         string       `json:"name"`
	Capabilities Capabilities `json:"capabilities"`
	IsDefault    bool         `json:"is_default,omitempty"`
}
