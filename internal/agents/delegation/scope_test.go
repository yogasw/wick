package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func mustSave(t *testing.T, r *Repo, p *entity.AgentProfile) {
	t.Helper()
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("save %s: %v", p.Key, err)
	}
}

// GetProfileScoped answers "what does this session get when it delegates
// to <key>" — the resolved view, shadow applied.
func TestGetProfileScopedPrefersProjectRow(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	mustSave(t, r, &entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"})
	mustSave(t, r, &entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "researcher", Provider: "codex"})

	got, err := r.GetProfileScoped(ctx, "proj-abc", "researcher")
	if err != nil {
		t.Fatalf("scoped get: %v", err)
	}
	if got.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", got.Provider)
	}

	got, err = r.GetProfileScoped(ctx, "", "researcher")
	if err != nil {
		t.Fatalf("global get: %v", err)
	}
	if got.Provider != "claude" {
		t.Fatalf("provider = %q, want the untouched global claude", got.Provider)
	}
}

// A project role must not resolve for a session in another project.
func TestGetProfileScopedDoesNotLeakAcrossProjects(t *testing.T) {
	r := testRepo(t)
	mustSave(t, r, &entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "db-migrator", Provider: "claude"})

	if _, err := r.GetProfileScoped(context.Background(), "proj-other", "db-migrator"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound", err)
	}
}

