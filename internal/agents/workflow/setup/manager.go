// Package setup wires every workflow subpkg together. Server code
// instantiates one Manager via New and calls Start to boot the
// engine, router, and all registered workflows.
package setup

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/canvas"
	"github.com/yogasw/wick/internal/agents/workflow/channel"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/cost"
	"github.com/yogasw/wick/internal/agents/workflow/dataset"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/guard"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/nodes"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
	"github.com/yogasw/wick/internal/agents/workflow/trigger"
)

// Manager bundles every wired piece so server.go can hand one struct
// to consumers (UI handlers, MCP transport, jobs).
type Manager struct {
	Layout     config.Layout
	Service    *service.FileService
	StateStore *state.FileStore
	Engine     *engine.Engine
	Router     *trigger.Router
	Cron       *trigger.CronScheduler
	Canvas     *canvas.Canvas
	Channels   *channel.Registry
	Connectors *connector.Registry
	Providers  *provider.Registry
	Datasets   dataset.Service
	Guard      *guard.Guard
	Cost       *cost.Tracker
	MCP        *mcp.Ops
}

// New constructs every dependency wired to a single Layout. Channels,
// connectors, providers, and guard start empty — caller plugs them in
// via With* before Start.
func New(layout config.Layout) *Manager {
	svc := service.New(layout)
	ss := state.New(layout)
	eng := engine.New(layout, svc, ss)
	router := trigger.NewRouter(eng, svc)
	cron := trigger.NewCronScheduler(router)
	can := canvas.New(svc)
	chReg := channel.NewRegistry()
	conReg := connector.NewRegistry(nil, nil)
	provReg := provider.NewRegistry()
	dsSvc := dataset.NewMem()
	// Guard default = off. Admin enables per-install via the agents
	// settings page (mode = warn|block). Keep the package alive so the
	// rule surface stays available — only the Apply gate is skipped
	// when mode = off.
	g := guard.New(guard.Config{Mode: guard.ModeOff})
	c := cost.New()

	// Wire executors so the engine can dispatch every node type once
	// registries have content.
	eng.Register(workflow.NodeShell, nodes.NewShellExecutor())
	eng.Register(workflow.NodeHTTP, nodes.NewHTTPExecutor())
	eng.Register(workflow.NodeBranch, nodes.NewBranchExecutor())
	eng.Register(workflow.NodeTransform, nodes.NewTransformExecutor())
	eng.Register(workflow.NodeEnd, nodes.NewEndExecutor())
	eng.Register(workflow.NodeClassify, nodes.NewClassifyExecutor(provReg))
	eng.Register(workflow.NodeAgent, nodes.NewAgentExecutor(provReg))
	eng.Register(workflow.NodeChannel, nodes.NewChannelExecutor(chReg))
	eng.Register(workflow.NodeConnector, nodes.NewConnectorExecutor(conReg))
	dsExec := nodes.NewDatasetExecutor(dsSvc)
	for _, t := range []workflow.NodeType{
		workflow.NodeDatasetGet, workflow.NodeDatasetExists, workflow.NodeDatasetQuery,
		workflow.NodeDatasetInsert, workflow.NodeDatasetUpsert, workflow.NodeDatasetDelete,
		workflow.NodeDatasetCount,
	} {
		eng.Register(t, dsExec)
	}

	ops := mcp.New(svc, eng, router, can, chReg, conReg, provReg, dsSvc, ss)

	return &Manager{
		Layout:     layout,
		Service:    svc,
		StateStore: ss,
		Engine:     eng,
		Router:     router,
		Cron:       cron,
		Canvas:     can,
		Channels:   chReg,
		Connectors: conReg,
		Providers:  provReg,
		Datasets:   dsSvc,
		Guard:      g,
		Cost:       c,
		MCP:        ops,
	}
}

// WithChannels registers one or more channels.
func (m *Manager) WithChannels(chs ...channel.Channel) *Manager {
	for _, ch := range chs {
		m.Channels.Register(ch)
	}
	return m
}

// WithProvider registers a provider.
func (m *Manager) WithProvider(p provider.Provider) *Manager {
	m.Providers.Register(p)
	return m
}

// WithGuardConfig replaces the guard configuration.
func (m *Manager) WithGuardConfig(cfg guard.Config) *Manager {
	m.Guard = guard.New(cfg)
	return m
}

// Start ensures layout, bootstraps router with current workflows, and
// kicks off the cron scheduler. Idempotent — safe to call from main.go
// on every boot.
func (m *Manager) Start(ctx context.Context) error {
	if err := m.Layout.EnsureLayout(); err != nil {
		return err
	}
	if err := Bootstrap(ctx, m.Service, m.Router, m.Cron); err != nil {
		return err
	}
	if m.Cron != nil {
		m.Cron.Start(ctx)
	}
	return nil
}

// Stop drains the router workers cleanly.
func (m *Manager) Stop() {
	if m.Cron != nil {
		m.Cron.Stop()
	}
	if m.Router != nil {
		m.Router.Stop()
	}
}

// Bootstrap wires every workflow folder found at startup into the
// router + cron scheduler. Called once from server startup after
// Service + Router are constructed.
func Bootstrap(ctx context.Context, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler) error {
	slugs, err := svc.List()
	if err != nil {
		return err
	}
	for _, slug := range slugs {
		w, err := svc.Load(slug)
		if err != nil {
			continue
		}
		router.Register(ctx, w)
		if cron != nil {
			cron.Sync(slug, w)
		}
	}
	return nil
}

// HotReload reloads + re-registers (or unregisters) one slug. Used by
// fsnotify watcher in production.
func HotReload(ctx context.Context, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler, slug string) error {
	w, err := svc.Load(slug)
	if err != nil {
		router.Unregister(slug)
		if cron != nil {
			cron.Unsync(slug)
		}
		return nil
	}
	router.Register(ctx, w)
	if cron != nil {
		cron.Sync(slug, w)
	}
	return nil
}

// CleanupOptions tunes the daily run-retention pass.
type CleanupOptions struct {
	SuccessTTL time.Duration
	FailedTTL  time.Duration
	KeepMax    int
	Now        func() time.Time
}

// CleanupRuns walks runs/ and removes old ones per policy.
func CleanupRuns(layout config.Layout, opts CleanupOptions) (removed int, err error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SuccessTTL == 0 {
		opts.SuccessTTL = 30 * 24 * time.Hour
	}
	if opts.FailedTTL == 0 {
		opts.FailedTTL = 90 * 24 * time.Hour
	}
	svc := service.New(layout)
	store := state.New(layout)
	slugs, err := svc.List()
	if err != nil {
		return 0, err
	}
	for _, slug := range slugs {
		runs, err := store.ListRuns(slug)
		if err != nil {
			continue
		}
		for i, rid := range runs {
			if opts.KeepMax > 0 && i < opts.KeepMax {
				continue
			}
			st, err := store.Load(slug, rid)
			if err != nil {
				continue
			}
			ttl := opts.SuccessTTL
			if st.Status == workflow.StatusFailed {
				ttl = opts.FailedTTL
			}
			if st.EndedAt != nil && opts.Now().Sub(*st.EndedAt) > ttl {
				dir := layout.WorkflowRunDir(slug, rid)
				if err := removeAll(dir); err == nil {
					removed++
				}
			}
		}
	}
	return removed, nil
}
