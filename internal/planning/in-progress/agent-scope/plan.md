# Agent Profile Scoping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give sub-agent roles a second scope — a project can own roles that shadow or supplement the global set — and ship an editor UI for both scopes.

**Architecture:** `AgentProfile` gains a `ProjectID` column; uniqueness moves from `key` to `(project_id, key)`. The repo never merges — it returns both scopes from one query and a pure function `ResolveScoped` applies the shadow rule, so the MCP roster and the web UI cannot drift. Two UI surfaces (a new SPA for global, a tab in project-settings for project scope) share one editor component from `fe/common/ui`.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite dialects only), templ, Svelte 5 runes, Vite, vitest.

**Spec:** [design.md](design.md)

## Global Constraints

- **Never `git commit`.** The user commits. Plans elsewhere in Superpowers end tasks with a commit step; here every task ends at "tests pass". Do not stage, commit, push, or open a PR.
- **UI copy is English.** Every label, placeholder, helper string, and error message.
- **No "qiscus" in samples.** Use `abc.com`, `example.com`, generic names.
- **Never edit `*_templ.go`.** Edit the `.templ` source and run `templ generate`.
- **Design system:** Inter (`font-sans`), 8px spacing grid, named Tailwind tokens only (no raw hex, no arbitrary values), a `dark:` counterpart on every colour class, status from the `pos/prog/cau/neg` ramps — green is the accent, never "success".
- **Zerolog:** `l := log.With().Str("component", "x").Logger()`, then `l.Debug()...`. Never `log.Debug()` directly.
- **`safeexec`, never `os/exec`.** `TestNoDirectOSExec` enforces it.
- Dialects in play are **postgres and sqlite only** (`internal/pkg/postgres/gorm.go:26,30`). `DROP INDEX IF EXISTS <name>` is valid on both; do not write MySQL's `DROP INDEX x ON t` form.
- Run Go tests with `-count=1` — the cache hides real failures.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/agents/delegation/scope.go` | `ResolveScoped` — the pure shadow rule. Nothing else. |
| `internal/agents/delegation/scope_test.go` | Shadow-rule table tests + cross-scope uniqueness tests. |
| `internal/pkg/postgres/migrate_test.go` | Proves the stale unique index is actually dropped. |
| `fe/common/api/agentProfiles.ts` | Typed client for `/api/agent-profiles`. Shared by both surfaces. |
| `fe/common/ui/AgentProfileEditor.svelte` | The one profile form. ~15 fields, scope-agnostic. |
| `fe/agents/agent-profiles/**` | New SPA (global scope). Presets-shaped scaffold. |
| `fe/agents/project-settings/src/lib/components/SubAgentsTab.svelte` | Project-scope surface. |
| `internal/tools/agents/view/agent_profiles_spa.templ` | Shell for the new SPA. |

**Modified:**

| Path | Change |
|---|---|
| `internal/entity/agent_profile.go` | `+ProjectID`, composite unique index. |
| `internal/entity/agent_delegation.go` | `+ProjectID`. |
| `internal/pkg/postgres/migrate.go` | Drop stale `idx_agent_profiles_key` before AutoMigrate. |
| `internal/agents/delegation/repo.go` | `+ListProfilesInScopes`, `+GetProfileScoped`, `+GetProfileExact`. |
| `internal/agents/delegation/run.go:219` | Scoped profile resolution. |
| `internal/agents/delegation/takeover.go:51` | Scoped via `d.ProjectID`. |
| `internal/mcp/handlers/delegation.go:180,256` | Roster reads the session's project; delegate reorders session-load before profile-resolve. |
| `internal/tools/agents/api_agent_profiles.go` | `project_id` in/out, per-scope permission split. |
| `internal/tools/agents/handler.go` | Route + page handler for the new SPA. |
| `internal/tools/agents/view/layout.templ` | Sidebar entry + `agentsMoreOpenAttr` case. |
| `fe/agents/project-settings/src/App.svelte` | Tab strip. |
| `docs/guide/agents/sub-agents.md` | Scoping section + the project-access trust note. |

---

### Task 1: Scope column + migration trap

The column is inert without the index change, and the index change is inert
on existing databases unless the old one is dropped. They ship together.

**Files:**
- Modify: `internal/entity/agent_profile.go:17-22`
- Modify: `internal/pkg/postgres/migrate.go:14,63-72`
- Test: `internal/agents/delegation/scope_test.go` (new)
- Test: `internal/pkg/postgres/migrate_test.go` (new)

**Interfaces:**
- Produces: `entity.AgentProfile.ProjectID string` (json `project_id`); `postgres.DropStaleProfileKeyIndex(db *gorm.DB)`.

- [ ] **Step 1: Write the failing uniqueness tests**

Create `internal/agents/delegation/scope_test.go`. `testRepo` already exists in
`governor_test.go` (same package) — reuse it, do not write a second one.

```go
package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agents/delegation/ -run 'TestSameKey' -count=1 -v`
Expected: `TestSameKeyAllowedInDifferentScopes` FAILs — `UNIQUE constraint failed: agent_profiles.key`.

- [ ] **Step 3: Add the column and move the index**

In `internal/entity/agent_profile.go`, replace the `Key` field block:

```go
	// ProjectID scopes this role. Empty = global: reachable from every
	// project. Non-empty = owned by that project and invisible elsewhere.
	// A project row whose Key matches a global row SHADOWS it for
	// sessions in that project; the global row is untouched for everyone
	// else. See delegation.ResolveScoped.
	ProjectID string `gorm:"type:varchar(64);not null;default:'';index:idx_profile_scope,unique,priority:1" json:"project_id"`
	// Key is the stable handle the LLM passes to wick_delegate. Unique
	// within a scope, lowercase-kebab by convention ("code-reviewer").
	Key         string `gorm:"type:varchar(128);not null;index:idx_profile_scope,unique,priority:2" json:"key"`
```

Note the `uniqueIndex` tag is **gone** from `Key` — it is now carried by the
composite index alone.

- [ ] **Step 4: Run to verify both pass**

Run: `go test ./internal/agents/delegation/ -run 'TestSameKey' -count=1 -v`
Expected: PASS. (A fresh test DB never had the old index, which is exactly
why Step 5 exists — this passing does not mean real installs work.)

- [ ] **Step 5: Write the failing migration test**

Create `internal/pkg/postgres/migrate_test.go`:

```go
package postgres

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// AutoMigrate creates the new composite index but never drops the old
// single-column one. Left behind, it keeps rejecting the second scope's
// row — and the failure surfaces as a constraint violation on save, far
// from its cause. This reproduces an upgraded database, not a fresh one.
func TestDropStaleProfileKeyIndexAllowsCrossScopeKeys(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&entity.AgentProfile{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Recreate the pre-scoping index exactly as the old tag produced it.
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_profiles_key ON agent_profiles(key)`).Error; err != nil {
		t.Fatalf("seed stale index: %v", err)
	}

	DropStaleProfileKeyIndex(db)

	if err := db.Create(&entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"}).Error; err != nil {
		t.Fatalf("insert global: %v", err)
	}
	if err := db.Create(&entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "researcher", Provider: "codex"}).Error; err != nil {
		t.Fatalf("the stale index was not dropped: %v", err)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/pkg/postgres/ -run TestDropStaleProfileKeyIndex -count=1 -v`
Expected: FAIL — `undefined: DropStaleProfileKeyIndex`.

- [ ] **Step 7: Implement the drop**

In `internal/pkg/postgres/migrate.go`, add above `func Migrate`:

```go
// DropStaleProfileKeyIndex removes the pre-scoping unique index on
// agent_profiles(key).
//
// Sub-agent roles became scoped: uniqueness is now (project_id, key), so
// a project may define a role under a key the global scope already uses.
// AutoMigrate creates that composite index but leaves the old one in
// place, and the old one keeps rejecting the second row — surfacing as a
// constraint violation on save, nowhere near its cause.
//
// Idempotent and safe to run on databases that never had the index.
func DropStaleProfileKeyIndex(db *gorm.DB) {
	l := log.With().Str("component", "migrate").Logger()
	if res := db.Exec(`DROP INDEX IF EXISTS idx_agent_profiles_key`); res.Error != nil {
		l.Warn().Err(res.Error).Msg("could not drop the stale agent_profiles key index; project-scoped roles may be rejected")
	}
}
```

Then call it inside `Migrate`, immediately after the `AutoMigrate` error
check and before the `idx_storage_tree` block:

```go
	// Must run after AutoMigrate: the composite index has to exist before
	// the single-column one is removed, or a concurrent write could land
	// with no uniqueness guard at all.
	DropStaleProfileKeyIndex(db)
```

- [ ] **Step 8: Run to verify it passes**

Run: `go test ./internal/pkg/postgres/ ./internal/agents/delegation/ -count=1`
Expected: PASS.

---

### Task 2: `ResolveScoped` — the shadow rule

**Files:**
- Create: `internal/agents/delegation/scope.go`
- Test: `internal/agents/delegation/scope_test.go` (append)

**Interfaces:**
- Consumes: `entity.AgentProfile.ProjectID` (Task 1).
- Produces: `delegation.ResolveScoped(profiles []entity.AgentProfile, projectID string) []entity.AgentProfile`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agents/delegation/scope_test.go`:

```go
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

// Shadowing is the feature, so assert the substitution itself, not just
// that the count is right: same key, the project's provider wins.
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

// Another project's roles must never leak, whichever order they arrive in.
func TestResolveScopedIgnoresOtherProjects(t *testing.T) {
	all := []entity.AgentProfile{
		prof("proj-other", "researcher", "gemini"),
		prof("", "researcher", "claude"),
	}
	got := ResolveScoped(all, "proj-abc")
	if len(got) != 1 || got[0].Provider != "claude" {
		t.Fatalf("got %+v, want the global claude row alone", keysOf(got))
	}
}
```

Add `"strings"` to that file's imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestResolveScoped -count=1 -v`
Expected: FAIL — `undefined: ResolveScoped`.

- [ ] **Step 3: Implement**

Create `internal/agents/delegation/scope.go`:

```go
package delegation

import (
	"sort"

	"github.com/yogasw/wick/internal/entity"
)

// ResolveScoped returns the roles visible to a session in projectID:
// every global role, with any role the project defines under the same key
// substituted in.
//
// Pure by design — no DB, no context. The MCP roster and the web UI both
// call it, so the two surfaces cannot disagree about what a project can
// see, and the rule is testable without a database.
//
// An empty projectID resolves to the global scope alone, which is the
// behaviour that predates scoping.
func ResolveScoped(profiles []entity.AgentProfile, projectID string) []entity.AgentProfile {
	byKey := make(map[string]entity.AgentProfile, len(profiles))
	for _, p := range profiles {
		if p.ProjectID != "" && p.ProjectID != projectID {
			continue // belongs to a different project
		}
		if existing, ok := byKey[p.Key]; ok && existing.ProjectID != "" {
			continue // a project row already claimed this key
		}
		byKey[p.Key] = p
	}
	out := make([]entity.AgentProfile, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	// Map iteration is random; callers render this list and diff it in
	// tests, so the order has to be deterministic.
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/agents/delegation/ -count=1`
Expected: PASS.

---

### Task 3: Scoped repo lookups

Three methods, deliberately distinct. Collapsing the last two is the bug
this task exists to prevent: saving a project role under a key the global
scope uses must **create a new row**, not overwrite the global one.

**Files:**
- Modify: `internal/agents/delegation/repo.go:55-79`
- Test: `internal/agents/delegation/scope_test.go` (append)

**Interfaces:**
- Consumes: `ResolveScoped` (Task 2).
- Produces:
  - `(*Repo).ListProfilesInScopes(ctx context.Context, projectID string, includeDisabled bool) ([]entity.AgentProfile, error)`
  - `(*Repo).GetProfileScoped(ctx context.Context, projectID, key string) (*entity.AgentProfile, error)`
  - `(*Repo).GetProfileExact(ctx context.Context, projectID, key string) (*entity.AgentProfile, error)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/agents/delegation/scope_test.go`:

```go
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
// have this key" — and is what save-dedup must use. If save used the
// resolved lookup instead, creating a project role named after a global
// one would find the global row and overwrite it.
func TestGetProfileExactIgnoresGlobalFallback(t *testing.T) {
	r := testRepo(t)
	mustSave(t, r, &entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"})

	if _, err := r.GetProfileExact(context.Background(), "proj-abc", "researcher"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound — the global row is not this scope's row", err)
	}
}

func mustSave(t *testing.T, r *Repo, p *entity.AgentProfile) {
	t.Helper()
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("save %s: %v", p.Key, err)
	}
}
```

Add `"errors"` to that file's imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agents/delegation/ -run 'TestGetProfile' -count=1 -v`
Expected: FAIL — `r.GetProfileScoped undefined`.

- [ ] **Step 3: Implement**

In `internal/agents/delegation/repo.go`, add after `GetProfile`:

```go
// ListProfilesInScopes returns the global roles plus the ones owned by
// projectID, unmerged. Shadowing is applied by ResolveScoped, not here —
// the repo issues the query, the pure function holds the rule.
//
// The scope predicate is written as IN rather than "a = ? OR b = ?"
// because an OR combined with the disabled filter would need explicit
// grouping to mean what it looks like it means.
func (r *Repo) ListProfilesInScopes(ctx context.Context, projectID string, includeDisabled bool) ([]entity.AgentProfile, error) {
	q := r.db.WithContext(ctx).
		Where("project_id IN (?)", []string{"", projectID}).
		Order("key asc")
	if !includeDisabled {
		q = q.Where("disabled = ?", false)
	}
	var out []entity.AgentProfile
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetProfileScoped resolves the role a session in projectID gets for key,
// with the project's version shadowing a global one of the same name.
// This is the runtime lookup — what a delegation actually spawns.
func (r *Repo) GetProfileScoped(ctx context.Context, projectID, key string) (*entity.AgentProfile, error) {
	rows, err := r.ListProfilesInScopes(ctx, projectID, true)
	if err != nil {
		return nil, err
	}
	for _, p := range ResolveScoped(rows, projectID) {
		if p.Key == key {
			found := p
			return &found, nil
		}
	}
	return nil, ErrProfileNotFound
}

// GetProfileExact looks up a row in one scope only, with no global
// fallback. Save-dedup MUST use this: resolving instead would make
// "create a project role named like a global one" find the global row and
// overwrite it — silently editing every other project's role.
func (r *Repo) GetProfileExact(ctx context.Context, projectID, key string) (*entity.AgentProfile, error) {
	var p entity.AgentProfile
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND key = ?", projectID, key).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/agents/delegation/ -count=1`
Expected: PASS.

---

### Task 4: Delegations remember their scope

Take-over resolves a running sub-agent's role by key. Without the scope on
the row it resolves the *global* role of that key — a silent mismatch that
steers using the wrong system prompt and the wrong tool list.

**Files:**
- Modify: `internal/entity/agent_delegation.go` (near `Workspace`, line ~99)
- Modify: `internal/agents/delegation/run.go` (row construction; `:219` for the lookup)
- Modify: `internal/agents/delegation/takeover.go:51`
- Test: `internal/agents/delegation/scope_test.go` (append)

**Interfaces:**
- Consumes: `GetProfileScoped` (Task 3).
- Produces: `entity.AgentDelegation.ProjectID string` (json `project_id`).

- [ ] **Step 1: Write the failing test**

Append to `internal/agents/delegation/scope_test.go`:

```go
// A delegation carries the scope it ran in, so anything resolving its
// role later (take-over, the monitor) reaches the same row the child was
// actually spawned from.
func TestDelegationRecordsProjectScope(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	d := &entity.AgentDelegation{
		ID: "d1", RootID: "d1", ParentSessionID: "parent", ProfileKey: "researcher",
		ChildSessionID: "sub-d1", ChildAgent: "codex", Status: StatusRunning,
		TriggeredBy: "user-1", ProjectID: "proj-abc",
	}
	if err := r.SaveDelegationForTest(ctx, d); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.GetDelegation(ctx, "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProjectID != "proj-abc" {
		t.Fatalf("project_id = %q, want proj-abc", got.ProjectID)
	}
}
```

Before writing, confirm the exact names of the seed and fetch helpers in
`internal/agents/delegation/repo.go` (`SaveDelegationForTest`, `GetDelegation`)
and use whatever is actually there.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestDelegationRecordsProjectScope -count=1 -v`
Expected: FAIL — `unknown field ProjectID`.

- [ ] **Step 3: Add the column and populate it**

In `internal/entity/agent_delegation.go`, beside `Workspace`:

```go
	// ProjectID is the scope the delegation ran in, copied from the
	// parent session. Roles are scoped, so resolving ProfileKey later
	// without it would find the global role of the same name — a
	// different prompt and a different tool list, with no error.
	ProjectID string `gorm:"type:varchar(64);not null;default:''" json:"project_id"`
```

In `internal/agents/delegation/run.go`, set `ProjectID: req.ProjectID` on the
`entity.AgentDelegation` literal, and change the lookup at `:219`:

```go
	profile, err := s.Repo.GetProfileScoped(ctx, req.ProjectID, req.ProfileKey)
```

In `internal/agents/delegation/takeover.go:51`:

```go
	profile, err := s.Repo.GetProfileScoped(ctx, d.ProjectID, d.ProfileKey)
```

- [ ] **Step 4: Run the package**

Run: `go test ./internal/agents/delegation/ -count=1`
Expected: PASS — including the pre-existing run/interrupt/takeover tests,
which exercise the empty-project path and must be unaffected.

---

### Task 5: MCP roster and delegate follow the session's project

**Files:**
- Modify: `internal/mcp/handlers/delegation.go:180` (`WickAgents`)
- Modify: `internal/mcp/handlers/delegation.go:256-311` (`WickDelegate`)

**Interfaces:**
- Consumes: `ResolveScoped` (Task 2), `GetProfileScoped` (Task 3).

- [ ] **Step 1: Add the scope lookup helper**

In `internal/mcp/handlers/delegation.go`, above `WickAgents`:

```go
// delegatingProjectID resolves the project a tool call belongs to.
//
// The session comes from the spawn-time header, never from the model: a
// caller naming its own session could attach itself to another project's
// scope and reach roles that project owns. An unresolvable session yields
// the global scope, which is strictly the smaller set.
func delegatingProjectID(r *http.Request, deps DelegationDeps) (sessionID, projectID string) {
	sessionID = strings.TrimSpace(r.Header.Get("X-Wick-Session-Id"))
	if sessionID == "" {
		return "", ""
	}
	sess, err := session.Load(deps.Layout, sessionID)
	if err != nil {
		return sessionID, ""
	}
	return sessionID, sess.Meta.ProjectID
}
```

- [ ] **Step 2: Scope the roster**

In `WickAgents`, replace the `ListProfiles` call:

```go
	_, projectID := delegatingProjectID(r, deps)
	rows, err := deps.Service.Repo.ListProfilesInScopes(r.Context(), projectID, false)
	if err != nil {
		rsp.ToolError(w, req.ID, "list profiles: "+err.Error(), agentsToolName)
		return
	}
	profiles := delegation.ResolveScoped(rows, projectID)
```

Leave the `VisibleProfiles` line below it untouched — scope decides what
exists, tags decide what may be seen, and both still apply.

- [ ] **Step 3: Reorder `WickDelegate`**

Today `GetProfile` runs at `:256` while the project is only discovered at
`:305`. Move the session load up. Replace the block from the `sessionID`
resolution through the profile lookup with:

```go
	sessionID, projectID := delegatingProjectID(r, deps)
	if sessionID == "" {
		sessionID = strings.TrimSpace(argString(args, "session_id"))
	}
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "cannot resolve the delegating session — delegation is only available to a running agent session", delegateToolName)
		return
	}

	// Resolve in the session's scope, so a project role shadows the
	// global one of the same key.
	profile, err := deps.Service.Repo.GetProfileScoped(r.Context(), projectID, profileKey)
```

Then, further down, the existing `if sess, err := session.Load(...)` block
keeps setting `req2.TriggeredBy` but no longer needs to set `ProjectID`
twice — set it from the value already resolved:

```go
	req2.ProjectID = projectID
```

- [ ] **Step 4: Build and run the MCP tests**

Run: `go build ./... && go test ./internal/mcp/... -count=1`
Expected: PASS.

---

### Task 6: HTTP API — scope in, scope-aware permissions

**Files:**
- Modify: `internal/tools/agents/api_agent_profiles.go`
- Test: `internal/tools/agents/api_agent_profiles_scope_test.go` (new)

**Interfaces:**
- Consumes: `ListProfilesInScopes`, `GetProfileExact`, `ResolveScoped`.
- Produces: `AgentProfileItem.ProjectID string` (json `project_id`); `GET /api/agent-profiles?project_id=<id>`.

- [ ] **Step 1: Add `ProjectID` to the DTO**

In `AgentProfileItem`, after `Key`:

```go
	ProjectID string `json:"project_id"`
```

and in `profileToItem`, add `ProjectID: p.ProjectID`.

- [ ] **Step 2: Scope the list handler**

In `apiAgentProfileList`, replace the `ListProfiles` call:

```go
	projectID := strings.TrimSpace(c.Query("project_id"))
	if projectID != "" && !callerProjectAccess(c).allowProject(projectID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	rows, err := globalDelegation.Repo.ListProfilesInScopes(c.Context(), projectID, isAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	profiles := delegation.ResolveScoped(rows, projectID)
```

Confirm the query-string accessor's real name on `*tool.Ctx` before writing
this (`c.Query`, `c.R.URL.Query().Get`, …) and use the actual one.

The project tab needs the *unresolved* rows to tell inherited from owned,
so add a passthrough: when `project_id` is set, also return the raw rows.

```go
	c.JSON(http.StatusOK, map[string]any{
		"profiles": items,   // resolved: what a session in this scope sees
		"owned":    ownedItems, // rows whose project_id == projectID
	})
```

Build `ownedItems` by filtering `rows` on `p.ProjectID == projectID && projectID != ""`.

- [ ] **Step 3: Split the save permission**

In `apiAgentProfileSave`, replace the `requireProfileAdmin(c)` guard in the
early-return chain. It must run *after* the body is decoded, because the
scope is in the body:

```go
	if notReady(c) || !delegationReady(c) {
		return
	}
	var req AgentProfileItem
	// … decode as today …

	req.ProjectID = strings.TrimSpace(req.ProjectID)
	// A global role is reachable from every project, so editing one stays
	// admin-only. A project role is scoped to people who already have the
	// project, so project access is the bar.
	if req.ProjectID == "" {
		if !requireProfileAdmin(c) {
			return
		}
	} else if !callerProjectAccess(c).allowProject(req.ProjectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "no access to that project"})
		return
	}
```

- [ ] **Step 4: Fix the dedup lookup**

Still in `apiAgentProfileSave`, the existing `GetProfile(c.Context(), req.Key)`
must become scope-exact, or saving a project role named after a global one
overwrites the global row:

```go
	existing, err := globalDelegation.Repo.GetProfileExact(c.Context(), req.ProjectID, req.Key)
```

and carry the scope onto the row being written:

```go
		ProjectID: req.ProjectID,
```

- [ ] **Step 5: Split the delete permission**

In `apiAgentProfileDelete`, replace the admin-only guard. The row's own
scope decides:

```go
	existing, err := globalDelegation.Repo.GetProfileByID(c.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "profile not found"})
		return
	}
	if existing.ProjectID == "" {
		if !requireProfileAdmin(c) {
			return
		}
	} else if !callerProjectAccess(c).allowProject(existing.ProjectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "no access to that project"})
		return
	}
```

- [ ] **Step 6: Write the permission tests**

Create `internal/tools/agents/api_agent_profiles_scope_test.go`. Follow the
harness already used by `internal/tools/agents/api_providers_test.go` for
building a `*tool.Ctx` with a logged-in user — read that file first and
mirror its setup rather than inventing one.

Assert exactly four things:

1. a non-admin with project access saves a profile carrying `project_id` → 200;
2. the same non-admin saving with an empty `project_id` → 403;
3. a non-admin without access to `proj-abc` saving into it → 403;
4. saving `{project_id: "proj-abc", key: "researcher"}` while a global
   `researcher` exists creates a **second** row and leaves the global row's
   provider unchanged.

Assertion 4 is the one that catches the dedup bug; do not skip it.

- [ ] **Step 7: Run**

Run: `go test ./internal/tools/agents/ -count=1`
Expected: PASS.

---

### Task 7: Shared FE client + editor

**Files:**
- Create: `fe/common/api/agentProfiles.ts`
- Modify: `fe/common/api/index.ts` (re-export)
- Create: `fe/common/ui/AgentProfileEditor.svelte`
- Test: `fe/common/api/__tests__/agentProfiles.test.ts`

**Interfaces:**
- Produces:
  - `type AgentProfile` with `project_id: string`
  - `listAgentProfiles(base: string, projectID?: string): Promise<{ profiles: AgentProfile[]; owned: AgentProfile[] }>`
  - `saveAgentProfile(base: string, p: AgentProfile): Promise<AgentProfile>`
  - `deleteAgentProfile(base: string, id: string): Promise<void>`
  - `<AgentProfileEditor {profile} {tags} {providers} readonly onsave ondelete oncancel />`

- [ ] **Step 1: Read the existing client conventions**

Read `fe/common/api/client.ts` and `fe/common/api/index.ts`. This repo uses
an effect-based client (`apiGetE` / `apiPostE`, `APIError` carrying
`.status`). Use those — do not hand-roll `fetch`.

- [ ] **Step 2: Write the failing client test**

`fe/common/api/__tests__/agentProfiles.test.ts` — mirror the mocking style
of the neighbouring tests in that folder. Assert:

- `listAgentProfiles(base)` requests `/api/agent-profiles` with **no**
  `project_id` parameter (an empty scope must not send `project_id=`);
- `listAgentProfiles(base, "proj-abc")` requests
  `/api/agent-profiles?project_id=proj-abc`;
- a response missing `owned` yields `owned: []` rather than `undefined`.

- [ ] **Step 3: Run to verify it fails**

Run: `npm --workspace=@wick-fe/common-api run test:unit`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement the client, re-export from `index.ts`, rerun**

Expected: PASS.

- [ ] **Step 5: Build the editor component**

`fe/common/ui/AgentProfileEditor.svelte`, Svelte 5 runes (`$props`,
`$state`, `$derived`). Fields, in order: Key, Name, Description, Icon,
Provider, Model, System prompt, Allowed tags, Allowed native tools, Strict
MCP, Can delegate, Allow take-over, Default max turns, Default max tokens,
Disabled.

Rules the form must encode:

- **Description is required.** The server rejects an empty one with a
  specific message; show it inline on the field, not as a toast.
- **`can_delegate` is disabled unless the provider is `claude`, `codex`, or
  `wick`.** The server forces it false otherwise; a checkbox that silently
  loses its value is worse than one that cannot be ticked. Show the reason.
- **`readonly`** renders every field disabled and hides Save/Delete — this
  is how a non-admin sees a global profile.

`{@const}` is only legal directly under `{#if}` / `{#each}` — hoist derived
values into the script as `$derived` instead.

- [ ] **Step 6: Typecheck**

Run: `npm --workspace=@wick-fe/common-ui run check`
Expected: clean.

---

### Task 8: Global-scope SPA

**Files:**
- Create: `fe/agents/agent-profiles/{index.html,package.json,svelte.config.js,tsconfig.json,vite.config.ts,vitest.config.ts}`
- Create: `fe/agents/agent-profiles/src/{main.ts,App.svelte,app.d.ts}`
- Create: `fe/agents/agent-profiles/src/lib/{router.ts,components/ProfileList.svelte}`
- Create: `internal/tools/agents/view/agent_profiles_spa.templ`
- Modify: `internal/tools/agents/handler.go` (route + page handler)
- Modify: `internal/tools/agents/view/layout.templ` (sidebar + `agentsMoreOpenAttr`)
- Modify: `fe/package.json` (`dev:agent-profiles` script)

**Interfaces:**
- Consumes: `listAgentProfiles` / `saveAgentProfile` / `deleteAgentProfile`,
  `AgentProfileEditor` (Task 7).

- [ ] **Step 1: Scaffold from Presets**

Copy `fe/agents/presets/` as the template — same shape, same router
approach. Change in `vite.config.ts`:

```ts
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/agent-profiles");
base: "/tools/agents/workflow/agent-profiles/",
server: { port: 5186, /* must not collide with an existing SPA port */ }
```

Check the ports already taken across `fe/agents/*/vite.config.ts` and pick
a free one. Name the package `@wick-fe/agents-agent-profiles`.

`//go:embed all:dist` in `internal/tools/agents/spa.go:13` already picks up
the new directory — no Go-side embed change is needed.

- [ ] **Step 2: Build the list + editor layout**

`App.svelte`: list on the left (global profiles, `+ New`), editor on the
right. Non-admins get `readonly` and no `+ New`.

- [ ] **Step 3: Add the templ shell**

Create `internal/tools/agents/view/agent_profiles_spa.templ`, copying
`presets_spa.templ` exactly and substituting the names. The
"bundle not built yet" branch must name the right workspace:
`npm --workspace=@wick-fe/agents-agent-profiles run build`.

Then run `templ generate`. Never edit `*_templ.go` by hand.

- [ ] **Step 4: Route and sidebar**

In `internal/tools/agents/handler.go`, beside the presets routes:

```go
	r.GET("/agent-profiles", agentProfilesPage)
```

```go
func agentProfilesPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.HTML(view.AgentProfilesSPA(view.AgentProfilesSPAVM{
		Layout:   sidebarVM(c, "agent-profiles", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("agent-profiles"),
	}))
}
```

In `internal/tools/agents/view/layout.templ`, add the entry inside the
"More" group next to Presets (line ~149):

```
@agentsNavLink(vm.Base+"/agent-profiles", "Sub-agents", vm.ActivePage == "agent-profiles")
```

and add `"agent-profiles"` to the `agentsMoreOpenAttr` case list at line
~690 — without it the group collapses and hides the active row.

- [ ] **Step 5: Build and smoke-test**

```bash
npm --workspace=@wick-fe/agents-agent-profiles run build
templ generate && go build ./...
```

Start the server, open `/tools/agents/agent-profiles`, create a profile,
reload, confirm it persists. **Then kill the process on port 8080** — this
repo has no live reload and a stray server blocks the next run.

---

### Task 9: Project-scope tab

**Files:**
- Modify: `fe/agents/project-settings/src/App.svelte`
- Create: `fe/agents/project-settings/src/lib/components/SubAgentsTab.svelte`

`ProjectSettingsForm.svelte` is 379 lines and is **not** modified — it
becomes the General tab as-is.

- [ ] **Step 1: Add the tab strip**

In `App.svelte`, `let tab = $state<"general" | "subagents">("general")`, two
buttons, and conditional rendering. The existing form moves inside the
`general` branch untouched.

- [ ] **Step 2: Build `SubAgentsTab.svelte`**

Calls `listAgentProfiles(base, projectID)` and renders two groups from the
one response:

- **Inherited from global** — rows in `profiles` whose `project_id` is
  `""`. Read-only, each with an **Override** button.
- **This project** — the `owned` array. Each with Edit and Delete.

A row in `owned` whose key also exists globally shows the literal label
`shadows global` (a `cau-*` chip — green is the accent, not a status).
Compute it by intersecting the two lists, not by trusting a server flag.

**Override** copies the global row into a new object with
`project_id = projectID` and `id = ""`, then opens `AgentProfileEditor`. It
is the only path that creates a shadow, so nobody makes one by reusing a
key without meaning to.

- [ ] **Step 3: Typecheck, build, verify**

```bash
npm --workspace=@wick-fe/agents-project-settings run check
npm --workspace=@wick-fe/agents-project-settings run build
```

Then in the browser: create a project-only role, confirm it appears for a
session in that project and **not** in another project; override a global
role and confirm the global one is unchanged on the global page. Kill port
8080 afterwards.

---

### Task 10: Docs

**Files:**
- Modify: `docs/guide/agents/sub-agents.md`

- [ ] **Step 1: Add a "Scope" section after "Mental model"**

Cover: global vs project roles; shadowing by key; a session with no project
sees globals only; another project's roles are never visible.

- [ ] **Step 2: Extend "Permissions"**

The current text says creating profiles is admin-only. That is now true of
**global** profiles only. State the split, and state the consequence
plainly:

> A project role can be created by anyone with access to the project, and
> those roles may set `strict_mcp: false` — which lets the sub-agent load
> the host's own MCP servers, outside wick's tag filter. Granting someone
> access to a project grants them that reach.

- [ ] **Step 3: Verify**

Run: `npm --prefix docs run build` (or the repo's documented docs build).
Expected: no broken links.

---

## Verification

- [ ] `go build ./... && go test ./... -count=1`
- [ ] `npm --prefix fe run check`
- [ ] `npm --prefix fe test`
- [ ] Compare failures against `master` (`git stash`) before calling any of
      them a regression — this tree has known pre-existing failures in
      `template/`, `browser.test.ts`, and `provider/codex`.
- [ ] Port 8080 is free.
