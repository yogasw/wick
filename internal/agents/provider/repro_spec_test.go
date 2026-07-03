package provider

import "testing"

func TestReproSpecFor(t *testing.T) {
	// Every supported type must declare a spec (the reproduce UI relies on it).
	for _, ty := range SupportedTypes() {
		s := ReproSpecFor(ty)
		if len(s.HeadlessFlags) == 0 && len(s.HeadlessValueFlags) == 0 && len(s.HeadlessSubcmds) == 0 {
			t.Errorf("%s: no headless tokens declared", ty)
		}
		if len(s.ResumeValueFlags) == 0 && len(s.ResumeSubcmds) == 0 {
			t.Errorf("%s: no resume token declared", ty)
		}
	}
	// Spot-check the shapes that differ across providers.
	if got := ReproSpecFor(TypeCodex); len(got.HeadlessSubcmds) == 0 || got.HeadlessSubcmds[0] != "exec" {
		t.Errorf("codex should drop the exec subcommand: %+v", got)
	}
	if got := ReproSpecFor(TypeClaude); len(got.ResumeValueFlags) == 0 || got.ResumeValueFlags[0] != "--resume" {
		t.Errorf("claude resume should be --resume: %+v", got)
	}
	// Unknown type → zero spec (nothing stripped).
	if got := ReproSpecFor(Type("mystery")); len(got.HeadlessFlags) != 0 || len(got.ResumeValueFlags) != 0 {
		t.Errorf("unknown type should yield an empty spec: %+v", got)
	}
}
