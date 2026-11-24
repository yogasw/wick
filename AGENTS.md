# AGENTS.md

Operational rules for coding agents (Claude Code, etc.) working in this repo. Human onboarding lives in [README.md](README.md).

## Required workflow for every code change

Read this file first, even when editing through the GitHub API or an MCP
connector instead of a local checkout. Then read the development skill for the
subsystem (see "Skills to use") and one existing implementation next to the code
you are about to change. Copy its shape; do not invent a parallel helper,
component or architecture for a small fix.

1. **Scope.** Change only what the request needs. Unrelated cleanup goes in a
   separate change. Never revert or overwrite local edits you did not make.
2. **Implement** the whole change (code + tests + docs/skills) before committing.
3. **Check** with the commands in "Before declaring a task done" and read your
   own full diff once more for leftovers, debug output and accidental edits.
4. **Commit and push** following "Git and pull request quality".
5. **Report** the commands you actually ran and their result. A check you could
   not run is reported as *not run*, never as passed.

`develop` has no automatic CI. The checks above are the only gate, so run them
locally before every push; do not wait for, or point to, a CI that will not run.

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

Minimum for any Go change:

```bash
templ generate          # only when .templ files changed
gofmt -l .              # must print nothing for files you touched
go vet ./<changed packages>/...
go test ./<changed packages>/...      # add -race for concurrency changes
go build ./...
```

| Also changed | Additional check |
|---|---|
| `fe/` | `cd fe && npm run check`, plus the relevant unit tests; click through the changed UI in a browser |
| Host Access connector | `bash scripts/check-connector-quality.sh host_access` |
| External plugin | test inside the `plugins` module; rebuild the plugin when its manifest/contract changes |
| UI (any) | tool grid / Ctrl+K palette look right in light and dark mode |
| Docs only | re-read the rendered diff; check links and commands against current source |

- Bug fix → add a regression test that fails without the fix. New behaviour →
  unit tests for the normal case, edge cases and the error path. Assert
  behaviour, not source text.
- A pre-existing unrelated failure is reported separately; never weaken or
  skip a test to make it pass.
- Any process you started is stopped.

## Code conventions

### Readable code (the rules most often broken)

- **Formatting.** `gofmt` for Go, the repo's existing formatter for FE. No
  semicolon-chained statements, no several statements on one line, no
  hand-minified code, no 300-character lines.
- **Small named functions.** A handler parses input, calls named helpers and
  writes output. If a function needs a comment block to explain its sections,
  split it into functions named after those sections. Keep nesting shallow with
  early returns.
- **Comments explain why.** Every new package and exported identifier gets a doc
  comment. Inside functions, comment only non-obvious decisions: security,
  compatibility, concurrency, fallbacks, magic values. Do not narrate the code
  line by line, and update comments the code no longer matches.
- **Reuse before writing.** Search for an existing helper, component or
  pattern first (`grep` the symbol). Duplicated validation, a second HTTP
  client wrapper or a copy-pasted Svelte component is a review blocker.
- **Errors.** Wrap with context (`fmt.Errorf("load config: %w", err)`), pass
  `ctx` through, close what you open. No ignored errors, no silent fallback to
  a more permissive default.
- **Logs.** Use the existing structured logger with identifiers and durations.
  Never log credentials, tokens, env vars, command arguments or file contents.
- **UI.** Use the design system and shared components; keep light/dark theme,
  responsive layout and keyboard access working.

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

A commit is **one logical change**, not one edited file.

- Code, its tests and its docs/skill update go in the **same commit**. Never a
  commit per file, per typo or per formatting fix. Unrelated fixes go in
  separate commits.
- Each commit builds and passes its tests on its own.
- Message: Conventional Commits, `type(scope): imperative summary`. Non-trivial
  changes get a body: why, design trade-off, how it was verified. No "update
  file", "fix again", "WIP".
- **Editing through the GitHub API:** build one commit for all changed files with
  the Git Tree/Commit API and move the branch ref with a fast-forward update.
  Do not call the contents API once per file. If the available tool can only
  commit one file at a time, say so and work on a feature branch/draft PR, not
  on `develop`.
- Before committing, read `git status` and the staged diff: no build artifacts,
  generated noise, local config, secrets or unrelated files.
- No force push, history rewrite, hard reset or amending someone else's commit
  without explicit permission.
- Branches: small verified fixes may be pushed straight to `develop`; larger
  features, risky changes and anything you could not fully verify go through a
  PR against `develop`. `master` and release branches are not pushed to directly.
- A PR description states the problem, the solution, any migration/security
  impact, and the exact checks run (including skipped or failing ones).

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
