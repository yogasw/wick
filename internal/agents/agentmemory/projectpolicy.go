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

// ── the roster ───────────────────────────────────────────────────────

// ProjectPolicyRow is one project's answer to the question an operator who
// inherits this host actually asks: is this project recording, and if not,
// why not?
//
// It carries the NAME as well as the id. A trial reported as a list of uuids
// is a trial nobody can act on — the id is what the policy is keyed by, the
// name is what the person recognises, and both belong in the answer.
type ProjectPolicyRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Value is what this project itself says: "", "on" or "off".
	Value string `json:"value"`
	// Recording is the resolved answer after the trial rule is applied.
	Recording bool `json:"recording"`
	// Reason is the sentence for this row's state, the same one the
	// project's own tab shows.
	Reason string `json:"reason"`
}

// ProjectPolicyRoster is every wick project with its resolved memory state.
//
// It exists because the trial's consequence is host-wide while its control is
// per-project: turning one project on stops the others recording, and until
// this list existed the only way to see WHICH ones had gone quiet was to open
// them one at a time. "Memory stopped being written here" is otherwise
// indistinguishable from a broken hook — which is the failure the Health tab
// exists to catch, so the roster is what that tab shows next to the finding.
//
// Sorted by name so the list is stable between reads and readable at a glance.
func ProjectPolicyRoster() []ProjectPolicyRow {
	if projectLister == nil {
		return nil
	}
	projects := projectLister()
	out := make([]ProjectPolicyRow, 0, len(projects))
	for _, p := range projects {
		pol := PolicyFor(p.ID)
		out = append(out, ProjectPolicyRow{
			ID:        p.ID,
			Name:      strings.TrimSpace(p.Name),
			Value:     pol.Value,
			Recording: pol.Allowed,
			Reason:    pol.Reason,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// TrialRoster splits the roster the way the question is asked: what is
// recording, and what has gone quiet because of the trial.
//
// A project switched explicitly OFF is not "silenced by the trial" — someone
// decided that, and mixing the two would inflate the number that is supposed
// to alarm people.
func TrialRoster() (recording, silenced []ProjectPolicyRow) {
	for _, r := range ProjectPolicyRoster() {
		switch {
		case r.Recording:
			recording = append(recording, r)
		case r.Value == PolicyUnset:
			silenced = append(silenced, r)
		}
	}
	return recording, silenced
}

// ── the switch a project cannot reach (Yoga, 2026-09-26) ─────────────

// ProviderMemoryState is the host-wide fact that decides whether a project's
// own setting means anything: whether ANY provider instance has Agent Memory
// turned on at all.
//
// It is read with the policy because only one of the two can ENABLE. The
// project switch narrows — off, or a trial — and an instance holds the server
// URL and the credentials, so a project switched on while no instance is wired
// records and recalls nothing. That state is invisible from the policy alone:
// PolicyFor answers "on", nothing happens, and the page has no way to say why.
//
// It counts instances across every backend on purpose. The question is not
// "is this bucket wired" but "will any agent in this project record", and an
// instance pointed at another backend still records.
type ProviderMemoryState struct {
	// Known false = wick could not read the provider list. The state is
	// then unknown, NOT empty: an unreadable config must not be reported
	// as "nothing is switched on".
	Known bool `json:"known"`
	// Instances is how many provider instances use Agent Memory.
	Instances int `json:"instances"`
	// Recording is the subset that also capture. The rest recall and write
	// nothing back, which is how a project reads its memory and never adds
	// to it (PLAN §7.1).
	Recording int `json:"recording"`
	// Names labels them, so a page that says "switch it on somewhere" can
	// also say where it already is.
	Names []string `json:"names,omitempty"`
}

// ProviderMemory reports that state.
func ProviderMemory() ProviderMemoryState {
	list, err := loadInstances()
	if err != nil {
		return ProviderMemoryState{}
	}
	st := ProviderMemoryState{Known: true}
	for _, ins := range list {
		if !ins.UseAgentMemory {
			continue
		}
		st.Instances++
		if ins.AgentMemoryCapture {
			st.Recording++
		}
		st.Names = append(st.Names, InstanceRef{Type: string(ins.Type), Name: ins.Name}.Label())
	}
	sort.Strings(st.Names)
	return st
}
