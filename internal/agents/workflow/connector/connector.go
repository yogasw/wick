// Package connector is the workflow-facing index of connector modules.
// Engine `type: connector` nodes resolve a (module, op, row) tuple
// through Registry and invoke the underlying Operation.Execute via
// pkg/connector. Audit + credential lookup go through the injected
// hooks so this package stays free of DB imports.
package connector

import (
	"context"
	"net/http"
	"sort"
	"sync"

	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// RowCredsFn returns the credential map for a connector row (instance).
type RowCredsFn func(module, row string) (map[string]string, error)

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
	mu       sync.RWMutex
	modules  map[string]pkgconnector.Module
	rowCreds RowCredsFn
	auditor  RunAuditor
	httpCli  *http.Client
}

// NewRegistry builds an empty registry.
func NewRegistry(rowCreds RowCredsFn, audit RunAuditor) *Registry {
	return &Registry{
		modules:  map[string]pkgconnector.Module{},
		rowCreds: rowCreds,
		auditor:  audit,
		httpCli:  &http.Client{},
	}
}

// SetRowCreds wires in a credential lookup hook after construction.
// Server.go uses this to inject ConnectorsCredsAdapter once the
// connectors service is built — Manager.New() runs before service
// construction so the registry starts with a nil hook.
func (r *Registry) SetRowCreds(fn RowCredsFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rowCreds = fn
}

// Register adds a connector module.
func (r *Registry) Register(m pkgconnector.Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[m.Meta.Key] = m
}

// Module returns a module by key.
func (r *Registry) Module(key string) (pkgconnector.Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[key]
	return m, ok
}

// HTTPClient exposes the shared http.Client used when building Ctx.
func (r *Registry) HTTPClient() *http.Client { return r.httpCli }

// RowCreds returns the resolved credentials for (module, row). Empty
// map when no lookup hook was provided.
func (r *Registry) RowCreds(module, row string) (map[string]string, error) {
	if r.rowCreds == nil {
		return map[string]string{}, nil
	}
	return r.rowCreds(module, row)
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
		ops := make([]OpInfo, 0, len(m.Operations))
		for _, op := range m.Operations {
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
