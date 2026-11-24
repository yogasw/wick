# AGENTS.md

Operational rules for coding agents (Claude Code, etc.) working in this repo. Human onboarding lives in [README.md](README.md).

## Required workflow for every code change

**Read this file before editing Wick, even when working through a GitHub API
or an MCP connector rather than a local checkout.** Read the nearest scoped
`AGENTS.md` files and the relevant development skills, then inspect an existing
implementation in the target subsystem. Do not assume rules from an earlier
conversation are still current. The user's requested behavior defines the
scope; unrelated cleanup belongs in a separate change.

1. Inspect the current branch, working tree and applicable conventions. Preserve
   unrelated local edits and understand the existing design before replacing it.
2. Plan a small, coherent change with its tests and documentation. Reuse existing
   abstractions, shared UI components and helpers rather than creating parallel
   implementations.
3. Implement and review the complete change in a working tree or staged batch
   of files, not as a sequence of remote one-file commits.
4. Format, lint, compile and test the affected code. Review the full diff for
   correctness, readability, security, regressions and accidental changes.
5. Commit a logical, independently buildable change including its tests and
   documentation. Open the PR against the intended branch (normally
   `develop` for Wick development). Report the checks actually executed.
6. Merge only after required checks pass and review blockers are resolved.
   For runtime changes, verify the installed version/commit and affected
   behavior after deployment. Documentation-only changes do not require a
   binary rebuild.

If the available tools cannot run a required check, say exactly what could not
be verified. Do not treat an unavailable check as a pass. A draft PR or clearly
identified unverified review branch is acceptable; a claimed verified merge
is not. Never bypass failing checks merely to finish quickly.

## Running the app (agent flow)

When an agent needs to start the server — e.g. to verify a UI change — use a **one-shot** flow, not watch mode:

1. Make sure the Tailwind CLI is present (see below).
2. Build CSS once:
   - Windows: `./bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css --minify`
   - macOS/Linux: `./bin/tailwindcss -i web/src/input.css -o web/public/css/app.css --minify`
3. Run the server: `go run main.go server`
4. **Always kill the server when you're done.** Don't leave background processes hanging.

Do NOT run `make css/watch`, `make dev`, or `make run/live` from an agent session — watchers are for interactive development, they don't terminate, and they waste the user's resources.

### Tailwind CLI presence check

The standalone Tailwind binary is OS-specific and not committed. Before building CSS:

```bash
ls ./bin/tailwindcss* 2>/dev/null
```

If nothing is found, run `make setup` once — it auto-detects OS/arch and downloads the right binary to `./bin/`.

### Killing processes

- Anything **you** started (server, watcher, build job) must be killed before handing control back.
- If you encounter a process **the user** started (e.g. their own dev server already running on :9425), do NOT kill it silently. Ask first: "Looks like something is already running on :9425. OK to kill it, or are you using it?"
- Prefer `taskkill //F //PID <pid>` on Windows (git bash), `kill <pid>` elsewhere.

## Quick commands

| Task | Command |
|---|---|
| Build CSS once | `./bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css --minify` |
| Regenerate templ | `templ generate` |
| Compile check | `go build ./...` |
| Run server (foreground) | `go run main.go server` |
| Run tests | `go test ./...` |
| Setup Tailwind CLI | `make setup` (one-time) |

## Before declaring a task done

- For runtime code changes, the applicable compile, lint and test checks below pass; generate templ output when required. For docs-only changes, review the documentation diff.
- Any process you started is stopped.
- Changes on the tool grid / Ctrl+K palette look right in both light and dark mode (if UI changed).

## Code conventions

### Readability, architecture and maintenance

- Match the surrounding project conventions. Go uses `gofmt` indentation and
  alignment; TypeScript/Svelte/CSS use the repository's existing formatter and
  style. Do not compress control flow into semicolon-separated one-liners or
  manually minify source. Generated files follow their generator's convention.
- Give new packages, plugin entry points, exported types and functions useful
  documentation. For internal helpers, explain purpose, inputs, outputs,
  invariants, side effects or error semantics when they are not obvious.
  Explain **why** a decision was made, especially security, compatibility,
  concurrency, caching, fallback and lifecycle decisions. Keep comments accurate
  when implementation changes; do not pad obvious getters with redundant prose.
- Keep functions focused and name them by intent. Separate configuration,
  operation declarations, business logic, policy, OS/network I/O, and UI
  rendering where their complexity warrants it. Avoid deep nesting, duplicated
  validation, magic constants and unrelated abstractions. Follow the existing
  subsystem layout rather than inventing a new architecture for a small fix.
- Keep error handling explicit. Propagate context cancellation and deadlines,
  close resources, bound input/output and concurrency, and preserve meaningful
  error context. Do not swallow failures or silently fall back to permissive
  security defaults.
