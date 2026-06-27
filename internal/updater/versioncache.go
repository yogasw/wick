package updater

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// wickWebURL is where the version row in the user menu links to — the
// public wick site. "View the framework" lands here.
const wickWebURL = "https://yogasw.github.io/wick"

// VersionSnapshot is the cached version + update state shown in the user
// menu dropdown. It is a plain value type, refreshed in the background so
// the dropdown reads it with zero network cost. Fields are deliberately
// minimal — just versions and "is an update available" booleans, no
// changelog (the Software Update page owns the detail).
type VersionSnapshot struct {
	// App is this build's own version and whether its release source has a
	// newer one. AppVersion is the RAW version for display (e.g. "dev",
	// "v0.1.63", or a long pseudo-version) — the UI truncates it. AppDev
	// marks a non-release build (dev / pseudo-version) that can't be
	// compared to a release tag; the UI then shows a neutral "dev" badge
	// instead of Latest/Update. AppUpdateKnown is false when the app
	// updater isn't configured (no release source).
	AppVersion     string
	AppDev         bool
	AppLatest      string
	AppUpdate      bool
	AppUpdateKnown bool

	// Wick is the embedded framework version checked against the public
	// wick repo. WickVersion is RAW for display. WickDev marks a
	// dev/pseudo build (no comparison). WickUpdateKnown is false when the
	// network check failed.
	WickVersion     string
	WickDev         bool
	WickLatest      string
	WickUpdate      bool
	WickUpdateKnown bool

	// IsOfficial is true when the app's release source IS the canonical
	// wick repo — then app and framework are one codebase / one release and
	// the UI can collapse them to a single version line.
	IsOfficial bool

	// WickNotes is the wick framework changelog for the range
	// (current, latest] — markdown. Empty when up to date, on a dev build,
	// or when the changelog fetch failed. Cached here so the Software
	// Update page renders "What's new" without a live request (it may be up
	// to the refresh interval stale). WickChangelogURL is the full
	// changelog link.
	WickNotes        string
	WickPublishedAt  string
	WickChangelogURL string

	// WebURL is the link target for the version row (the public wick site).
	WebURL string
}

// VersionCache holds the latest VersionSnapshot behind an atomic pointer
// so the request path reads it lock-free. A background goroutine
// (Run) refreshes it on boot and on an interval; the dropdown never
// triggers a network call. Safe for concurrent use.
type VersionCache struct {
	coord   *Coordinator // app self-update source (may be non-configured)
	appRaw  string       // running app version, RAW (for display)
	wickRaw string       // embedded wick framework version, RAW (for display)

	snap atomic.Pointer[VersionSnapshot]

	mu      sync.Mutex
	refresh bool // a refresh is in flight (coalesce concurrent triggers)
}

// isDevVersion reports whether v is a non-release build that can't be
// compared to a release tag: empty, the literal "dev"/"unknown"
// sentinels, or a Go pseudo-version / dirty build (carries a pre-release
// timestamp segment like "-0.20260626…" or a "+dirty" build suffix). For
// these the UI shows a neutral "dev" badge rather than Latest/Update.
func isDevVersion(v string) bool {
	n := normalizeVer(v) // "" for dev/unknown/empty
	if n == "" {
		return true
	}
	// Pseudo-version (vX.Y.Z-0.<timestamp>-<hash>) or a build-metadata
	// suffix (+dirty / +<hash>) means a local/untagged build.
	return strings.Contains(n, "+") || strings.Contains(n, "-0.")
}

// baseSemver returns the plain "vX.Y.Z" core of a version, dropping any
// pre-release ("-…") and build-metadata ("+…") suffix. For a dev/pseudo
// build (v0.8.2-0.<ts>-<hash>+dirty) this yields the base release the
// build was cut from (v0.8.2), which is what to compare against the
// latest release tag — comparing the full pseudo-version would treat it
// as a pre-release of that base (i.e. older than it). Returns "" for
// sentinels like "dev"/"unknown".
func baseSemver(v string) string {
	n := normalizeVer(v) // "" for dev/unknown/empty, else "vX.Y.Z[-pre][+meta]"
	if n == "" {
		return ""
	}
	if i := strings.IndexAny(n, "-+"); i > 0 {
		return n[:i]
	}
	return n
}

