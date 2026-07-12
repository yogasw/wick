package airouter

import "context"

// ConfigStore persists the small set of airouter knobs and answers the access
// questions the control endpoints need. The hosting package backs it with the
// app's config service + auth so this package stays free of storage/login
// imports. Per-router knobs are keyed by router ID.
type ConfigStore interface {
	// Enabled is the master switch — false disables every router surface.
	Enabled() bool
	// AccessAllowed reports whether the request's user may drive controls
	// (admin only today).
	AccessAllowed(ctx context.Context) bool
	// GetAutostart reports whether router id should auto-start on boot.
	GetAutostart(id string) bool
	SetAutostart(ctx context.Context, id string, on bool) error
	// GetExternalAPI reports the per-router external-API toggle.
	GetExternalAPI(id string) bool
	SetExternalAPI(ctx context.Context, id string, on bool) error
	// ExternalAPIAllowed reports whether router id's /v1 API may be reached
	// off-machine (master switch AND the per-router toggle).
	ExternalAPIAllowed(id string) bool
}
