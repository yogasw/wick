package agentmemory

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// "Has this backend spawned, or not?" — the question the panel has to answer
// in one glance without lying (Yoga, 2026-09-26: "maksut ku health check itu
// buat tau apakah ai memory udah spawn atau ngak gitu").
//
// It was answered from the configured PREFERENCE, and on this host that made
// it answer "Stopped" beside a daemon that was alive and serving on another
// port — the worst possible version of the control, because the next thing a
// person does with it is press Start, and that would have bound a second
// daemon on a second store. So the answer comes from the process list and the
// socket, and where wick cannot tell, it says it cannot tell: an unknown
// reported as "stopped" is what makes someone press the button.

// DaemonCheck is that answer, as the Health tab renders it.
type DaemonCheck struct {
	// Running is process-and-socket truth, not a stored flag.
	Running bool `json:"running"`
	// Managed is whether THIS wick process spawned it. Adopted is not a
	// defect, but it changes what wick can promise — no stdout to read, no
	// uptime it witnessed — so it is stated rather than papered over.
	Managed bool `json:"managed"`
	// Processes is every process of this backend wick can see. More than
	// one is a real and dangerous state, so the list is reported whole.
	Processes []DaemonProcess `json:"processes,omitempty"`
	// Port is the port it actually listens on; 0 when unknown or ambiguous.
	Port int `json:"port"`
	// PrefPort is where the NEXT start would try. Kept beside Port because
	// the two differing is normal and worth seeing.
	PrefPort int `json:"pref_port"`
	// Answering is whether the health path returned 200 just now. It is
	// separate from Running: a wedged daemon is running and silent, and the
	// two need different words.
	Answering bool `json:"answering"`
	// HealthPath is what was pinged, so the claim is checkable by hand.
	HealthPath string `json:"health_path,omitempty"`
	// SpawnsWithoutMemory is the consequence nobody could see: agents are
	// starting with no memory wiring because wick has no address for the
	// daemon. It is the finding, not a side note — a session that silently
	// forgets is the failure this whole feature exists to catch.
	SpawnsWithoutMemory bool `json:"spawns_without_memory"`
	// BriefingsOmitted counts sessions that opted into the pasted project
	// brief and got none, because the store could not be reached. Separate
	// from SpawnsWithoutMemory: those sessions have no memory wiring at all,
	// while these have the MCP tools and lost only the block that arrives
	// without being asked for.
	BriefingsOmitted int64 `json:"briefings_omitted"`
	// Verdict is the sentence for the state this backend is actually in.
	Verdict string `json:"verdict"`
}

// spawnsWithoutMemory counts spawns that were wired with nothing because no
// address could be resolved. A counter rather than a flag: "it happened once
// an hour ago" and "it is happening to every session" are different problems,
// and the second is the one that gets someone out of bed.
var spawnsWithoutMemory atomic.Int64

// noteSpawnWithoutMemory records one. Called from the spawn path.
func noteSpawnWithoutMemory() { spawnsWithoutMemory.Add(1) }

// RunDaemonCheck answers the question for one backend.
func RunDaemonCheck(be *Backend) DaemonCheck {
	procs := be.Mgr.Daemons()
	port := be.Mgr.BoundPort()
	c := DaemonCheck{
		Managed:             be.Mgr.spawnedHere(),
		Processes:           procs,
		Port:                port,
		PrefPort:            be.Mgr.PrefPort(),
		HealthPath:          be.Desc.HealthPath,
		SpawnsWithoutMemory: port == 0 && spawnsWithoutMemory.Load() > 0,
		BriefingsOmitted:    briefingsOmitted.Load(),
	}
	c.Running = c.Managed || len(procs) > 0
	if port > 0 {
		c.Answering = be.Mgr.probeHealth()
	}
	c.Verdict = daemonVerdict(be.Desc.DisplayName, c)
	return c
}

// daemonVerdict is the sentence. Four states, and the fourth is the one that
// used to be missing: wick cannot tell.
func daemonVerdict(name string, c DaemonCheck) string {
	switch {
	case len(c.Processes) > 1:
		// The state this host was actually in. Two daemons usually means two
		// stores, so one of them is being written to and the other read.
		return fmt.Sprintf("%d %s daemons are running at once (%s). Agents reach whichever one wick resolved, and if they use different stores the memory you read is not the memory being written. Stop the one that should not be there.",
			len(c.Processes), name, describeDaemons(c.Processes))
	case c.Running && c.Answering:
		who := "wick started it"
		if !c.Managed {
			who = "wick did not start it — it was already running, so there is no output to show and no uptime wick witnessed"
		}
		extra := ""
		if c.PrefPort > 0 && c.PrefPort != c.Port {
			extra = fmt.Sprintf(" Its preferred port is %d, which is where the next start would try.", c.PrefPort)
		}
		return fmt.Sprintf("%s is running on port %d and answering %s. %s.%s", name, c.Port, c.HealthPath, who, extra)
	case c.Running:
		return fmt.Sprintf("%s is running on port %d but not answering %s. The process is there and wedged, rather than gone.",
			name, c.Port, c.HealthPath)
	case c.SpawnsWithoutMemory:
		return fmt.Sprintf("wick cannot tell whether %s is running: no process of it is visible and no port is known, so agents are spawning with NO memory — they neither recall nor record. Start it from this panel.", name)
	default:
		return fmt.Sprintf("%s is not running. No process of it is visible, so nothing is recalling or recording.", name)
	}
}

// DaemonCheckSummary is the one-line form, for a log or a message.
func DaemonCheckSummary(c DaemonCheck) string {
	return strings.TrimSpace(c.Verdict)
}
