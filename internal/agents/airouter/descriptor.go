// Package airouter manages one or more embedded "AI router" backends —
// local OpenAI-compatible proxy dashboards (9router, OmniRoute, …) that ship
// as npm packages and serve a web dashboard on a loopback port. airouter
// installs/updates each package on demand, starts/stops/restarts its process,
// and reverse-proxies its dashboard so it can be embedded in an iframe — the
// host never exposes the underlying port.
//
// Each router lives in its own subpackage (internal/agents/airouter/<id>) and
// registers a Descriptor via Register at init. The core is generic: adding a
// router is "new folder + Register", never a core edit. Multiple routers run
// concurrently, each on its own loopback port, mounted at /airouter/<id>/, so
// the UI can switch between them and check any of them live.
package airouter

import "github.com/yogasw/wick/internal/agents/provider"

// Descriptor is everything the core needs to manage one router backend. A
// router subpackage builds one and hands it to Register.
type Descriptor struct {
	// ID is the stable identifier used in the URL path (/airouter/<ID>) and
	// config keys (airouter_<ID>_autostart). Lowercase, no spaces.
	ID string
	// DisplayName is the human label shown in the switcher/menu.
	DisplayName string
	// Blurb is a one-line description shown under the title.
	Blurb string
	// NpmPackage is the npm package installed with `npm i -g <pkg>@latest`.
	NpmPackage string
	// BinName is the command resolved on PATH once installed (usually == NpmPackage).
	BinName string
	// PrefPort is the dashboard port the router prefers. When taken (two
	// routers default to the same port), the core remaps to the next free
	// loopback port at start.
	PrefPort int
	// IdentitySubstr, when set, is matched case-insensitively against the
	// name / short_name in the backend's /manifest.webmanifest to confirm that
	// a process wick did NOT spawn — one merely found listening on the port —
	// is really THIS router, not a different router that happens to hold the
	// same default port (both 9router and OmniRoute default to 20128). Empty =
	// skip the identity check and trust port-reachability alone.
	IdentitySubstr string
	// IconSVG is inline SVG markup (the inner shapes, no <svg> wrapper) for
	// the switcher tile. Empty = a default router glyph.
	IconSVG string
	// RoutePrefixes are this router SPA's app-specific top-level routes (root-
	// absolute, e.g. OmniRoute's "/home") that must be re-rooted under the
	// mount prefix in the rewrite pass — on top of baseRewritePrefixes. Add a
	// route here if a client-side navigation to it escapes to the wick root.
	RoutePrefixes []string
	// Launch builds the exec args + extra env to start the process listening
	// on `port`, bound to loopback. Routers differ: 9router takes --port /
	// --host flags, OmniRoute takes a PORT env var. The core resolves the bin
	// and prepends it; Launch returns only the args after the bin and any
	// extra env (KEY=VALUE) to add to the child environment.
	Launch func(port int) (args []string, env []string)
	// Hook contributes the CLI args + env an agent needs to route through this
	// router at spawn time. nil = this router can't be used as a spawn target.
	Hook SpawnHook
}

// SpawnHook lets a router inject what an agent CLI needs to route through it.
// It is the "on spawn, add what" extension point: the spawner calls one
// generic function and each router supplies its own base-URL wiring, env var
// names, and flags — so per-provider spawners barely change and differences
// between routers (and between agent types) are absorbed here.
type SpawnHook interface {
	// Contribute returns the extra args + env for agent type t routed through
	// this router. base is the wick-origin API base ("http://127.0.0.1:<port>/
	// airouter/<id>/v1"); key is the resolved plaintext API key. A router that
	// doesn't support t returns (nil, nil, nil).
	Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error)
	// Slots are the model-picker slots this router exposes for agent type t
	// (claude → opus/sonnet/haiku; codex → model/subagent). Empty = unsupported.
	Slots(t provider.Type) []provider.RouterSlot
	// DefaultKey is the credential used when the instance sets none. 9router
	// accepts a documented default ("sk_9router"); routers with no default
	// return "".
	DefaultKey() string
}
