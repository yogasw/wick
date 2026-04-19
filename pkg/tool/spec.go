package tool

import "github.com/yogasw/wick/pkg/entity"

// RegisterFunc wires a tool's routes on the Router wick passes in. It
// is the handler-side half of module registration — paired with a Tool
// meta and a typed Config struct at app.RegisterTool time.
//
// Handlers declared inside RegisterFunc stay stateless: per-instance
// metadata is read via c.Meta(), runtime-editable config values via
// c.Cfg(...). There is no per-module struct to carry private state.
type RegisterFunc func(r Router)

// Module is the internal, fully-resolved registration record wick keeps
// for every tool. It is produced by app.RegisterTool from a meta, a
// typed Config value (reflected into Configs at register time), and a
// RegisterFunc — downstream code does not construct Module directly.
type Module struct {
	Meta     Tool
	Configs  []entity.Config
	Register RegisterFunc
}
