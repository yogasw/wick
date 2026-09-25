package agentmemory

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/pkg/tool"
)

// The per-project switch (Yoga: "jadi aku bisa test 1 project buat aktifin
// ai memory nya").
//
// The rule it implements is the whole feature, so it is tested as a rule and
// not as four endpoints: a project can keep memory out of itself, and the
// first project to opt IN puts the host into a trial where the projects that
// have not opted in go quiet. Get that wrong in either direction and either
// a trial captures everything anyway, or turning one project on silently
// switches the rest off with nothing on screen to say why.

// fakePolicies is an in-memory ProjectPolicyStore.
type fakePolicies struct {
	values map[string]string
	known  map[string]bool
	setErr error
	saved  [][2]string
}

func newPolicies(known ...string) *fakePolicies {
	f := &fakePolicies{values: map[string]string{}, known: map[string]bool{}}
	for _, id := range known {
		f.known[id] = true
	}
	return f
}

func (f *fakePolicies) Policy(id string) (string, bool) { return f.values[id], f.known[id] }

func (f *fakePolicies) SetPolicy(id, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.saved = append(f.saved, [2]string{id, v})
	f.values[id] = v
	if v != "" {
		f.known[id] = true
	}
	return nil
}

func (f *fakePolicies) All() map[string]string {
	out := map[string]string{}
	for k, v := range f.values {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func withPolicies(t *testing.T, s ProjectPolicyStore) {
	t.Helper()
	prev := policies()
	t.Cleanup(func() { SetProjectPolicyStore(prev) })
	SetProjectPolicyStore(s)
}

// TestPolicyFollowsTheInstanceByDefault: with nothing set anywhere, the
// feature behaves exactly as it did before this switch existed.
func TestPolicyFollowsTheInstanceByDefault(t *testing.T) {
	withPolicies(t, newPolicies("a", "b"))
	p := PolicyFor("a")
	if !p.Allowed || p.TrialMode {
		t.Fatalf("policy %+v", p)
	}
	if p.Reason == "" {
		t.Error("a project with no setting still has to say what governs it")
	}
}

// TestPolicyOffKeepsMemoryOut: "off" is not advisory.
func TestPolicyOffKeepsMemoryOut(t *testing.T) {
	st := newPolicies("a", "b")
	st.values["a"] = PolicyOff
	withPolicies(t, st)

	if ProjectAllowed("a") {
		t.Fatal("a project switched off still got memory")
	}
	if !ProjectAllowed("b") {
		t.Fatal("one project's off switched another one off")
	}
	if r := PolicyFor("a").Reason; r == "" {
		t.Error("no reason given for an off project")
	}
}

// TestOneProjectOnPutsTheHostInTrial is the point of the whole thing: opting
// ONE project in has to make the others quiet, or it is not a trial.
func TestOneProjectOnPutsTheHostInTrial(t *testing.T) {
	st := newPolicies("kasir", "brand", "legacy")
	st.values["kasir"] = PolicyOn
	withPolicies(t, st)

	if !ProjectAllowed("kasir") {
		t.Fatal("the opted-in project has no memory")
	}
	for _, id := range []string{"brand", "legacy"} {
		if ProjectAllowed(id) {
			t.Fatalf("%s still captures while a trial is running", id)
		}
	}
	p := PolicyFor("brand")
	if !p.TrialMode || len(p.TrialProjects) != 1 || p.TrialProjects[0] != "kasir" {
		t.Fatalf("policy %+v", p)
	}
	// "Why is my other project silent?" must be answerable from the screen.
	if !contains(p.Reason, "kasir") {
		t.Fatalf("the reason does not name the trial project: %q", p.Reason)
	}
}

// TestOffBeatsTheTrial: a project that said no stays no, even while it is
// the one the trial is about.
func TestOffBeatsTheTrial(t *testing.T) {
	st := newPolicies("kasir", "brand")
	st.values["kasir"] = PolicyOn
	st.values["brand"] = PolicyOff
	withPolicies(t, st)

	if ProjectAllowed("brand") {
		t.Fatal("an explicit off was overridden")
	}
}

// TestUnknownProjectFollowsTheInstance: wick spawns sessions outside any
// project, and the trial clause is about projects. Refusing those would turn
// the feature off for everyone the moment this file was wired in.
func TestUnknownProjectFollowsTheInstance(t *testing.T) {
	st := newPolicies("kasir")
	st.values["kasir"] = PolicyOn
	withPolicies(t, st)

	if !ProjectAllowed("not-a-project") {
		t.Fatal("a session outside any project lost its memory to a trial")
	}
}

// TestUnwiredStoreFollowsTheInstance: a host where nothing wired the switch
// behaves as it always did rather than losing memory everywhere.
func TestUnwiredStoreFollowsTheInstance(t *testing.T) {
	withPolicies(t, nil)
	if !ProjectAllowed("anything") {
		t.Fatal("an unwired policy store switched memory off")
	}
}

// TestSetProjectPolicyRefusesNonsense: three values, and nothing else
// reaches the project's meta.json.
func TestSetProjectPolicyRefusesNonsense(t *testing.T) {
	st := newPolicies("a")
	withPolicies(t, st)
	for _, v := range []string{PolicyUnset, PolicyOn, PolicyOff} {
		if err := SetProjectPolicy("a", v); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
	}
	if err := SetProjectPolicy("a", "maybe"); err == nil {
		t.Fatal(`"maybe" was accepted`)
	}
	if len(st.saved) != 3 {
		t.Fatalf("saved %v", st.saved)
	}
}

// ── the spawn gate ───────────────────────────────────────────────────

// TestSpawnGateFollowsTheFolder: a spawn has a directory, the policy is keyed
// by project id, and the mapping is wick's own project list.
func TestSpawnGateFollowsTheFolder(t *testing.T) {
	st := newPolicies("kasir", "brand")
	st.values["kasir"] = PolicyOn
	withPolicies(t, st)
	withProjects(t, "wick",
		ProjectFolder{ID: "kasir", Name: "Kasir", Folder: "/srv/projects/kasir/files"},
		ProjectFolder{ID: "brand", Name: "Brand", Folder: "/srv/projects/brand/files"},
	)

	if !ProjectAllowedForFolder("/srv/projects/kasir/files") {
		t.Fatal("the opted-in project's folder was refused")
	}
	// A session in a subdirectory is still in that project.
	if !ProjectAllowedForFolder("/srv/projects/kasir/files/services/api") {
		t.Fatal("a subdirectory of the project was treated as another project")
	}
	if ProjectAllowedForFolder("/srv/projects/brand/files") {
		t.Fatal("a project outside the trial captured anyway")
	}
	// Somewhere else entirely: not a project, so it follows the instance.
	if !ProjectAllowedForFolder("/tmp/scratch") {
		t.Fatal("a folder outside every project lost its memory")
	}
	if !ProjectAllowedForFolder("") {
		t.Fatal("an unknown folder must follow the instance, not refuse")
	}
}

// TestSpawnContributionIsEmptyForADisabledProject is the gate as the spawner
// meets it: no MCP server, no hooks, nothing — which is what "off" has to
// mean, since half of it would recall without recording.
func TestSpawnContributionIsEmptyForADisabledProject(t *testing.T) {
	Register(Descriptor{ID: "policy-mem", DisplayName: "policy-mem", BinName: "policy-mem", PrefPort: 42400, HealthPath: "/healthz", Hook: fakeHook{}})
	Init()
	st := newPolicies("off-project")
	st.values["off-project"] = PolicyOff
	withPolicies(t, st)
	withProjects(t, "wick", ProjectFolder{ID: "off-project", Name: "Off", Folder: "/srv/projects/off/files"})

	ins := &provider.Instance{UseAgentMemory: true, AgentMemoryProvider: "policy-mem", AgentMemoryCapture: true}
	got, err := provider.MemorySpawnContribution(ins, provider.TypeClaude, "/srv/projects/off/files")
	if err != nil {
		t.Fatalf("contribution: %v", err)
	}
	if len(got.Args) != 0 || len(got.Env) != 0 {
		t.Fatalf("a disabled project still got wiring: %+v", got)
	}

	// …and the same instance in a project that is not disabled still works.
	got, err = provider.MemorySpawnContribution(ins, provider.TypeClaude, "/tmp/elsewhere")
	if err != nil {
		t.Fatalf("contribution: %v", err)
	}
	if len(got.Args) == 0 && len(got.Env) == 0 {
		t.Fatal("the gate switched memory off everywhere, not just in the disabled project")
	}
}

// ── endpoints ────────────────────────────────────────────────────────

func TestProjectPolicyEndpoints(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	st := newPolicies("kasir")
	withPolicies(t, st)

	t.Run("read answers the resolved policy", func(t *testing.T) {
		w, c := get(url.Values{"project": {"kasir"}})
		projectPolicyHandler(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		body := decodeBody(t, w)
		if _, ok := body["allowed"]; !ok {
			t.Fatalf("payload %v", body)
		}
		if body["reason"] == "" {
			t.Error("no reason in the payload")
		}
	})

	t.Run("a missing project is a 400, not a guess", func(t *testing.T) {
		w, c := get(nil)
		projectPolicyHandler(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d", w.Code)
		}
	})

	t.Run("saving answers the resolved policy, because it changes the others", func(t *testing.T) {
		w, c := post(url.Values{"project": {"kasir"}, "value": {PolicyOn}})
		saveProjectPolicyHandler(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		if body["value"] != PolicyOn || body["trial_mode"] != true {
			t.Fatalf("payload %v", body)
		}
	})

	t.Run("an invalid value is refused", func(t *testing.T) {
		w, c := post(url.Values{"project": {"kasir"}, "value": {"sometimes"}})
		saveProjectPolicyHandler(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d", w.Code)
		}
	})
}

// TestProjectPolicyWriteRefusesAViewer: the switch decides whether agents in
// a project record anything at all, so it is admin-only (PLAN §23).
func TestProjectPolicyWriteRefusesAViewer(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, viewer: true})
	withPolicies(t, newPolicies("kasir"))

	w, c := post(url.Values{"project": {"kasir"}, "value": {PolicyOff}})
	saveProjectPolicyHandler(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a viewer changed a project's memory policy: %d", w.Code)
	}

	// Reading it is open: the project's own tab shows the state to anyone
	// who may see the project.
	w, c = get(url.Values{"project": {"kasir"}})
	projectPolicyHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("a viewer could not read the policy: %d", w.Code)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && stringsIndex(s, sub) >= 0 }

func stringsIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = tool.HandlerFunc(nil)

// Leaving the trial must restore EXACTLY the behaviour that existed before
// anyone opted in. A narrowing you cannot undo is not a trial, it is a
// migration — and the host would be left quietly not recording with no
// obvious way back.
func TestLeavingTheTrialRestoresTheOldBehaviour(t *testing.T) {
	st := newPolicies("a", "b", "c")
	withPolicies(t, st)
	withProjects(t, "wick",
		ProjectFolder{ID: "a", Name: "A", Folder: "/srv/a/files"},
		ProjectFolder{ID: "b", Name: "B", Folder: "/srv/b/files"},
		ProjectFolder{ID: "c", Name: "C", Folder: "/srv/c/files"},
	)

	before := PolicyFor("b")
	if !before.Allowed || before.TrialMode {
		t.Fatalf("before any opt-in: allowed=%v trial=%v, want allowed with no trial", before.Allowed, before.TrialMode)
	}

	if err := SetProjectPolicy("a", PolicyOn); err != nil {
		t.Fatal(err)
	}
	during := PolicyFor("b")
	if during.Allowed || !during.TrialMode {
		t.Fatalf("during the trial: allowed=%v trial=%v, want b silenced", during.Allowed, during.TrialMode)
	}
	if RunTrialCheck().Silenced != 2 {
		t.Fatalf("silenced = %d, want the 2 projects still on follow", RunTrialCheck().Silenced)
	}

	// Back to unset — the round trip.
	if err := SetProjectPolicy("a", PolicyUnset); err != nil {
		t.Fatal(err)
	}
	after := PolicyFor("b")
	if after.Allowed != before.Allowed || after.TrialMode != before.TrialMode {
		t.Fatalf("after leaving: allowed=%v trial=%v, want the state it started in (%v/%v)",
			after.Allowed, after.TrialMode, before.Allowed, before.TrialMode)
	}
	if tc := RunTrialCheck(); tc.Active || tc.Silenced != 0 {
		t.Fatalf("trial check still reports %+v after the trial ended", tc)
	}
}

// ── the roster ───────────────────────────────────────────────────────

// TestRosterAnswersWhoIsRecordingAndWhoIsSilent is the guard the trial needs
// most: its control is per-project and its consequence is host-wide, so an
// operator has to be able to see WHICH projects went quiet without opening
// them one at a time.
func TestRosterAnswersWhoIsRecordingAndWhoIsSilent(t *testing.T) {
	st := newPolicies("kasir", "brand", "legacy", "archive")
	st.values["kasir"] = PolicyOn
	st.values["archive"] = PolicyOff
	withPolicies(t, st)
	withProjects(t, "wick",
		ProjectFolder{ID: "kasir", Name: "Kasir", Folder: "/srv/p/kasir"},
		ProjectFolder{ID: "brand", Name: "Brand site", Folder: "/srv/p/brand"},
		ProjectFolder{ID: "legacy", Name: "Legacy API", Folder: "/srv/p/legacy"},
		ProjectFolder{ID: "archive", Name: "Archive", Folder: "/srv/p/archive"},
	)

	recording, silenced := TrialRoster()
	if len(recording) != 1 || recording[0].ID != "kasir" {
		t.Fatalf("recording %+v", recording)
	}
	// The two left on "follow the instance" are the ones the trial silenced.
	// The project someone switched OFF is not counted among them: that was a
	// decision, and mixing the two inflates the number meant to alarm people.
	if len(silenced) != 2 {
		t.Fatalf("silenced %+v", silenced)
	}
	names := []string{silenced[0].Name, silenced[1].Name}
	if names[0] != "Brand site" || names[1] != "Legacy API" {
		t.Fatalf("silenced names %v (sorted by name?)", names)
	}
	for _, r := range silenced {
		if r.Reason == "" {
			t.Errorf("%s went quiet with no reason attached", r.Name)
		}
		// The id is what the policy is keyed by; the name is what a person
		// recognises. A roster of uuids is a roster nobody can act on.
		if r.ID == "" || r.Name == "" {
			t.Errorf("row %+v is missing half its identity", r)
		}
	}
}

// TestRosterOutsideATrialReportsEverythingRecording: with nothing opted in,
// the roster says exactly what the old behaviour was — no silenced column
// appearing out of nowhere.
func TestRosterOutsideATrialReportsEverythingRecording(t *testing.T) {
	st := newPolicies("a", "b")
	withPolicies(t, st)
	withProjects(t, "wick",
		ProjectFolder{ID: "a", Name: "A", Folder: "/srv/a"},
		ProjectFolder{ID: "b", Name: "B", Folder: "/srv/b"},
	)

	recording, silenced := TrialRoster()
	if len(recording) != 2 || len(silenced) != 0 {
		t.Fatalf("recording %d silenced %d", len(recording), len(silenced))
	}
}

// TestRosterEndpointIsReadableAndComplete drives the endpoint the Health tab
// reads, including the trial block it renders beside the list.
func TestRosterEndpointIsReadableAndComplete(t *testing.T) {
	st := newPolicies("kasir", "brand")
	st.values["kasir"] = PolicyOn
	withPolicies(t, st)
	withProjects(t, "wick",
		ProjectFolder{ID: "kasir", Name: "Kasir", Folder: "/srv/p/kasir"},
		ProjectFolder{ID: "brand", Name: "Brand site", Folder: "/srv/p/brand"},
	)

	// A VIEWER: an operator who has just inherited this host has to be able
	// to see what is happening without being an admin.
	withStore(t, &fakeStore{enabled: true, viewer: true})
	w, c := get(nil)
	projectPolicyRosterHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := decodeBody(t, w)
	for _, key := range []string{"trial", "recording", "silenced"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("payload is missing %q: %v", key, body)
		}
	}
	rec, _ := body["recording"].([]any)
	sil, _ := body["silenced"].([]any)
	if len(rec) != 1 || len(sil) != 1 {
		t.Fatalf("recording %v silenced %v", rec, sil)
	}
	row, _ := sil[0].(map[string]any)
	if row["name"] != "Brand site" || row["recording"] != false {
		t.Fatalf("silenced row %v", row)
	}
	trial, _ := body["trial"].(map[string]any)
	if trial["active"] != true {
		t.Fatalf("trial block %v", trial)
	}
}

// TestRosterListsAreNeverNull: the FE maps over both, and a null is a
// TypeError rather than an empty table.
func TestRosterListsAreNeverNull(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, viewer: true})
	withPolicies(t, newPolicies())
	prev := projectLister
	t.Cleanup(func() { projectLister = prev })
	projectLister = nil

	w, c := get(nil)
	projectPolicyRosterHandler(c)
	body := decodeBody(t, w)
	for _, key := range []string{"recording", "silenced"} {
		v, ok := body[key]
		if !ok || v == nil {
			t.Fatalf("%s serialised as %v", key, v)
		}
		if _, ok := v.([]any); !ok {
			t.Fatalf("%s is %T, want an array", key, v)
		}
	}
}