- Use the existing structured logger for meaningful operation lifecycle and
  error events, with appropriate levels and identifiers/durations where useful.
  Do not log credentials, raw sensitive inputs, command arguments, environment
  variables or file contents. Avoid duplicate or noisy logs.
- For UI changes, reuse the design system and shared components, preserve theme,
  responsive behavior and accessibility, and verify interaction in a browser
  when available. Do not rely solely on screenshots of static markup.
- For plugins, respect the plugin boundary, the existing SDK, access policy
  and versioning. Use the public `pkg/safeexec` compatibility helpers for
  subprocesses that may run on Termux; these helpers are not a security sandbox.

### Verification before committing

| Change | Minimum relevant checks |
|---|---|
| Go code | Format changed handwritten files with `gofmt`, run `go vet` and `go test` for affected packages; use existing lint configuration if present. |
| Go templates / backend UI | Generate templ output as required, compile affected packages and test rendering/behavior. |
| External connector plugins | Test within the plugins module/workspace; verify plugin build/manifest when changing its contract. For Host Access, run `bash scripts/check-connector-quality.sh host_access`. |
| Frontend | Use the existing workspace checks/tests/build commands (for example `cd fe && npm run check` and the relevant unit tests). Verify changed interaction in the browser when available. |
| Docs only | Review the diff, links, examples and consistency with current source; no application rebuild is required. |

Add regression tests with bug fixes and unit tests for new behavior, covering
normal cases, edge cases, errors and relevant security boundaries. Favor
behavioral assertions over brittle source-text checks. Run broader checks when
shared contracts, core code, or cross-package behavior changes. For concurrency
changes, use race testing where supported. Report existing unrelated failures
separately; do not hide them by weakening tests. Use the tools and commands
actually available in the repository; do not invent a passing lint result.

### Connector code quality

Use `plugins/connector/httpbin/main.go` and `connector.go` as the style reference.
Keep package comments, a readable module declaration, typed input schemas and
named handlers. Split policy, validation, I/O and UI rendering by responsibility.
Explain why each non-obvious function or security decision exists; comments
should describe invariants and trade-offs, not repeat the code. Avoid dense
semicolon-separated statements and inline multi-step handlers. Use structured
logs for operation outcomes and durations; never log credentials or raw file
and command payloads.

Run `gofmt -w` on changed Go files before committing, then run `go vet` and
the relevant unit tests. For Host Access use
`bash scripts/check-connector-quality.sh host_access`. Report actual test results;
do not call a change verified merely because its build compiled.
Use `github.com/yogasw/wick/pkg/safeexec` for subprocess execution, including
plugins that run on Termux. Resolve executable names before command creation.
Safeexec is a compatibility layer, not a sandbox.

### Never call `os/exec.LookPath` directly in host runtime — use `pkg/safeexec.LookPath`

Go's `os/exec.LookPath` uses the `faccessat2(2)` syscall to check executable permissions. Android's seccomp filter on devices that still ship a 4.x kernel (Android 13 userland on top of kernel 4.14 is a real, in-the-wild combination) rejects `faccessat2` with `SIGSYS` instead of the `ENOSYS` that Go's runtime expects, killing the process before its fallback to plain `faccessat` can fire.

Anything that runs inside `wick-agent` (everything under `internal/agents/`, `internal/pkg/api/`, `app/`, and code reachable from `template/main.go`) must avoid `exec.LookPath`:

- **Replace `exec.LookPath(name)`** with `safeexec.LookPath(name)`.
- **Before `exec.Command(name, ...)` where `name` may be a bare binary name** (anything without `/`), call `safeexec.ResolveBin(name)` first and pass the resolved absolute path. `exec.Command` calls `LookPath` internally during `Cmd` construction whenever the argument has no slash, which trips the same `faccessat2` chain.

CLI tooling (`cmd/cli/`, `internal/builder/`, `internal/updater/`) can also run on Termux. Prefer `pkg/safeexec` for new or changed subprocess call sites; do not assume the developer's kernel supports `faccessat2`. Preserve existing working compatibility helpers.

## Git and pull request quality

A commit represents a **logical change, not an individual file edit**.

- Group related source, regression tests, comments, formatting, version changes
  and documentation together. Finish and review a logical unit before committing.
  Do not create a commit for each file, typo, indentation correction or routine
  intermediate fix. Do not split a feature into artificially small commits;
  conversely, do not combine unrelated fixes merely to reduce commit count.
- Each commit should be independently buildable and testable in its context.
  Prefer a single atomic commit for a small change, or a small series of coherent
  commits for a larger feature. Keep separate issues in separate PRs when they
  can be reviewed and deployed independently.
- Use Conventional Commits: `type(scope): imperative summary` (scope optional).
  For non-trivial changes, include a body explaining the motivation, design
  trade-offs, compatibility and verification. Avoid vague messages such as
  "update file", "fix again" or "WIP" in review-ready history.