// GetProfileExact answers a different question — "does THIS scope already
// hold this key" — and is what save-dedup must use. Were save to use the
// resolved lookup instead, creating a project role named after a global
// one would find the global row and overwrite it, silently editing the
// role every other project sees.
func TestGetProfileExactIgnoresGlobalFallback(t *testing.T) {
	r := testRepo(t)
	mustSave(t, r, &entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"})

	if _, err := r.GetProfileExact(context.Background(), "proj-abc", "researcher"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound — the global row is not this scope's row", err)
	}
}

// A delegation carries the scope it ran in, so anything resolving its
// role later — take-over, the monitor — reaches the same row the child was
// actually spawned from. Without it, a sub-agent spawned from a project
// role would be steered using the GLOBAL role of the same key: a
// different prompt and a different tool list, with no error anywhere.
func TestDelegationRecordsProjectScope(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	d := &entity.AgentDelegation{
		ID: "d1", RootID: "d1", ParentSessionID: "parent", ProfileKey: "researcher",
		ChildSessionID: "sub-d1", ChildAgent: "codex", Status: entity.DelegationRunning,
		TriggeredBy: "user-1", ProjectID: "proj-abc",
	}
	if err := r.SaveDelegationForTest(ctx, d); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProjectID != "proj-abc" {
		t.Fatalf("project_id = %q, want proj-abc", got.ProjectID)
	}
}

// The disabled filter and the scope predicate must compose. An OR written
// without grouping would leak disabled rows once a second condition joins
// it, so assert the combination rather than each half.
func TestListProfilesInScopesExcludesDisabled(t *testing.T) {
	r := testRepo(t)
	mustSave(t, r, &entity.AgentProfile{ID: "g1", Key: "live", Provider: "claude"})
	mustSave(t, r, &entity.AgentProfile{ID: "g2", Key: "dead", Provider: "claude", Disabled: true})
	mustSave(t, r, &entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "scoped-dead", Provider: "claude", Disabled: true})

	rows, err := r.ListProfilesInScopes(context.Background(), "proj-abc", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := keysOf(rows); strings.Join(got, ",") != "live" {
		t.Fatalf("got %v, want [live] — disabled rows must be filtered in both scopes", got)
	}
}

func prof(projectID, key, provider string) entity.AgentProfile {
	return entity.AgentProfile{ID: projectID + "/" + key, ProjectID: projectID, Key: key, Provider: provider}
}

func keysOf(ps []entity.AgentProfile) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

func TestResolveScoped(t *testing.T) {
	all := []entity.AgentProfile{
		prof("", "researcher", "claude"),
		prof("", "reviewer", "claude"),
		prof("proj-abc", "researcher", "codex"),
		prof("proj-abc", "db-migrator", "claude"),
		prof("proj-other", "intruder", "claude"),
	}

	tests := []struct {
		name      string
		projectID string
		wantKeys  []string
	}{
		{"no project sees globals only", "", []string{"researcher", "reviewer"}},
		{"project sees globals plus its own", "proj-abc", []string{"db-migrator", "researcher", "reviewer"}},
		{"unknown project sees globals only", "proj-nope", []string{"researcher", "reviewer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keysOf(ResolveScoped(all, tt.projectID))
			if strings.Join(got, ",") != strings.Join(tt.wantKeys, ",") {
				t.Fatalf("got %v, want %v", got, tt.wantKeys)
			}
		})
	}
}

// Shadowing is the feature, so assert the substitution itself rather than
// just the row count: same key, the project's provider wins.
func TestResolveScopedProjectShadowsGlobal(t *testing.T) {
	all := []entity.AgentProfile{
		prof("", "researcher", "claude"),
		prof("proj-abc", "researcher", "codex"),
	}
	got := ResolveScoped(all, "proj-abc")
	if len(got) != 1 {
		t.Fatalf("a shadow must replace, not duplicate: got %d rows", len(got))
	}
	if got[0].Provider != "codex" {
		t.Fatalf("provider = %q, want the project's codex", got[0].Provider)
	}
}

// Another project's roles must never leak, whichever order they arrive in
// — the global row arriving second must not overwrite the decision.
func TestResolveScopedIgnoresOtherProjects(t *testing.T) {
	all := []entity.AgentProfile{
		prof("proj-other", "researcher", "gemini"),
		prof("", "researcher", "claude"),
	}
	got := ResolveScoped(all, "proj-abc")
	if len(got) != 1 || got[0].Provider != "claude" {
		t.Fatalf("got %v, want the global claude row alone", keysOf(got))
	}
}

// A project row arriving BEFORE the global row it shadows must still win.
// Query order is not guaranteed, so the rule cannot depend on it.
func TestResolveScopedShadowWinsRegardlessOfOrder(t *testing.T) {
	all := []entity.AgentProfile{
		prof("proj-abc", "researcher", "codex"),
		prof("", "researcher", "claude"),
	}
	got := ResolveScoped(all, "proj-abc")
	if len(got) != 1 || got[0].Provider != "codex" {
		t.Fatalf("got %d rows with provider %q, want 1 codex", len(got), got[0].Provider)
	}
}

// The whole point of scoping: a project may define a role under a key the
// global scope already uses. If the old single-column unique index on key
// survives, this fails at INSERT with a constraint violation.
func TestSameKeyAllowedInDifferentScopes(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	global := &entity.AgentProfile{ID: "g1", Key: "researcher", Name: "Researcher", Provider: "claude"}
	if err := r.SaveProfile(ctx, global); err != nil {
		t.Fatalf("save global: %v", err)
	}
	scoped := &entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "researcher", Name: "Researcher", Provider: "codex"}
	if err := r.SaveProfile(ctx, scoped); err != nil {
		t.Fatalf("same key in a different scope must be allowed: %v", err)
	}
}

// Within one scope the key stays unique, otherwise a key no longer names
// exactly one role and delegation becomes ambiguous.
func TestSameKeySameScopeRejected(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	first := &entity.AgentProfile{ID: "a1", ProjectID: "proj-abc", Key: "researcher", Provider: "claude"}
	if err := r.SaveProfile(ctx, first); err != nil {
		t.Fatalf("save first: %v", err)
	}
	dup := &entity.AgentProfile{ID: "a2", ProjectID: "proj-abc", Key: "researcher", Provider: "codex"}
	if err := r.SaveProfile(ctx, dup); err == nil {
		t.Fatal("a duplicate key within one scope must be rejected")
	}
}
