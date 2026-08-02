# Agent profile scoping — project vs global

Sub-agent roles (`entity.AgentProfile`) are global today: one flat namespace,
one unique `key`, admin-only to edit, and no UI at all. This adds a second
scope so a project can own roles that only exist inside it, and gives both
scopes a real editor.

Builds on [multi-agent/design.md](../../todo/multi-agent/design.md), which
landed the delegation core. Nothing here changes the governor.

## Status — shipped 2026-08-02

- [x] `AgentProfile.ProjectID` + composite unique `(project_id, key)`
- [x] Drop the stale `uniqueIndex` on `key` explicitly in `migrate.go`
- [x] `AgentDelegation.ProjectID` column, filled from `Request.ProjectID`
- [x] `delegation.ResolveScoped` — pure merge/shadow function + tests
- [x] `Repo.ListProfilesInScopes` / `GetProfileScoped` / `GetProfileExact`
- [x] Rewire 4 call sites (`WickAgents`, `WickDelegate`, `Run`, `takeover`)
- [x] `project_id` on the profile API + per-scope permission split
- [x] `fe/common/ui/AgentProfileEditor.svelte` + `fe/common/api` client
- [x] New SPA `fe/agents/agent-profiles/` + templ shell + route + sidebar
- [x] `Sub-agents` tab in project-settings
- [x] Docs: `docs/guide/agents/sub-agents.md` scoping section + trust note

### Three things landed differently from this plan

1. **A third repo method, `GetProfileExact`.** The plan had two. Save-dedup
   cannot use the resolved lookup: creating a project role named after a
   global one would find the global row and overwrite it, silently editing
   the role every other project sees. Exact-scope lookup, no fallback.
2. **The list response carries `inherited`, plus the editor's options.**
   The plan assumed the tab could derive shadowing from `profiles` +
   `owned`. It cannot — once a project row shadows a global one, the
   global row is gone from the merged list and nothing records that it was
   there. Both unresolved halves are returned. Tag and provider choices
   ride along on the same response rather than getting a route of their
   own, since the editor cannot render without them.
3. **Permission logic is a pure function, `canMutateProfileScope`.** The
   plan inlined the same eight lines in the save and delete handlers.
   Extracted so the rule is tested directly and cannot drift between the
   two paths.

## Follow-up shipped the same day: the connector move

Delegation moved off the top-level MCP surface into a fixed,
single-instance connector at `internal/connectors/sub-agents`. The four
tools (`wick_delegate`, `wick_agents`, `wick_delegate_collect`,
`wick_tasks`) are gone; the ops are `list_agents`, `delegate`, `collect`,
`create_agent`, `tasks`, reached through `wick_execute`.

Cost: one resolution hop per delegation. Bought: discovery via
`wick_list`, an admin page, tag visibility, and `connector_runs` audit —
plus `create_agent`, which lets an agent define a role mid-conversation.

Not tagged `System`, deliberately: `tags.System` is `IsFilter`+`IsSystem`
and no user carries it, so it hides the row from every non-admin. A
delegation surface only admins can see is one that does not exist for the
people who need it. Write safety comes from scope instead —
`create_agent` writes into the calling conversation's project and refuses
when there is none, so an agent-invented role cannot reach past the
project it was already working in.

### Three dead knobs found and two revived

Auditing every profile field against the spawn path turned up four that
were stored but never read: `system_prompt`, `allowed_native_tools`,
`strict_mcp`, `can_delegate`. `project.Defaults.SystemAddon` was dead
too, and predates all of this.

- **`system_prompt` — revived.** It is the role; without it a profile is
  a provider plus a turn budget wearing a role's name. Now written to the
  child session's `Meta.SystemAddon` and appended after the preset by the
  spawn factory.
- **`project.Defaults.SystemAddon` — revived** by the same path, project
  addon first so the session-specific one can refine it.
- **`strict_mcp` / `allowed_native_tools` — still dead**, now documented
  as such. Whether a spawn isolates MCP is decided globally by the
  `WICK_STRICT_MCP` env var, identically for leaders and sub-agents.
