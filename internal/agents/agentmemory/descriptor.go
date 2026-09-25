// Package agentmemory manages one or more "agent memory" backends — local
// servers (ai-memory, …) that hold what agents learned across sessions and
// hand it back over MCP, so context survives a session ending and a switch
// from one provider CLI to another. agentmemory starts/stops each backend's
// daemon on a loopback port, probes its health, and contributes the per-spawn
// wiring an agent needs to reach it.
//
// Each backend lives in its own subpackage (internal/agents/agentmemory/<id>)
// and registers a Descriptor via Register at init. The core is generic: adding
// a backend is "new folder + Register", never a core edit.
//
// Sibling of internal/agents/airouter, with two deliberate differences: a
// backend here is installed from a GitHub release rather than npm (see
// InstallKind), and there is no dashboard reverse proxy — the panel is built
// on wick's own FE against the backend's API.
package agentmemory

import "github.com/yogasw/wick/internal/agents/provider"

// InstallKind is how a backend's binary gets onto the machine. Unlike the
// routers next door, memory backends aren't npm packages — ai-memory is a Rust
// binary published as a GitHub release asset.
type InstallKind string

const (
	// InstallGitHubRelease downloads the per-arch asset from ReleaseRepo's
	// latest release. See Manager.Install for the current state of this.
	InstallGitHubRelease InstallKind = "github-release"
	// InstallManual means wick never installs the backend — the user puts
	// the binary on PATH themselves. Also the zero value's meaning for a
	// descriptor that leaves ReleaseRepo empty.
	InstallManual InstallKind = "manual"
)

// Descriptor is everything the core needs to manage one memory backend. A
// backend subpackage builds one and hands it to Register.
type Descriptor struct {
	// ID is the stable identifier used in config keys
	// (agentmemory_<ID>_autostart) and in Instance.AgentMemoryProvider.
	// Lowercase, no spaces.
	ID string
	// DisplayName is the human label shown in the switcher/menu.
	DisplayName string
	// Blurb is a one-line description shown under the title.
	Blurb string
	// GitHubURL is the backend's upstream repo, surfaced next to its name as
	// an info/GitHub icon so the user can see what they are running.
	GitHubURL string
	// IconSVG is inline SVG markup (the inner shapes, no <svg> wrapper) for
	// the switcher tile. Empty = a default memory glyph.
	IconSVG string
	// InstallKind is how wick obtains the binary. Empty = InstallManual.
	InstallKind InstallKind
	// ReleaseRepo is the "owner/name" the release asset is downloaded from
	// when InstallKind is InstallGitHubRelease.
	ReleaseRepo string
	// BinName is the command resolved on PATH once installed.
	BinName string
	// PrefPort is the port the daemon prefers. When taken (another backend,
	// or an unrelated process), the core remaps to the next free loopback
	// port at start.
	PrefPort int
	// HealthPath is the path probed to decide the daemon is up, e.g.
	// "/healthz". It must answer 200 when healthy. Empty = fall back to a
	// plain TCP connect, which only proves *something* holds the port.
	HealthPath string
	// Launch builds the exec args + extra env to start the daemon with the
	// resolved options. The core resolves the bin and prepends it; Launch
	// returns only the args after the bin and any extra env (KEY=VALUE).
	//
	// It takes LaunchOptions rather than a bare port because the store
	// location and the optional web UI are per-daemon decisions the core
	// resolves (from the instances using this backend), not constants the
	// descriptor can bake in.
	Launch func(opt LaunchOptions) (args, env []string)
	// Hook contributes the CLI args + env an agent needs to reach this
	// backend at spawn time. nil = this backend can't be used as a spawn
	// target.
	Hook SpawnHook
	// Data reads the panel's numbers out of this backend (see DataSource).
	// nil = the backend can be managed but has nothing to show, and the
	// data endpoints answer 501 rather than an empty dashboard that looks
	// like an empty store.
	Data DataSource
}

// LaunchOptions are the knobs the core resolves before starting a daemon.
type LaunchOptions struct {
	// Port is the loopback port the daemon must bind. Already remapped when
	// PrefPort was taken.
	Port int
	// DataDir is the store location. Empty = the backend's own default.
	DataDir string
	// EnableWeb mounts the backend's own HTTP API + web UI. On by default
	// for a wick-managed daemon — the panel reads its project list and
	// search from that API (see Settings.EnableWeb).
	EnableWeb bool
	// AuthToken is the bearer token the daemon should require, already
	// decrypted. Empty = run without auth.
	AuthToken string
	// Tuning is the rest of the resolved settings. A Launch implementation
	// translates the fields its backend understands into flags or
	// environment and IGNORES the rest — an unset field must not turn into
	// a zero the daemon then treats as a real setting (see Tuning).
	Tuning Tuning
}

// SpawnConn is everything a backend needs to wire one spawn to its daemon.
//
// It carries more than airouter's equivalent pair of (base, key) strings, and
// on purpose: a router is only ever a URL to call, while a memory backend also
// RECORDS. ai-memory records through hooks the agent CLI itself executes, and
// those hooks are invocations of the backend's own binary against the daemon's
// own store — so the wiring needs the resolved binary path and the store, not
// just the endpoint.
type SpawnConn struct {
	// ServerURL is the resolved base URL, e.g. "http://127.0.0.1:49374",
	// without a trailing slash so callers can append a path.
	ServerURL string
	// AuthKey is the resolved plaintext auth token, "" when the backend runs
	// without auth. It belongs in env, never in argv — argv is logged.
	AuthKey string
	// BinPath is the absolute path of the backend's binary, "" when it does
	// not resolve. A hook command must name the path rather than the bare
	// command: it is run by the AGENT's shell, whose PATH is not wick's.
	BinPath string
	// DataDir is the daemon's store on disk, "" = the backend's own default.
	// It is a daemon-level setting (PLAN §19), not a per-instance one.
	DataDir string
	// Tuning is the daemon's resolved configuration. A spawn needs it
	// because some of it is not the daemon's to hold: capture mode, project
	// strategy and the hook half of assistant capture only exist on the hook
	// command line a spawn generates (see Tuning.CaptureMode).
	Tuning Tuning
}

// SpawnHook lets a backend inject what an agent CLI needs to reach it. It is
// the "on spawn, add what" extension point: the spawner calls one generic
// function and each backend supplies its own MCP wiring, env var names, and
// flags — so per-provider spawners barely change and differences between
// backends (and between agent types) are absorbed here.
type SpawnHook interface {
	// Contribute returns the extra args + env for agent type t wired to this
	// backend over conn. A backend that doesn't support t returns
	// (nil, nil, nil).
	Contribute(t provider.Type, ins provider.Instance, conn SpawnConn) (args, env []string, err error)
}
