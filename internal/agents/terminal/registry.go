package terminal

import (
	"sync"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// registry.go is the per-type registry behind the web terminal. Each
// provider type that has one registers itself in its own provider_<type>.go
// file; Commands, LookupCommand, Env and Prepare read the registry and
// never switch on the type. Adding a terminal for a new type is one new
// file calling Register from init.

// Provider is what one provider type contributes to the web terminal.
type Provider struct {
	// Commands is the allowlist for an instance of this type. Required:
	// an empty list means the instance has no web terminal.
	Commands func(provider.Instance) []Command
	// Env, when set, adjusts the environment after the instance's account
	// env is added and before GOTTY_* vars are dropped. It gets the env so
	// far and returns the env to use.
	Env func(ins provider.Instance, env []string) []string
	// Prepare, when set, runs right before gotty starts, e.g. to create a
	// data dir the command writes into. An error aborts the start.
	Prepare func(provider.Instance) error
}

var (
	regMu     sync.RWMutex
	providers = map[provider.Type]Provider{}
)

// Register installs the terminal for one provider type, replacing any
// earlier one. A Provider with a nil Commands unregisters the type.
func Register(t provider.Type, p Provider) {
	regMu.Lock()
	defer regMu.Unlock()
	if p.Commands == nil {
		delete(providers, t)
		return
	}
	providers[t] = p
}

// lookup returns the registered terminal for t.
func lookup(t provider.Type) (Provider, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := providers[t]
	return p, ok
}

// Supported reports whether ins has a web terminal: its type is
// registered and the allowlist for it is not empty.
func Supported(ins provider.Instance) bool {
	return len(Commands(ins)) > 0
}

// Prepare runs the type's Prepare hook for ins, if any. Call it after
// the command is resolved and before the terminal starts.
func Prepare(ins provider.Instance) error {
	p, ok := lookup(ins.Type)
	if !ok || p.Prepare == nil {
		return nil
	}
	return p.Prepare(ins)
}
