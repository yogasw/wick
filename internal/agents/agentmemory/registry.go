package agentmemory

import "sort"

// Backend is one registered memory backend: its descriptor plus the daemon
// manager the core built for it.
type Backend struct {
	Desc Descriptor
	Mgr  *Manager
}

var (
	registry = map[string]*Backend{}
	order    []string // registry keys, kept sorted for stable UI order
)

// Register adds a backend to the process-wide registry, constructing its
// Manager. Idempotent: a duplicate ID is ignored so a double blank-import
// can't panic. Called from each backend subpackage's init.
func Register(d Descriptor) {
	if d.ID == "" {
		return
	}
	if _, ok := registry[d.ID]; ok {
		return
	}
	registry[d.ID] = &Backend{Desc: d, Mgr: newManager(d)}
	order = append(order, d.ID)
	sort.Strings(order)
}

// Get returns the backend with the given ID.
func Get(id string) (*Backend, bool) {
	b, ok := registry[id]
	return b, ok
}

// Resolve returns the backend an instance setting names, applying the same
// empty-means-default rule the spawn path uses (backendID). Callers outside
// this package want exactly that resolution — reimplementing it there is how
// the UI ends up describing a different backend than the one a spawn wires to.
func Resolve(id string) (*Backend, bool) { return Get(backendID(id)) }

// List returns every registered backend in stable (ID-sorted) order.
func List() []*Backend {
	out := make([]*Backend, 0, len(order))
	for _, id := range order {
		out = append(out, registry[id])
	}
	return out
}

// IDs returns the registered backend IDs in stable order.
func IDs() []string {
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// SetDataDir points every registered backend's daemon at one store on disk.
// The empty string means "each backend's own default". Called at boot from the
// daemon-level Agent Memory setting; a backend registered later keeps its
// default until the next call.
func SetDataDir(dir string) {
	for _, b := range List() {
		b.Mgr.SetDataDir(dir)
	}
}

// defaultBackendID is the backend an instance gets when it names none — the
// first one wick shipped, so instances configured before a second backend
// existed keep resolving.
const defaultBackendID = "ai-memory"

// backendID resolves the effective backend id, defaulting to ai-memory, then
// to the first registered backend.
func backendID(id string) string {
	if id != "" {
		return id
	}
	for _, bid := range IDs() {
		if bid == defaultBackendID {
			return bid
		}
	}
	if ids := IDs(); len(ids) > 0 {
		return ids[0]
	}
	return defaultBackendID
}
