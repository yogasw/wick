// Package wrapper installs and removes the shim that places each agent
// spawn in its own cgroup, for hosts where wick's built-in guard is not
// the thing doing it.
//
// Two layers, and both are load-bearing:
//
//	<bindir>/<provider>        the shim script itself, owned by the user
//	/usr/local/bin/<provider>  a symlink to it, which is what gets found
//
// The symlink is the interception point rather than the provider's
// configured Binary path because provider.Find() reads an in-memory
// cache, invalidated only from inside the process — a path changed
// through the UI may not take effect until a restart. exec.LookPath
// touches the filesystem on every spawn, so a symlink cannot be stale.
//
// What this CANNOT catch, and says so in Status: a caller that runs the
// real binary by absolute path never passes through the symlink. That is
// not hypothetical — on the host this was built for, the "claude" binary
// lives inside another service's directory, and that service invokes it
// directly. Interception by name only reaches callers who go by name.
package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Provider is one program to place behind a shim.
type Provider struct {
	// Name is the command as callers type it: "claude", "codex".
	Name string
	// RealBin is the actual executable the shim execs. Resolved from the
	// provider's configured Binary, or from PATH, and overridable —
	// detection is right often enough to be the default and wrong often
	// enough that a flag has to exist.
	RealBin string
	// Link is the path whose lookup this intercepts, normally
	// /usr/local/bin/<name>.
	Link string
	// LimitMB is the per-spawn ceiling. 0 means the shim creates the
	// scope but applies no ceiling — the measure-mode shape.
	LimitMB int
}

// RenderShim returns the shim script for one provider.
//
// MemoryHigh=infinity and MemorySwapMax=0 are not tuning knobs and are
// written at every ceiling including none. MemoryHigh throttles
// allocation rather than killing: a process past it stalls indefinitely
// while holding its slot, which is how one production incident became a
// 116-minute outage instead of a clean kill.
//
// The two escape hatches are deliberate. AGENT_NO_CGROUP lets an
// operator bypass the shim for one command without uninstalling it, and
// a missing systemd-run or XDG_RUNTIME_DIR falls through to the real
// binary rather than failing the spawn — an unguarded agent beats no
// agent at all, which is the same trade memguard.go makes.
func RenderShim(p Provider, slice string) string {
	if slice == "" {
		slice = "agents.slice"
	}
	var limit string
	if p.LimitMB > 0 {
		limit = fmt.Sprintf(" -p MemoryMax=%dM", p.LimitMB)
	}
	// $$ is the shim shell's pid: unique while the scope is alive, which
	// is all systemd requires of a transient unit name.
	return fmt.Sprintf(`#!/bin/sh
# Installed by wick: run each %[1]s session in its own cgroup.
# Remove with: wick memory wrapper uninstall %[1]s
REAL=%[2]s

[ -n "$AGENT_NO_CGROUP" ] && exec "$REAL" "$@"
if ! command -v systemd-run >/dev/null 2>&1 || [ -z "$XDG_RUNTIME_DIR" ]; then
    exec "$REAL" "$@"
fi

exec systemd-run --user --scope --quiet --collect \
    --slice=%[3]s --unit="%[1]s-agent-$$" \
   %[4]s -p MemoryHigh=infinity -p MemorySwapMax=0 \
    -- "$REAL" "$@"
`, p.Name, p.RealBin, slice, limit)
}

// LinkCommands returns the shell commands that point Link at the shim,
// for an operator to run themselves.
//
// Printed rather than executed. /usr/local/bin needs root, wick does not
// run as root, and a sudo call from a web request or a non-interactive
// service hangs on a password prompt that nobody can answer. Printing
// also shows exactly what is about to change before it changes.
func LinkCommands(p Provider, bindir, stamp string) []string {
	// path.Join, never filepath.Join: these commands are for the shell on
	// the target host, which is POSIX. filepath.Join uses the separator
	// of whatever machine wick runs on, so a wick on Windows managing a
	// Linux host would emit backslashes and hand the operator a command
	// that cannot work.
	shim := posixJoin(bindir, p.Name)
	return []string{
		// Back up whatever is there first: on this path the existing
		// entry is usually a symlink into a node install, and `npm i -g`
		// will happily replace it again later.
		fmt.Sprintf("sudo cp -a %s %s.orig-%s 2>/dev/null || true", sh(p.Link), sh(p.Link), stamp),
		fmt.Sprintf("sudo ln -sfn %s %s", sh(shim), sh(p.Link)),
	}
}

// UnlinkCommands returns the commands that restore Link to the real
// binary.
//
// Restore comes before removing the shim, never after. Reversed, there
// is a window where the symlink points at a file that no longer exists
// and EVERY spawn fails — a worse state than the one being undone.
func UnlinkCommands(p Provider) []string {
	return []string{
		fmt.Sprintf("sudo ln -sfn %s %s", sh(p.RealBin), sh(p.Link)),
	}
}

// sh quotes a path for a shell command line.
func sh(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'$&|;<>()*?[]#~`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// posixJoin joins path segments with "/" regardless of host OS, because
// the result is consumed by a POSIX shell rather than by this process.
func posixJoin(dir, name string) string {
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// Providers are the agent CLIs worth shimming. Runtimes an agent spawns
// (node, python3) are deliberately absent: they are what an agent starts,
// not what starts an agent, and shimming them would intercept unrelated
// programs across the whole machine.
var Providers = []string{"claude", "codex", "gemini"}

// LinkDir is the directory a lookup by name finds first. Not
// configurable: a shim installed somewhere PATH does not reach is a shim
// that silently covers nothing.
const LinkDir = "/usr/local/bin"

// ShimDir is where the shim scripts live: user-owned, so writing them
// needs no privilege. Only the symlink into a system path does.
func ShimDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "wick-bin")
	}
	return filepath.Join(home, ".local", "share", "wick", "bin")
}

// Detect resolves each known provider to its real binary.
//
// By PATH lookup rather than a hardcoded table: the paths differ per host
// — on one machine the agent binary lives inside an unrelated service's
// data directory — and a table baked into wick would be right on exactly
// one machine. lookPath is a parameter so the caller supplies the repo's
// safeexec wrapper without this package importing it.
func Detect(lookPath func(string) (string, error), limitMB int) []Provider {
	var out []Provider
	for _, name := range Providers {
		real, err := lookPath(name)
		if err != nil {
			continue
		}
		// Resolve through the link, so re-installing over an existing
		// shim does not point the shim at itself and loop forever.
		if resolved, err := filepath.EvalSymlinks(real); err == nil {
			if !strings.HasPrefix(resolved, ShimDir()) {
				real = resolved
			}
		}
		out = append(out, Provider{
			Name: name, RealBin: real, Link: LinkDir + "/" + name, LimitMB: limitMB,
		})
	}
	return out
}

// Filter narrows a detected set to the named providers. Empty names mean
// all of them, so a caller that passes nothing acts on everything.
func Filter(all []Provider, names []string) []Provider {
	if len(names) == 0 {
		return all
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Provider
	for _, p := range all {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// IsShim reports whether a resolved path is one of ours, which is what
// makes the difference between a shim that is installed and one that is
// merely written to disk.
func IsShim(resolvedPath string) bool {
	return resolvedPath != "" && strings.HasPrefix(resolvedPath, ShimDir())
}
