package airouter

import "sort"

// Router is one registered backend: its descriptor plus the process/proxy
// manager the core built for it.
type Router struct {
	Desc Descriptor
	Mgr  *Manager
}

var (
	registry = map[string]*Router{}
	order    []string // registry keys, kept sorted for stable UI order
)

// Register adds a router to the process-wide registry, constructing its
// Manager. Idempotent: a duplicate ID is ignored so a double blank-import
// can't panic. Called from each router subpackage's RegisterBuiltins.
func Register(d Descriptor) {
	if d.ID == "" {
		return
	}
	if _, ok := registry[d.ID]; ok {
		return
	}
	registry[d.ID] = &Router{Desc: d, Mgr: newManager(d)}
	order = append(order, d.ID)
	sort.Strings(order)
}

// Get returns the router with the given ID.
func Get(id string) (*Router, bool) {
	r, ok := registry[id]
	return r, ok
}

// List returns every registered router in stable (ID-sorted) order.
func List() []*Router {
	out := make([]*Router, 0, len(order))
	for _, id := range order {
		out = append(out, registry[id])
	}
	return out
}

// IDs returns the registered router IDs in stable order.
func IDs() []string {
	out := make([]string, len(order))
	copy(out, order)
	return out
}