- **`can_delegate` — still dead.** No tool-list gating reads it.

### Retracted

An earlier revision of this doc and of `docs/guide/agents/sub-agents.md`
carried a security warning that `strict_mcp: false` lets a project role
reach the host's MCP servers, framed as an accepted risk. **That was
wrong** — the field is not read, so the control does not work in either
direction. Both documents now describe the actual behaviour. A security
claim that does not match the code is worse than none.

### Security fix that came with the move

`wick_execute` treated the `X-Wick-Session-Id` header as a *fallback*
behind any `session_id` the model supplied. Delegation had the opposite
precedence for a reason: the session decides which tree a call joins,
whose tags it inherits, and — since scoping landed — which project's
roles it can see. Precedence is now header-first for every connector
(`handlers.ResolveCallSession`), which is strictly safer and closes the
same gap for the Slack owner-bot resolution.

### Not verified

The stack was not exercised against a running server with a real login
session. Coverage is unit + repo-level, including the production boot path
(`Migrate` on a database carrying the old index). The HTTP handlers'
happy path is covered only through `canMutateProfileScope` and the DTO
round trip, not end to end.

## The model

```
agent_profiles
  project_id  key           provider
  ""          researcher    claude    ← global
  ""          reviewer      claude    ← global
  proj-abc    researcher    codex     ← shadows the global one
  proj-abc    db-migrator   claude    ← project-only

session(project = proj-abc) resolves to:
  researcher (codex)   reviewer (claude)   db-migrator (claude)
```

`project_id = ""` means global: visible from every project. A non-empty
value scopes the row to that project and hides it everywhere else. A
project row whose `key` matches a global row **shadows** it — the
project's version wins for sessions in that project, and the global row
is untouched for everyone else.

Sessions with no project resolve to the global scope alone, which is
exactly today's behaviour. Existing rows migrate to `project_id = ""`, so
nothing changes for anyone already using the feature.

## Where resolution lives

The repo does no merging. It runs one query returning both scopes, and a
pure function applies the rule:

```go
// ResolveScoped returns the profiles visible to a session in projectID:
// every global role, with any role the project defines under the same
// key substituted in. Pure — no DB, no context.
func ResolveScoped(profiles []entity.AgentProfile, projectID string) []entity.AgentProfile
```

Both the MCP roster and the web UI go through it, so the two surfaces
cannot drift — the same shape the codebase already uses for
`VisibleProfiles`. It is also testable without a database, which the
SQL-side alternative is not.

### The migration trap

GORM's `AutoMigrate` creates the new composite index but does **not** drop
the old `uniqueIndex` on `key`. Left in place, that index keeps rejecting
two rows with the same key, so shadowing can never be created — and the
failure surfaces as a constraint violation on save, far from its cause.
`migrate.go` drops it explicitly before `AutoMigrate`, idempotently.

A test asserts the post-migration state directly: on a database carrying
the old index, inserting the same key in two scopes must succeed.

## Call sites

| Location | Change |
|---|---|
| `WickAgents` (`internal/mcp/handlers/delegation.go:180`) | Read `X-Wick-Session-Id`, load the session for its `ProjectID`, then resolve. The roster is project-blind today. |
| `WickDelegate` (same file, `:256`) | **Reorder**: load the session before resolving the profile. Today `GetProfile` runs at `:256` while the project is only known at `:305`. |
| `Run` (`internal/agents/delegation/run.go:219`) | `GetProfileScoped(ctx, req.ProjectID, req.ProfileKey)`. `req.ProjectID` is already populated. |
| `takeover.go:51` | Resolve using the delegation's own `ProjectID`. |

`AgentDelegation` gains `ProjectID` for that last one. Without it,
taking over a sub-agent spawned from a project role would resolve the
*global* role of the same key — a silent mismatch, not an error.

## Permissions

Three rules, not a hierarchy:

