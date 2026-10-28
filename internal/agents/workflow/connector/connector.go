// Package connector is the workflow-facing index of connector modules.
// Engine `type: connector` nodes name a (module, op, instance) tuple and
// run it through the injected ExecuteFn — the same connectors.Service
// path a direct MCP call takes — so this package stays free of DB
// imports and never handles an instance's config itself.
package connector

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/yogasw/wick/internal/entity"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// Call is one connector op a workflow node runs as its owner.
type Call struct {
	Module string
	Op     string
	// Instance names the connector instance by ID or label. Empty picks
	// the newest instance of Module.
	Instance string
	// AccountID, when set, runs as that connected account of the instance.
	AccountID string
	Owner     *entity.User
	OwnerTags []string
	Input     map[string]string
	RawInput  map[string]any
}

// ExecuteFn runs c and returns the op's JSON response.
type ExecuteFn func(ctx context.Context, c Call) (string, error)

// UserResolverFn resolves a workflow's owner (Workflow.CreatedBy) into the
// authenticated user + their filter tag IDs, so the connector executor can
// stamp identity onto the run context the same way the cookie-session
// middleware does for HTTP. Returns a nil user (no error) when the id is
// unknown so the run continues unauthenticated rather than failing here.
type UserResolverFn func(ctx context.Context, userID string) (*entity.User, []string, error)

// RunAuditor receives one record per connector node execution.
type RunAuditor interface {
	WriteRun(ctx context.Context, rec RunRecord) error
}

// RunRecord captures one execution.
type RunRecord struct {
	WorkflowID string
	RunID      string
	NodeID       string
	Module       string
	Op           string
	Row          string
	Source       string
	RequestArgs  map[string]any
	Response     any
	Status       string
	Error        string
	LatencyMs    int64
	Destructive  bool
}

// Registry indexes connector modules.
type Registry struct {
	mu           sync.RWMutex
	modules      map[string]pkgconnector.Module
	executor     ExecuteFn
	userResolver UserResolverFn
	auditor      RunAuditor
}

// NewRegistry builds an empty registry.
func NewRegistry(exec ExecuteFn, audit RunAuditor) *Registry {
	return &Registry{
		modules:  map[string]pkgconnector.Module{},
		executor: exec,
		auditor:  audit,
	}
}

// SetExecutor wires the op runner after construction. Server.go injects
// ConnectorsExecutor once the connectors service is built — Manager.New()
// runs before service construction so the registry starts with a nil hook.
func (r *Registry) SetExecutor(fn ExecuteFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executor = fn
}

// SetUserResolver wires the workflow-owner → user lookup hook. Like
// SetExecutor it is injected post-construction from server.go once the
// login service exists. Nil hook = runs stay unauthenticated (legacy
// behaviour) and identity-gated connector ops keep returning their own
// "not authenticated" error.
func (r *Registry) SetUserResolver(fn UserResolverFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userResolver = fn
}

// ResolveUser returns the user + filter tag IDs for a workflow owner id.
// Empty id or nil hook yields (nil, nil, nil) so callers can skip stamping.
func (r *Registry) ResolveUser(ctx context.Context, userID string) (*entity.User, []string, error) {
	r.mu.RLock()
	fn := r.userResolver
	r.mu.RUnlock()
	if fn == nil || userID == "" {
		return nil, nil, nil
	}
	return fn(ctx, userID)
}

// Register adds a connector module.
func (r *Registry) Register(m pkgconnector.Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[m.Meta.Key] = m
}

// Unregister drops the module stored under key. No-op when absent.
//
// Mirrors the core registry so a deleted custom connector stops appearing
// in the workflow palette immediately, instead of lingering as a node type
// whose module no longer exists.
func (r *Registry) Unregister(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.modules, key)
}

// Module returns a module by key.
func (r *Registry) Module(key string) (pkgconnector.Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[key]
	return m, ok
}

// Execute runs c through the wired ExecuteFn.
func (r *Registry) Execute(ctx context.Context, c Call) (string, error) {
	r.mu.RLock()
	fn := r.executor
	r.mu.RUnlock()
	if fn == nil {
		return "", errors.New("connector execution is not wired")
	}
	return fn(ctx, c)
}

// WriteAudit forwards to the configured RunAuditor (no-op if nil).
func (r *Registry) WriteAudit(ctx context.Context, rec RunRecord) {
	if r.auditor == nil {
		return
	}
	_ = r.auditor.WriteRun(ctx, rec)
}

// List returns the registered module keys sorted.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.modules))
	for k := range r.modules {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Describe returns introspection metadata for `workflow_connectors` MCP op.
func (r *Registry) Describe() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Info{}
	for _, m := range r.modules {
		allOps := m.AllOps()
		ops := make([]OpInfo, 0, len(allOps))
		for _, op := range allOps {
			inputs := make([]OpInput, 0, len(op.Input))
			for _, in := range op.Input {
				inputs = append(inputs, OpInput{
					Key:         in.Key,
					Description: in.Description,
					Required:    in.Required,
				})
			}
			ops = append(ops, OpInfo{
				Key:         op.Key,
				Name:        op.Name,
				Description: op.Description,
				Destructive: op.Destructive,
				Input:       inputs,
			})
		}
		out = append(out, Info{
			Module:      m.Meta.Key,
			Name:        m.Meta.Name,
			Description: m.Meta.Description,
			Operations:  ops,
		})
	}
	return out
}

// Info is one introspection row.
type Info struct {
	Module      string   `json:"module"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Operations  []OpInfo `json:"operations"`
}

// OpInfo is one op's introspection shape.
type OpInfo struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Destructive bool      `json:"destructive,omitempty"`
	Input       []OpInput `json:"input,omitempty"`
}

// OpInput is one named argument the op expects. The workflow editor
// renders these as form fields under the connector node's Args panel
// so users see exactly which keys the op needs (and which are
// required) without reading the connector source.
type OpInput struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}
