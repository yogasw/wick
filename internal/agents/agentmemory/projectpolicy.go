package agentmemory

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	errNoPolicyStore = errors.New("agentmemory: no per-project policy store is wired on this host")
	errBadPolicy     = errors.New(`agentmemory: a project's memory policy must be "", "on" or "off"`)
)

// Per-project Agent Memory (Yoga, 2026-09-25: "jadi aku bisa test 1 project
// buat aktifin ai memory nya").
//
// The switches that existed before this were the master switch and the
// per-provider-instance toggle, and an instance spans every project — so
// there was no way to try the feature on one project without turning it on
// everywhere. This is the missing narrowing, and it is a NARROWING only: a
// project can keep memory out, and it can opt itself into a trial, but it
// cannot turn memory on for an instance that is not wired to a backend. The
// instance holds the server URL and the credentials; a project holds an
// opinion about itself.
//
// The rule, in one place so nothing has to re-derive it:
//
//	explicit "off"                     → no memory in this project, always
//	explicit "on"                      → memory, and the host is in TRIAL mode
//	unset, and some project is "on"    → no memory (that is what a trial is)
//	unset, and no project is "on"      → follow the provider instance
//
// The trial clause is what makes one project testable without a third global
// switch — two switches that can disagree about whether something should be
// running is the bug, not the feature (PLAN §25.1 made the same argument for
// the watchdog). It is derived, not stored, and the panel states it wherever
// it bites.

// ProjectPolicy is one project's setting, as the panel reads and writes it.
type ProjectPolicy struct {
	// Value is "", "on" or "off" — what this project itself says.
	Value string `json:"value"`
	// Allowed is the answer after the whole rule is applied: whether a
	// session in this project gets memory at all.
	Allowed bool `json:"allowed"`
	// TrialMode is true while at least one project has opted in, which is
	// what turns every unset project off.
	TrialMode bool `json:"trial_mode"`
	// TrialProjects names the opted-in projects, so "why is my other
	// project silent?" is answered on the screen rather than by reading
	// this comment.
	TrialProjects []string `json:"trial_projects,omitempty"`
	// Reason is the sentence the panel shows for the state this project is
	// actually in.
	Reason string `json:"reason"`
}

// Policy values, mirroring project.Memory*. The constants are duplicated
// rather than imported because this package must not import the project
// store — the same rule that keeps the registry out of here (see
// projectLister). They are three string literals and a test pins them.
const (
	PolicyUnset = ""
	PolicyOn    = "on"
	PolicyOff   = "off"
)

// ProjectPolicyStore is how this package reads and writes a project's
// setting without importing the project store. Injected at boot, like the
// project lister next door.
type ProjectPolicyStore interface {
	// Policy returns one project's stored value ("", "on", "off") and
	// whether the project exists at all.
	Policy(id string) (string, bool)
	// SetPolicy persists a value.
	SetPolicy(id, value string) error
	// All returns every project's value, keyed by id — what the trial
	// rule is derived from.
	All() map[string]string
}

var (
	policyMu    sync.RWMutex
	policyStore ProjectPolicyStore
)

// SetProjectPolicyStore wires the per-project setting. Unwired means every
// project follows its provider instance, which is exactly how the feature
// behaved before this existed.
func SetProjectPolicyStore(s ProjectPolicyStore) {
	policyMu.Lock()
	policyStore = s
	policyMu.Unlock()
}

func policies() ProjectPolicyStore {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return policyStore
}

// ProjectAllowed reports whether a session in project id may use memory.
//
// An unknown id follows the instance: wick spawns sessions outside any
// project (and in tests), and refusing those would switch the feature off
// for everyone the moment this file was wired in.
func ProjectAllowed(id string) bool {
	return PolicyFor(id).Allowed
}

// PolicyFor is the whole rule, with the words for it.
func PolicyFor(id string) ProjectPolicy {
	st := policies()
	if st == nil {
		return ProjectPolicy{Allowed: true, Reason: "Memory follows the provider instance — wick has no per-project setting wired on this host."}
	}
	all := st.All()
	trial := optedIn(all)
	p := ProjectPolicy{TrialMode: len(trial) > 0, TrialProjects: trial}

	value, known := st.Policy(strings.TrimSpace(id))
	p.Value = value
	switch {
	case value == PolicyOff:
		p.Allowed = false
		p.Reason = "Agent Memory is off for this project: sessions here neither record nor recall, whatever the provider instance says."
	case value == PolicyOn:
		p.Allowed = true
		p.Reason = "Agent Memory is on for this project."
	case !known:
		// Not a project wick knows — a session outside any project, or a
		// test. Follow the instance rather than inventing a policy.
		p.Allowed = true
		p.Reason = "Memory follows the provider instance."
	case p.TrialMode:
		p.Allowed = false
		p.Reason = "Agent Memory is being trialled on " + strings.Join(trial, ", ") +
			", so projects that have not opted in are off. Turn it on here to include this project."
	default:
		p.Allowed = true
		p.Reason = "Memory follows the provider instance: whichever agents are wired to a backend record and recall here."
	}
	return p
}

// optedIn lists the projects that explicitly turned memory on, sorted so the
// sentence the panel shows is stable between reads.
func optedIn(all map[string]string) []string {
	var out []string
	for id, v := range all {
		if v == PolicyOn {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// SetProjectPolicy stores a project's value, refusing anything that is not
// one of the three.
func SetProjectPolicy(id, value string) error {
	st := policies()
	if st == nil {
		return errNoPolicyStore
	}
	switch value {
	case PolicyUnset, PolicyOn, PolicyOff:
	default:
		return errBadPolicy
	}
	return st.SetPolicy(strings.TrimSpace(id), value)
}

// ProjectAllowedForFolder is the spawn-time question: a session is starting
// in this directory — may it use memory?
//
// The folder is what a spawn has; the project id is what the policy is keyed
// by. The mapping is wick's own project list, the same one the collision
// check reads (collision.go), so nothing here guesses.
func ProjectAllowedForFolder(folder string) bool {
	id, ok := projectIDForFolder(folder)
	if !ok {
		// A session outside any wick project. It follows the instance —
		// the trial clause is about projects, and this is not one.
		return true
	}
	return ProjectAllowed(id)
}

// projectIDForFolder maps a session's working directory onto a wick project.
// It matches the project's folder exactly and then by prefix, because a
// session runs in the project's folder or somewhere under it.
func projectIDForFolder(folder string) (string, bool) {
	folder = strings.TrimRight(strings.TrimSpace(folder), "/\\")
	if folder == "" || projectLister == nil {
		return "", false
	}
	var best ProjectFolder
	for _, p := range projectLister() {
		pf := strings.TrimRight(strings.TrimSpace(p.Folder), "/\\")
		if pf == "" {
			continue
		}
		if folder == pf {
			return p.ID, true
		}
		// The longest matching parent wins: a project nested inside
		// another's folder is still its own project.
		if strings.HasPrefix(folder, pf+"/") && len(pf) > len(best.Folder) {
			best = p
		}
	}
	if best.ID != "" {
		return best.ID, true
	}
	return "", false
}