- **Global profiles**: admin-only to create, edit, or delete. Unchanged.
- **Project profiles**: anyone who passes `callerProjectAccess(c).allowProject(id)`
  — the same gate `projectAccessMW` applies to `/api/projects/{id}`. All
  fields, including `strict_mcp` and `allowed_native_tools`.
- **Reading**: the existing tag rule (`VisibleProfiles`) still applies, on
  top of the resolved set. Scope decides what exists; tags decide what
  may be seen. Two filters, neither replacing the other.

### Accepted risk: project access is host-MCP trust

`strict_mcp = false` is a spawn argument, not a prompt. It makes the child
load the host's own MCP servers from `~/.claude.json` — tools that never
pass through wick's tag filter. Letting non-admins set it on project
profiles was chosen deliberately: **granting someone access to a project
grants them that reach.** Documented in the user guide so whoever hands
out project access knows what they are handing out.

The governor is untouched. Ceilings stay global and still clamp project
profiles. No new knobs — and therefore no new knobs that nothing reads.

## UI

One editor, two surfaces. The profile form is ~15 fields; written twice it
would drift the first time a field is added. It lives in
`fe/common/ui/AgentProfileEditor.svelte`, with the API client in
`fe/common/api/`. Each page supplies only the chrome and the scope.

### Surface A — `fe/agents/agent-profiles/`

A new SPA following the Presets pattern (list left, editor right, own
`router.ts`). Sidebar entry under **More**, labelled **Sub-agents** —
"Agents" would be ambiguous inside the Agents tool.

```
┌──────────────┬─────────────────────────────┐
│ GLOBAL       │  Key         researcher     │
│  researcher  │  Provider    claude      ▾  │
│  reviewer    │  Description [required]     │
│  + New       │  System prompt [.........]  │
│              │  Tags  ☐ ops  ☑ dev         │
│              │  Max turns  12              │
│              │  ☑ Strict MCP               │
│              │  ☐ Can delegate             │
│              │       [Save]  [Delete]      │
└──────────────┴─────────────────────────────┘
```

Non-admins see the list; Save, New and Delete are absent.

### Surface B — project-settings tab

`ProjectSettingsForm.svelte` is already 379 lines and is not touched. A tab
strip goes in `App.svelte` (17 lines); the existing form becomes **General**
and a new `SubAgentsTab.svelte` becomes **Sub-agents**.

```
Project › wick        [ General ][ Sub-agents ]

  Inherited from global
    researcher   claude              [Override]
    reviewer     claude              [Override]

  This project
    researcher   codex   shadows global   [Edit][Delete]
    db-migrator  claude                   [Edit][Delete]

    + New agent
```

**Override** copies a global role into the project under the same key and
opens the editor. It is the only way to create a shadow, so nobody creates
one by accidentally reusing a key. Shadowing rows carry the explicit label
`shadows global` rather than relying on colour alone.

Styling follows the project design system: Inter, 8px grid, named tokens,
a `dark:` counterpart on every colour class. The `shadows global` label
uses the `cau-*` ramp — green is the accent, not a status.

### API

`GET /api/agent-profiles?project_id=<id>` (absent or empty = global only),
`POST` carries `project_id` in the body, `DELETE` is unchanged. One
endpoint set, both surfaces.

## Testing

| Layer | Assertions |
|---|---|
| `ResolveScoped` (pure) | global-only; project shadows global; project-only; empty projectID yields globals alone; stable ordering |
| Repo + sqlite | same key in two scopes is accepted; same key in one scope is rejected — proves the composite index is actually in place |
| Migration | a database carrying the old `uniqueIndex` on `key` accepts a cross-scope duplicate after migrating |
| Handlers | non-admin may save a project profile and is refused on a global one; the `WickAgents` roster follows its session's project |
| Vitest | API client; inherited/project grouping in the list |

## Out of scope

Squads and boards stay global. Per-project governor overrides were
considered and dropped — four new knobs whose runtime effect would have to
be proven one by one, for no demand yet.
