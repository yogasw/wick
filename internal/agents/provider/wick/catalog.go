package wick

import (
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// init registers an intentionally empty picker catalog for wick.
//
// The env/args pickers exist for CLI providers where configuration
// happens via environment variables and flags. Wick's configuration
// surface is the Models table + Provider settings card instead —
// API keys live per-model (encrypted), generation knobs live in
// WickGenConfig/RawConfig. Registering the empty catalog (rather than
// leaving the type unregistered) documents that this is a decision,
// not an omission.
func init() {
	provider.RegisterCatalog(provider.TypeWick, provider.ProviderCatalog{})
}