- Prefer local editing, staging, and a single commit for a related file set.
  When using GitHub API tools, use the Git Tree/Commit API to create an atomic
  multi-file change against the current parent, then update the feature branch
  with an expected-head check and no force. Do not call the contents API once
  per changed file when an atomic batch is available. If the tools cannot
  provide an atomic commit, disclose that limitation and prefer a reviewable
  draft over a long chain of unverified micro-commits.
- Inspect `git status` and the staged diff (or the equivalent API diff) before
  committing. Exclude generated noise, secrets, local configuration, unrelated
  changes and build artifacts unless intentionally required. Review the full
  PR diff against the target branch before requesting merge.
- Do not rewrite published or merged history, force push, hard reset, or amend
  someone else's commits without explicit authorization. Squash merge is an
  option for keeping target-branch history clean when the repository policy
  permits it; it is not permission to bypass checks or discard useful history.
- PR descriptions should state the problem, solution, relevant security or
  migration implications, and exact verification results (including skipped or
  failing checks). Keep the PR focused and update its description if scope
  changes. Required CI checks and branch protections must not be bypassed.

## Skills to use

Two separate sets. Which one applies depends on whether you are **editing wick**
or **using it**.

### Development skills — `.claude/skills/`

Read before touching the subsystem each one covers. They reference real paths in
this repo, so they only make sense with the repo checked out.

- `tool-module` — any work under `internal/tools/{tool}/` **or** `internal/jobs/{job}/`. Covers both surfaces — they share the same Config reflection, `wick:"..."` tag grammar, and bootstrap contract.
- `connector-module` — any work under `internal/connectors/{connector}/` or anything that touches the MCP surface (`internal/mcp/`).
- `plugin-module` — packaging a connector as an external plugin binary (the `plugins` monorepo, `wick plugin build`, catalog, signing).
- `workflow-node-module` — any work on a workflow node executor under `internal/agents/workflow/nodes/`.
- `agent-channel-module` — any work on an agent channel transport under `internal/agents/channels/`.
- `agent-resource-guard` — the memory guard, CPU/IO controls, OOM reporting, usage history, and the Resources page.
- `fe-module` — any work on the Svelte SPAs or shared FE libraries under `fe/`.
- `design-system` — any UI styling decision (colors, spacing, typography).
- `config-tags` — adding/editing `wick:"..."` config fields (widget types, modifiers, key derivation).
- `encrypted-fields` — any connector that handles credentials, tokens, or sensitive values flowing through the LLM. Covers the `secret` tag (auto-mask), `c.Mask` / `c.MaskIgnoreCase` for dynamic response data, and the `wick_enc_` token contract.

### Shipped skills — `internal/agents/skillsync/builtin/`

These are embedded in the binary and describe how to **use** wick, for agents
running on a machine that has no wick checkout. They are extracted to
`~/.<app>/builtin-skills/` on boot and on every skill sync, and that directory
is **wiped and rewritten** each time — never edit the extracted copy, edit the
source here.

`wick-agents`, `wick-connectors`, `wick-plugins`, `wick-workflows`,
`wick-resource-limits`, `wick-rich-output`, `wick-slack-replies`.

Keep them free of `internal/...` paths: a user reading one has no such files.

### Keeping skills current

**A change to behaviour a skill documents is not finished until that skill is
updated in the same commit.** A stale skill is worse than a missing one — it
sends the next agent confidently in the wrong direction, and nothing in the
build catches it.

This applies in both directions:

- Changed a config default, a tag, a contract, a file layout, or a rule inside a
  subsystem → update the matching development skill.
- Changed something a **user** sees or does — a CLI command, a config key, a
  page, an MCP op, a fence, a channel behaviour → update the matching shipped
  skill, since it ships to machines that cannot read this repo.

When a change spans both, update both. When you are unsure a skill covers what
you changed, grep it for the symbol or key you touched.

## Module surfaces at a glance

| Surface | URL | Who | Purpose |
|---|---|---|---|
| Tool UI | `/tools/{key}` | End user / operator | Tool home page (module's own templ) |
| Job operator | `/jobs/{key}` | End user / operator | Run Now button + last run info + history |
| Job admin | `/manager/jobs/{key}` | Admin | Schedule + config (writable settings) |
| Tool admin | `/manager/tools/{key}` | Admin | Tool config only (no schedule) |

## Register signatures

```go
// Tool with typed Config (runtime-editable knobs)
app.RegisterTool(meta tool.Tool, cfg C, register func(tool.Router))

// Tool with no knobs (external-link cards, pure-compute tools)
app.RegisterToolNoConfig(meta tool.Tool, register func(tool.Router))

// Scheduled job with typed Config
app.RegisterJob(meta job.Meta, cfg C, run func(ctx) (string, error))

// Job with no knobs
app.RegisterJobNoConfig(meta job.Meta, run func(ctx) (string, error))
```

Handlers and Run funcs are top-level stateless funcs. No `NewTool()` / `NewJob()`. No per-module struct. Metadata always lives at the register call site, not inside the module.
