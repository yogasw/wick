package provider

import "sync"

// catalog.go defines the picker types and a registry. The actual entry
// lists live in each provider subpackage (claude/catalog.go,
// codex/catalog.go, gemini/catalog.go) and register themselves via
// RegisterCatalog in their init() — the same blank-import wiring the
// capability registries use. This keeps each provider's catalog next to
// its spawner instead of in one growing file here.
//
// The "catalog" powers the click-to-add env/args pickers on the provider
// detail page: operators pick a known env var or flag from a dropdown
// (with the right value widget per entry) instead of memorising names.
// Lists are sourced from each provider's own docs and are intentionally
// broad; the UI dropdown lets the operator search. Secrets (API keys)
// are represented as free-text string entries and masked by the
// secret-handling layer.

// CatalogValueKind tells the UI which input widget to render for an entry.
type CatalogValueKind string

const (
	// CatalogBool renders a true/false (or 1/0) dropdown. Options holds
	// the exact string values to offer; the first is the common default.
	CatalogBool CatalogValueKind = "bool"
	// CatalogEnum renders a dropdown of Options.
	CatalogEnum CatalogValueKind = "enum"
	// CatalogString renders a free-text input.
	CatalogString CatalogValueKind = "string"
	// CatalogInt renders a numeric input.
	CatalogInt CatalogValueKind = "int"
)

// CatalogEntry is one known env var or CLI arg an operator can add to a
// provider instance from the picker.
type CatalogEntry struct {
	// Key is the env var name (for env entries) or the flag (for args).
	Key string `json:"key"`
	// Description is a one-line explanation shown under the picker row.
	Description string `json:"description"`
	// Kind selects the value widget.
	Kind CatalogValueKind `json:"kind"`
	// Options are the selectable values for bool/enum. First = default.
	Options []string `json:"options,omitempty"`
	// Placeholder hints the expected value for string/int entries.
	Placeholder string `json:"placeholder,omitempty"`
}

// ProviderCatalog bundles the env + args pickers for one provider type.
type ProviderCatalog struct {
	Env  []CatalogEntry `json:"env"`
	Args []CatalogEntry `json:"args"`
}

var (
	catalogMu       sync.RWMutex
	catalogRegistry = map[Type]ProviderCatalog{}
)

// RegisterCatalog records the picker entries for a provider type. Called
// from each provider subpackage's init(). Last registration wins, so a
// subpackage fully owns its catalog.
func RegisterCatalog(t Type, c ProviderCatalog) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	catalogRegistry[t] = c
}

// CatalogFor returns the registered picker entries for a provider type.
// Unknown / unregistered types get an empty catalog (the manual KvList
// still works — the picker just has nothing to offer). The provider
// subpackages must be blank-imported for their init() to run; the
// agents UI module already does this alongside the capability wiring.
func CatalogFor(t Type) ProviderCatalog {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return catalogRegistry[t]
}
