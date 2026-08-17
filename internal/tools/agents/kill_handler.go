package agents

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/pkg/memreport"
	"github.com/yogasw/wick/pkg/tool"
)

// kill_handler.go ends a process from the Resources page.
//
// This is the one destructive thing the page can do, so the guards matter
// more than the feature:
//
//   - admin only, like the rest of this API;
//   - never wick itself, which would take the dashboard down with it;
//   - never PID 1, which would take the whole machine or container down;
//   - a group kill names every PID it will end, and stops at a cap.
//
// It sends SIGTERM, not SIGKILL. A browser or an editor asked politely
// will close its files and flush its state; killed outright it may leave a
// corrupt profile behind. Anything that ignores the signal can be killed
// again from the operator's own shell, which is the right place for that
// decision.

// maxGroupKill bounds one group kill. A browser can hold dozens of
// processes, and "end 40 things at once" from a dashboard click is a
// bigger action than the click communicates.
const maxGroupKill = 25

type killRequest struct {
	PID int `json:"pid"`
	// Name kills every process sharing this executable name. Mutually
	// exclusive with PID; the UI sends one or the other.
	Name string `json:"name"`
}

type killResult struct {
	Killed  []int    `json:"killed"`
	Failed  []int    `json:"failed"`
	Skipped []string `json:"skipped,omitempty"`
	Message string   `json:"message"`
}

// killProcessHandler ends one process or a whole name group.
func killProcessHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	var req killRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	procs, err := memreport.Snapshot()
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "process control is unavailable on this platform",
		})
		return
	}

	l := log.With().Str("component", "resources").Logger()

	targets, skipped := selectKillTargets(procs, req, os.Getpid())
	if len(targets) == 0 {
		msg := "nothing to end"
		if len(skipped) > 0 {
			msg = strings.Join(skipped, "; ")
		}
		// Logged, not silent. A refusal answers 200 with an explanation in
		// the body, so without this line the server side of a "why didn't
		// it die?" question is a blank — the access log shows a successful
		// POST and nothing else.
		l.Info().
			Int("req_pid", req.PID).
			Str("req_name", req.Name).
			Str("reason", msg).
			Msg("resources: kill request ended nothing")
		c.JSON(http.StatusOK, killResult{Message: msg, Skipped: skipped})
		return
	}

	res := killResult{Skipped: skipped}
	for _, pid := range targets {
		if err := terminate(pid); err != nil {
			l.Warn().Err(err).Int("pid", pid).Msg("resources: could not end process")
			res.Failed = append(res.Failed, pid)
			continue
		}
		l.Info().Int("pid", pid).Msg("resources: process ended by operator")
		res.Killed = append(res.Killed, pid)
	}

	switch {
	case len(res.Failed) == 0:
		res.Message = "asked " + strconv.Itoa(len(res.Killed)) + " process(es) to close"
	case len(res.Killed) == 0:
		res.Message = "could not end any of them — they may belong to another user"
	default:
		res.Message = strconv.Itoa(len(res.Killed)) + " ended, " +
			strconv.Itoa(len(res.Failed)) + " refused"
	}
	c.JSON(http.StatusOK, res)
}

// selectKillTargets resolves a request to the PIDs that may actually be
// ended, plus human-readable reasons for anything held back.
//
// Pure so the refusals — the part that must not regress — are testable
// without ending real processes.
func selectKillTargets(procs []memreport.Proc, req killRequest, selfPID int) (targets []int, skipped []string) {
	match := func(p memreport.Proc) bool {
		if req.PID != 0 {
			return p.PID == req.PID
		}
		return req.Name != "" && p.Name == req.Name
	}

	for _, p := range procs {
		if !match(p) {
			continue
		}
		// Killing wick would take the dashboard down with it, and the
		// operator would be left with no way to see what happened.
		if p.PID == selfPID {
			// Named and explained: the operator is looking at a row for a
			// process that will not die, and "skipped wick itself" alone
			// reads as a bug rather than a rule.
			skipped = append(skipped,
				"pid "+strconv.Itoa(p.PID)+" is this wick server — ending it would close this page")
			continue
		}
		// PID 1 is init: ending it takes down the machine or container.
		if p.PID == 1 {
			skipped = append(skipped, "skipped pid 1 (init)")
			continue
		}
		// A zombie has already exited; the kernel discards signals sent to
		// it. Only its parent calling wait() clears the entry. Sending
		// SIGTERM would report success and change nothing, which is how a
		// process that "cannot be killed" appears to keep coming back.
		if p.Kind == memreport.KindZombie {
			skipped = append(skipped,
				"pid "+strconv.Itoa(p.PID)+" ("+p.Name+") has already exited — "+
					"it is waiting for its parent to reap it, and signals to it do nothing")
			continue
		}
		// A kernel thread has no user address space and does not accept
		// signals the way a process does.
		if p.Kind == memreport.KindKernel {
			skipped = append(skipped,
				"pid "+strconv.Itoa(p.PID)+" ("+p.Name+") is a kernel thread and cannot be ended")
			continue
		}
		targets = append(targets, p.PID)
	}

	if len(targets) > maxGroupKill {
		skipped = append(skipped,
			"stopped at "+strconv.Itoa(maxGroupKill)+" of "+strconv.Itoa(len(targets))+
				" — run it again to continue")
		targets = targets[:maxGroupKill]
	}
	return targets, skipped
}
