package agents

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/internal/agents/provider/memscope/wrapper"
	"github.com/yogasw/wick/pkg/safeexec"
	"github.com/yogasw/wick/pkg/tool"
)

// wrapper_handler.go backs the path-shim controls on the Resources page.
//
// The shim is what extends a limit to agents wick did not start — a
// terminal session, or another service on this machine. Wick's own
// wrapping only ever reaches wick's own children, so without this an
// operator can enforce a ceiling and still watch the box run out of
// memory from an identical binary nobody was measuring.
//
// Writing the shim needs no privilege; pointing the system path at it
// does. That second step is returned as text for a person to run rather
// than executed here. A sudo call from an HTTP request hangs on a
// password prompt nobody can answer, and granting wick passwordless root
// would mean anyone who reaches this page can write to /usr/local/bin.

type wrapperProviderRow struct {
	Name    string `json:"name"`
	RealBin string `json:"real_bin"`
	Link    string `json:"link"`
	// Installed is true when the system path resolves to our shim, which
	// is the only thing that makes the shim take effect.
	Installed bool `json:"installed"`
	// LinkTarget is what the path resolves to now, so the page can say
	// what replaced the shim rather than only that it is gone.
	LinkTarget string `json:"link_target,omitempty"`
}

type wrapperStatusResponse struct {
	// Supported is false off Linux, where there are no cgroups to place
	// anything in. The page renders an explanation instead of controls.
	Supported bool   `json:"supported"`
	Notice    string `json:"notice,omitempty"`

	Providers []wrapperProviderRow `json:"providers"`
	Processes []wrapperProcRow     `json:"processes"`

	// Isolated / Unisolated is the whole verdict: a ceiling applies, or
	// it does not. Unisolated is the number that matters — those
	// processes share this machine's memory and nothing configured here
	// can stop one of them taking the rest of it.
	Isolated   int `json:"isolated"`
	Unisolated int `json:"unisolated"`
}

type wrapperProcRow struct {
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	RSSBytes uint64 `json:"rss_bytes"`
	Isolated bool   `json:"isolated"`
	FromWick bool   `json:"from_wick"`
}

// wrapperStatusHandler reports what is intercepted and what is running.
func wrapperStatusHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	res := wrapperStatusResponse{Supported: true}

	for _, p := range wrapper.Detect(safeexec.LookPath, 0) {
		row := wrapperProviderRow{Name: p.Name, RealBin: p.RealBin, Link: p.Link}
		if target, err := filepath.EvalSymlinks(p.Link); err == nil {
			row.LinkTarget = target
			row.Installed = wrapper.IsShim(target)
		}
		res.Providers = append(res.Providers, row)
	}

	procs, err := wrapper.Scan(wrapper.Providers, os.Getpid(), memscope.SliceName)
	if err != nil {
		// Not an error state: a platform without cgroups is a supported
		// configuration, and the page says so rather than showing zeros
		// that look like "nothing is running".
		res.Supported = false
		res.Notice = err.Error()
		c.JSON(http.StatusOK, res)
		return
	}
	s := wrapper.Summarize(procs)
	res.Isolated, res.Unisolated = s.Isolated, s.Unisolated

	sort.Slice(procs, func(i, j int) bool { return procs[i].RSSBytes > procs[j].RSSBytes })
	for _, p := range procs {
		res.Processes = append(res.Processes, wrapperProcRow{
			PID: p.PID, Name: p.Name, RSSBytes: p.RSSBytes,
			Isolated: p.Isolated, FromWick: p.FromWick,
		})
	}
	c.JSON(http.StatusOK, res)
}

type wrapperInstallRequest struct {
	// Providers limits the action; empty means every detected provider.
	Providers []string `json:"providers"`
	// LimitMB is the per-session ceiling. 0 creates the cgroup without
	// one, which is the measure-mode shape: a peak becomes readable and
	// nothing is ever killed for it.
	LimitMB int `json:"limit_mb"`
}

type wrapperActionResponse struct {
	// Written are the shim paths this call actually created.
	Written []string `json:"written,omitempty"`
	// Commands are the privileged steps left for a person to run, in the
	// order they must be run in.
	Commands []string `json:"commands"`
	Message  string   `json:"message"`
}

// wrapperInstallHandler writes the shims and returns the privileged step.
func wrapperInstallHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	var req wrapperInstallRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	targets := wrapper.Filter(wrapper.Detect(safeexec.LookPath, req.LimitMB), req.Providers)
	if len(targets) == 0 {
		c.JSON(http.StatusOK, wrapperActionResponse{
			Message: "no matching agent binary found on this machine",
		})
		return
	}

	dir := wrapper.ShimDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	l := log.With().Str("component", "resources").Logger()
	res := wrapperActionResponse{}
	stamp := time.Now().Format("20060102-150405")
	for _, p := range targets {
		path := filepath.Join(dir, p.Name)
		if err := os.WriteFile(path, []byte(wrapper.RenderShim(p, memscope.SliceName)), 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		l.Info().Str("provider", p.Name).Str("path", path).Int("limit_mb", p.LimitMB).
			Msg("resources: wrote agent isolation shim")
		res.Written = append(res.Written, path)
		res.Commands = append(res.Commands, wrapper.LinkCommands(p, dir, stamp)...)
	}
	// Membership is fixed at spawn: a session already running keeps its
	// placement until it ends. Saying so up front prevents "I installed
	// it and nothing changed".
	res.Message = "Shim written. Run the command below to point the system path at it, " +
		"then start a new session — sessions already running keep their current group."
	c.JSON(http.StatusOK, res)
}

// wrapperUninstallHandler returns the restore step, and removes the shim
// only after the caller has run it.
//
// Two calls rather than one because the order cannot be enforced from
// here: removing the shim while the system path still points at it makes
// EVERY spawn fail until the path is fixed. The restore command goes out
// first, and the file is removed on the second call.
func wrapperUninstallHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	var req struct {
		Providers []string `json:"providers"`
		// Confirmed is set by the second call, after the operator has run
		// the restore command.
		Confirmed bool `json:"confirmed"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	targets := wrapper.Filter(wrapper.Detect(safeexec.LookPath, 0), req.Providers)
	if len(targets) == 0 {
		c.JSON(http.StatusOK, wrapperActionResponse{Message: "nothing to remove"})
		return
	}

	res := wrapperActionResponse{}
	if !req.Confirmed {
		for _, p := range targets {
			res.Commands = append(res.Commands, wrapper.UnlinkCommands(p)...)
		}
		res.Message = "Run this first to point the system path back at the real binary. " +
			"Removing the shim before that would break every spawn in between."
		c.JSON(http.StatusOK, res)
		return
	}

	l := log.With().Str("component", "resources").Logger()
	for _, p := range targets {
		path := filepath.Join(wrapper.ShimDir(), p.Name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			l.Warn().Err(err).Str("path", path).Msg("resources: could not remove shim")
			continue
		}
		l.Info().Str("provider", p.Name).Msg("resources: removed agent isolation shim")
	}
	res.Message = "Shim removed. New sessions go back to whatever the guard applies on its own."
	c.JSON(http.StatusOK, res)
}
