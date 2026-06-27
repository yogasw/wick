package ui

import "context"

// VersionInfo is the minimal version + update state the user-menu
// dropdown renders. It is populated from a background-refreshed cache
// (see internal/updater.VersionCache) and injected per request via
// WithVersionInfo, so the dropdown reads it with zero network cost.
//
// This type is intentionally defined in the ui package (not imported
// from updater) to keep the view layer free of a dependency on the
// updater package — the server maps the cache's snapshot into this.
type VersionInfo struct {
	AppVersion     string // running app version, RAW for display (e.g. "v0.1.63" or "dev")
	AppDev         bool   // dev/pseudo build — show a neutral "dev" badge, no comparison
	AppLatest      string // latest app release, when known
	AppUpdate      bool   // a newer app release is available
	AppUpdateKnown bool   // app update state is known (release source configured)

	WickVersion     string // embedded wick framework version, RAW for display
	WickDev         bool   // dev/pseudo build — show a neutral "dev" badge
	WickLatest      string // latest wick release, when known
	WickUpdate      bool   // a newer wick release is available
	WickUpdateKnown bool   // wick update state is known (check succeeded)

	// IsOfficial: the app's release source IS the canonical wick repo, so
	// app == framework == one release. The dropdown then shows a SINGLE
	// version line ("Wick") instead of separate App + Wick rows.
	IsOfficial bool

	WebURL string // link target for the version row (public wick site)
}

// AnyUpdate reports whether either the app or the wick framework has a
// known available update — used to show a single "update available" dot
// on the avatar/menu without the user opening the dropdown.
func (v VersionInfo) AnyUpdate() bool {
	return (v.AppUpdateKnown && v.AppUpdate) || (v.WickUpdateKnown && v.WickUpdate)
}

type versionInfoCtxKey struct{}

// WithVersionInfo stores the version snapshot in ctx so the user-menu
// dropdown can render it without an explicit parameter (mirrors
// WithAppName).
func WithVersionInfo(ctx context.Context, v VersionInfo) context.Context {
	return context.WithValue(ctx, versionInfoCtxKey{}, v)
}

// VersionInfoFromContext returns the version snapshot set via
// WithVersionInfo, or a zero value (no versions, no badges) when none was
// set — the dropdown then simply omits the version section.
func VersionInfoFromContext(ctx context.Context) VersionInfo {
	v, _ := ctx.Value(versionInfoCtxKey{}).(VersionInfo)
	return v
}