// NewVersionCache builds a cache seeded with versions known at boot (no
// "latest" yet — that arrives on the first Refresh). coord is the app's
// self-update coordinator (used to learn the app's latest version when
// configured); appVersion/wickVersion are the running build's RAW
// versions (kept verbatim for display; comparison normalises internally).
func NewVersionCache(coord *Coordinator, appVersion, wickVersion string) *VersionCache {
	c := &VersionCache{
		coord:   coord,
		appRaw:  strings.TrimSpace(appVersion),
		wickRaw: strings.TrimSpace(wickVersion),
	}
	// Seed an initial snapshot so the dropdown has versions to show before
	// the first background refresh completes — just no update badges yet
	// (dev builds keep their neutral "dev" badge regardless).
	c.snap.Store(&VersionSnapshot{
		AppVersion:  c.appRaw,
		AppDev:      isDevVersion(c.appRaw),
		WickVersion: c.wickRaw,
		WickDev:     isDevVersion(c.wickRaw),
		WebURL:      wickWebURL,
	})
	return c
}

// Snapshot returns the current cached snapshot. Never nil. Cheap and
// lock-free — safe to call on every request.
func (c *VersionCache) Snapshot() VersionSnapshot {
	if s := c.snap.Load(); s != nil {
		return *s
	}
	return VersionSnapshot{
		AppVersion:  c.appRaw,
		AppDev:      isDevVersion(c.appRaw),
		WickVersion: c.wickRaw,
		WickDev:     isDevVersion(c.wickRaw),
		WebURL:      wickWebURL,
	}
}

// Refresh re-checks both the app's own release source (when configured)
// and the public wick framework repo, then publishes a new snapshot. It
// makes network calls — never call it from the request path; the
// background Run loop owns it. Concurrent calls coalesce (a second caller
// returns immediately while the first is in flight).
func (c *VersionCache) Refresh(ctx context.Context) {
	c.mu.Lock()
	if c.refresh {
		c.mu.Unlock()
		return
	}
	c.refresh = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.refresh = false
		c.mu.Unlock()
	}()

	next := VersionSnapshot{
		AppVersion:  c.appRaw,
		AppDev:      isDevVersion(c.appRaw),
		WickVersion: c.wickRaw,
		WickDev:     isDevVersion(c.wickRaw),
		WebURL:      wickWebURL,
	}

	// Official build: app release source IS the canonical wick repo, so app
	// and framework move together. Sourced from the app updater so the
	// whole "official vs downstream" decision lives here, in one place.
	if c.coord != nil {
		if upd := c.coord.Updater(); upd != nil {
			next.IsOfficial = upd.IsOfficial()
		}
	}

	// App update: knowable when the app's own updater is configured. Dev
	// builds ARE compared too — against the base release (CheckLatest reads
	// the updater's current version; a pseudo-version sorts below its base,
	// so a newer tag still registers as an update). The AppDev flag stays
	// so the UI shows a "dev" marker alongside the result.
	if c.coord != nil {
		if upd := c.coord.Updater(); upd != nil && upd.Configured() {
			if info, err := upd.CheckLatest(ctx); err == nil {
				next.AppLatest = info.Version
				next.AppUpdate = !info.AlreadyLatest
				next.AppUpdateKnown = true
			}
		}
	}

	// Wick framework: public repo, always checkable (no PAT). For a
	// dev/pseudo build, compare the BASE release it was cut from (v0.8.2)
	// against the latest tag — so a newer release still shows "Update
	// available" even on a local build. Reuse the same lightweight check
	// the Software Update page uses, and cache its changelog.
	wickCmp := c.wickRaw
	if next.WickDev {
		wickCmp = baseSemver(c.wickRaw)
	}
	if ws, err := CheckWickVersion(ctx, wickCmp); err == nil {
		next.WickLatest = ws.Latest
		next.WickUpdate = !ws.UpToDate
		next.WickUpdateKnown = true
		next.WickNotes = ws.ReleaseNotes
		next.WickPublishedAt = ws.PublishedAt
		next.WickChangelogURL = ws.ChangelogURL
	}

	c.snap.Store(&next)
}

// Run refreshes the cache once immediately, then on every interval tick
// until ctx is cancelled. Call it in a goroutine at startup. A typical
// interval is 6h — frequent enough to surface a release within a day,
// rare enough to be invisible load.
func (c *VersionCache) Run(ctx context.Context, interval time.Duration) {
	c.Refresh(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Refresh(ctx)
		}
	}
}
