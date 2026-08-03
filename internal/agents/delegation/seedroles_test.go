package delegation

import (
	"context"
	"strings"
	"testing"
)

// memMarker is an in-memory stand-in for the configs table.
type memMarker struct{ v string }

func (m *memMarker) Get() string        { return m.v }
func (m *memMarker) Set(v string) error { m.v = v; return nil }

// The first thing a team does with a seeded role is tune its prompt for
// their own stack. A seeder that overwrote that would make the roles
// unusable in exactly the case they matter.
func TestSeedDoesNotOverwriteAnOperatorEdit(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	marker := &memMarker{}

	if err := SeedProductionRoles(ctx, r, marker); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	p, err := r.GetProfileExact(ctx, "", "log-investigator")
	if err != nil {
		t.Fatalf("seeded role missing: %v", err)
	}
	p.SystemPrompt = "tuned for our stack"
	if err := r.SaveProfile(ctx, p); err != nil {
		t.Fatalf("edit: %v", err)
	}

	if err := SeedProductionRoles(ctx, r, marker); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	got, _ := r.GetProfileExact(ctx, "", "log-investigator")
	if got.SystemPrompt != "tuned for our stack" {
		t.Fatal("seeding overwrote an operator's edit")
	}
}

// A role somebody deleted must stay deleted. Plain insert-if-absent would
// resurrect it every boot, which is a setting that cannot be changed.
func TestSeedDoesNotResurrectADeletedRole(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	marker := &memMarker{}

	if err := SeedProductionRoles(ctx, r, marker); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	p, err := r.GetProfileExact(ctx, "", "client-response-drafter")
	if err != nil {
		t.Fatalf("seeded role missing: %v", err)
	}
	if err := r.DeleteProfile(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := SeedProductionRoles(ctx, r, marker); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if _, err := r.GetProfileExact(ctx, "", "client-response-drafter"); err == nil {
		t.Fatal("a deleted role came back")
	}
}

// An interrupted first run must be able to finish, so a missing key is
// still filled while the marker is unset.
func TestSeedFillsAGapOnAnUnmarkedRun(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	if err := SeedProductionRoles(ctx, r, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, _ := r.GetProfileExact(ctx, "", "data-validator")
	if err := r.DeleteProfile(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := SeedProductionRoles(ctx, r, nil); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if _, err := r.GetProfileExact(ctx, "", "data-validator"); err != nil {
		t.Fatalf("gap not filled: %v", err)
	}
}

func TestSeededRolesAreShapedForTheirJob(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	if err := SeedProductionRoles(ctx, r, &memMarker{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, key := range SeededRoleKeys {
		p, err := r.GetProfileExact(ctx, "", key)
		if err != nil {
			t.Fatalf("role %q was not seeded: %v", key, err)
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Fatalf("role %q has no description, so nothing can decide when to pick it", key)
		}
		// Locked roles cannot be tuned, and tuning is the first thing a
		// team does with these.
		if p.Locked {
			t.Fatalf("role %q was seeded locked", key)
		}
	}

	// The two that work on already-collected material run synchronously,
	// because the supervisor is waiting on them to decide what happens
	// next. The four that read a large surface do not.
	sync := map[string]bool{"evidence-checker": true, "client-response-drafter": true}
	for _, key := range SeededRoleKeys {
		p, _ := r.GetProfileExact(ctx, "", key)
		want := ModeAsync
		if sync[key] {
			want = ModeSync
		}
		if p.DefaultMode != want {
			t.Fatalf("role %q mode = %q, want %q", key, p.DefaultMode, want)
		}
	}

	// Only the headless supervisor delegates. A role that can spawn is a
	// role that can spend the tree's budget without being asked.
	for _, key := range SeededRoleKeys {
		p, _ := r.GetProfileExact(ctx, "", key)
		if want := key == "incident-supervisor"; p.CanDelegate != want {
			t.Fatalf("role %q can_delegate = %v, want %v", key, p.CanDelegate, want)
		}
	}
}

// The evidence standard is what separates an investigation from a
// plausible story, so every investigating role has to state it.
func TestInvestigatorPromptsCarryTheEvidenceRule(t *testing.T) {
	for _, p := range seededRoles() {
		switch p.Key {
		case "evidence-checker", "client-response-drafter", "incident-supervisor":
			continue // they judge, write, or dispatch rather than gather
		}
		if !strings.Contains(p.SystemPrompt, "A finding with no excerpt is a guess") {
			t.Fatalf("role %q does not state the evidence rule", p.Key)
		}
	}
}

// A prompt that never mentions the constraint is a constraint nobody
// applied.
func TestClientDrafterPromptForbidsInternalDetail(t *testing.T) {
	for _, p := range seededRoles() {
		if p.Key != "client-response-drafter" {
			continue
		}
		for _, want := range []string{"stack traces", "credentials", "never send"} {
			if !strings.Contains(strings.ToLower(p.SystemPrompt), want) {
				t.Fatalf("drafter prompt omits %q", want)
			}
		}
	}
}
