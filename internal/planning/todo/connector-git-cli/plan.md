# Git CLI Connector Plugin — Design

A wick connector plugin that wraps the **local `git` CLI** for LLM use, with a policy
engine that blocks unsafe operations (bad branch names, pushes to protected branches,
arbitrary subcommands) and HTTPS credentials injected per-instance without ever
touching disk.

Provider-agnostic by design: it wraps `git`, not a hosting API, so GitHub, Bitbucket,
GitLab and self-hosted servers all work through the same operations. The existing
`github` / `bitbucket` plugins stay separate — they cover what `git` cannot (pull
requests, issues, reviews). No merging, no overlap.

## TODO

- [ ] Scaffold `plugins/connector/git/` from `_template`, `Key: "git"`, VERSION `0.1.0`
- [ ] `policy.go` — `EffectivePolicy` resolve (global → per-repo) + evaluate + `matched_rule`
- [ ] `policy_test.go` — table tests: branch pattern, protected glob, specificity, `"-"` unset, fail-closed
- [ ] `remote.go` — read `origin`, strip embedded credentials, SSH→HTTPS conversion, host map
- [ ] `remote_test.go` — conversion table incl. the two must-fail cases (ssh alias, unmapped self-hosted)
- [ ] `git.go` — argv builder (`userArgs` vs `injectedArgs` split), env allowlist, timeout, output cap
- [ ] `git_test.go` — deny-list coverage, env leak checks, truncation, process-group kill
- [ ] `service.go` — per-op input validation and argument assembly
- [ ] `connector.go` — Meta, Config, Input structs, `Operations()`
- [ ] `main.go` — `wickplugin.Serve(Module())` + `--askpass` mode
- [ ] `policyui.go` — `policy_manager` / `policy_simulate` / `policy_rule_save` config-only ops + widget (inline styles, CSS vars)
- [ ] `docs/connectors/git.md`
- [ ] Build check: `wick plugin build git`

Deferred (not in this scope): SSH key auth, remote execution on another host,
policy DSL, writing credentials into `.git/config`.

---

## 1. Architecture

Out-of-tree plugin at `plugins/connector/git/`, following the `pkg/connector`
contract (`Module` / `Meta` / `Configs` / `Category` / `Op`) exactly as
`plugins/connector/github/` does. Built and distributed separately via
`wick plugin build git`.

- `Key: "git"`, display name **"Git CLI"**, icon 🌿
- One instance = one git identity (author + HTTPS credential + policy set)
- Prerequisite: `git` on the PATH of the machine running wick. Missing → every op
  fails with `git not found in PATH`, never a vague error.

### Execution pipeline

Every operation passes through four stages in order:

```
1. resolve   repo_path exists? contains .git? git on PATH?
2. policy    EffectivePolicy evaluated; deny → stop, no process is spawned
3. build     argv = [git] + injectedArgs + subcommandArgs
             userArgs (from the agent) are kept SEPARATE and filtered
4. exec      env allowlist + askpass + timeout + output cap → typed result
```

Stage 2 runs **before** any process spawn — a denied operation costs nothing and
changes nothing.

**The `userArgs` / `injectedArgs` split in stage 3 is structural, not a
convention.** The deny-list applies only to `userArgs`; `injectedArgs` are what the
plugin itself adds (askpass config, hooks path, identity). If both live in one
slice, the deny-list leaks and the plugin blocks its own injections. Two fields,
two code paths, joined only at `exec.Command`.

### Workdir

No path allowlist — the point is to manage repos already cloned on the machine.
`repo_path` is any directory that contains `.git`. Guards live on **arguments and
environment**, not on paths (see §5).

### File layout

```
plugins/connector/git/
  main.go        # wickplugin.Serve(Module()) + --askpass mode
  connector.go   # Meta + Config + Input structs + Operations()
  policy.go      # EffectivePolicy: resolve two layers → evaluate
  policy_test.go
  git.go         # runner: argv build, env allowlist, timeout, output cap
  git_test.go
  remote.go      # read origin, strip credentials, SSH→HTTPS
  remote_test.go
  service.go     # per-op input validation + argument assembly
  VERSION
```

`main.go` has two modes: normal (gRPC plugin) and `--askpass` (git calls the
binary, it echoes the token from env and exits). One binary, two roles — this is
what makes askpass work with no extra file on disk.

---

## 2. Config

Fields are grouped with `group=` so the admin page renders readable cards.

```go
type Config struct {
    // ── Identity ──
    AuthorName  string `wick:"group=Identity;desc=Name for commits made through this connector. Example: Deploy Bot"`
    AuthorEmail string `wick:"group=Identity;desc=Email for commits. Example: bot@example.com"`

    // ── Auth (HTTPS) ──
    Username   string `wick:"group=Auth;desc=HTTPS username. For a GitHub PAT use x-access-token."`
    Token      string `wick:"secret;group=Auth;desc=Personal access token or app password. Injected via askpass; never written to disk."`
    AuthMethod string `wick:"dropdown=askpass|credential_helper|extraheader;group=Auth;desc=How the token reaches git. askpass is safest (default). extraheader exposes the token in the process list."`

    // ── Remote ──
    ConvertSSHRemoteToHTTPS bool   `wick:"bool;group=Remote;desc=Rewrite SSH remotes to HTTPS for network operations. Default true."`
    RemoteHostMap           string `wick:"kvlist=ssh_host|https_host;group=Remote;desc=Map SSH hosts to HTTPS hosts for self-hosted servers. Leave empty for GitHub/GitLab/Bitbucket."`

    // ── Policy ──
    //
    // One group, not two. Global rules and per-repo overrides are the same decision
    // at different scopes — global is the fallback, per-repo wins — and splitting them
    // into separate cards hid that relationship. The group is named "Policy" rather
    // than "Branch Policy" because it already covers more than branches (commit
    // messages) and is where any future rule belongs.
    BranchNamePattern    string `wick:"group=Policy;desc=Regex a NEW branch name must match. Example: ^(fix|feat|chore)/[a-z0-9._-]+$"`
    ProtectedBranches    string `wick:"kvlist=branch;group=Policy;desc=Protected branches. Direct push is blocked. Globs allowed: release/*"`
    AllowForcePush       bool   `wick:"bool;group=Policy;desc=Allow --force / --force-with-lease. Default false."`
    CommitMessagePattern string `wick:"group=Policy;desc=Regex a commit message must match. Empty accepts any message."`

    // Per-repo overrides, in the same group, below the globals they override.
    RepoPolicies  string `wick:"hidden;desc=Per-repo policy rows, managed by the Policy widget."`
    PolicyManager string `wick:"html=policy_manager;group=Policy;desc=Edit and test per-repo policy rules."`

    // ── Raw operation ──
    RawEnabled bool   `wick:"bool;group=Raw Operation;desc=Enable the raw operation (arbitrary git subcommands). Default false."`
    RawRules   string `wick:"kvlist=subcommand|mode;group=Raw Operation;desc=Per-subcommand rules. mode: allow or deny. Unlisted subcommands are denied."`

    // ── Runtime ──
    AllowHooks            bool `wick:"bool;group=Runtime;desc=Let repo hooks (.git/hooks) run. Default false."`
    TimeoutSeconds        int  `wick:"group=Runtime;desc=Timeout for non-network operations. Default 60."`
    NetworkTimeoutSeconds int  `wick:"group=Runtime;desc=Timeout for network operations (push/pull/fetch/clone). Default 180."`
    MaxOutputBytes        int  `wick:"group=Runtime;desc=Output size cap. Beyond this the result is truncated and flagged. Default 262144."`
}
```

Policy lists use `kvlist` (editable table) rather than textareas — no comma or JSON
parsing for the admin, and each column is self-describing.

### Layer resolution

Both layers compile into a single `EffectivePolicy` before evaluation. **No operation
reads raw config.**

```
layer 1 (global fields)   → base
layer 2 (repo_policies)   → wins over layer 1, only for matching repos
```

Rules that make this unambiguous:

1. **Deny beats allow, across layers.** A per-repo row may add restrictions or replace
   a pattern. It cannot silently un-protect a branch.
2. **Empty column = not specified** → inherit the global value. Not "allow anything".
   Fail closed.
3. **`"-"` = explicitly clear the inherited value.** This is the only way to express
   "this repo has no protected branches". Without it, a permissive per-repo row would
   look like it applies but quietly inherit the global list.

   `"-"` applies to the **list and pattern** columns (`protected`, `branch_pattern`,
   `message_pattern`),
   where "no value" is a meaningful state. On the boolean `force_push` there is no
   third state to clear to — allow and deny are the only options — so `"-"` there
   means the same as empty: inherit.
4. **`RawEnabled=false` or an empty `RawRules` → `raw` denies everything.** Both must
   be set deliberately.
5. Every verdict carries `matched_rule` (which layer, which row) into the response, so
   "why was my push blocked" is answerable without reading code.

### Matching semantics

Two different languages appear in one row — the most common source of mistakes:

| Column | Language | Matched against |
|---|---|---|
| `repo` | glob (`*`) | both the local path **and** `host/owner/repo` |
| `branch_pattern` | regex (Go RE2) | branch name, when the branch is CREATED |
| `message_pattern` | regex (Go RE2) | the whole commit message, on `commit` only |
| `protected` | comma-separated globs | branch name |
| `force_push` | `true` / `false` / empty or `-` (both inherit) | — |

Most specific wins, measured by wildcard count: `*/org/sandbox` (1) beats `*/org/*`
(2). Ties go to the **stricter** row, and a tie that strictness cannot break falls
back to lexicographic order on the `repo` glob — results must never depend on JSON
ordering, and "stricter" alone is not a total order.

---

## 3. Policy Cookbook

Literal, paste-ready values. This section doubles as the contract for asking an AI to
generate a policy later.

### Shape contract

Every field below lives in the **Policy** group (renamed from "Branch Policy" once it
grew past branches — it now also covers commit messages, and is the place any future
rule belongs).

Plain string fields, one value each:

```
branch_name_pattern     <regex>   a NEW branch name must match this
commit_message_pattern  <regex>   a commit message must match this
allow_force_push        <bool>    --force / --force-with-lease
```

List fields, stored as a JSON array of string-keyed objects:

```
protected_branches : [{"branch": "<name or glob>"}]
repo_policies      : [{"repo":"<glob>", "branch_pattern":"<regex or empty>",
                       "message_pattern":"<regex or empty>",
                       "protected":"<csv globs, or - to clear>", "force_push":"true|false|empty (inherit)"}]
raw_rules          : [{"subcommand":"<git subcommand>", "mode":"allow|deny"}]
remote_host_map    : [{"ssh_host":"<host>", "https_host":"<host[/path]>"}]
```

Non-negotiable facts a generator must know:

- `branch_pattern`, `message_pattern` and the global `commit_message_pattern` are
  **regexes** (Go RE2, no lookahead, unanchored unless you write `^…$`); `repo` and
  `protected` are **globs** where `*` is the only metacharacter. Different languages,
  sometimes on the same row — a glob typed into a pattern column compiles as a regex
  and then matches almost nothing.
- Every layer-1 pattern has a layer-2 counterpart, so one repo can demand
  Conventional Commits while another demands a ticket id: `commit_message_pattern`
  (global) is overridden per repo by `message_pattern`.
- Empty column = inherit. `-` = clear the inheritance.
- Unlisted `raw` subcommand = denied.
- Specificity by wildcard count; ties → stricter.
- **Which rule gates which operation** — getting this wrong produces a policy that
  looks strict and blocks nothing, or one that blocks work nobody meant to stop:

  | Rule | Applies to | Does NOT apply to |
  |---|---|---|
  | `branch_name_pattern` | creating a branch (`branch_create`, `checkout` with create) | pushing to a branch that already exists |
  | `protected_branches` | `push`, `commit`, `merge`, `pull` on that branch | reading it (`log`, `diff`, `show`) |
  | `commit_message_pattern` | `commit` only | `push` — it carries no message of its own |
  | `message_pattern` (per repo) | `commit` in repos the row matches | `merge`, `rebase` — those messages are git's, not the operator's |
  | `allow_force_push` | `push --force`, `reset --hard` | anything non-destructive |

  The branch pattern deliberately stops at creation: if it also gated pushes, nobody
  could push to any pre-existing branch whose name predates the pattern.
- A regex that does not compile **blocks every mutation** and leaves reads working,
  rather than being ignored. Silence would be the dangerous reading.

### Scenario 1 — standard team: feature branches required, master/main closed

```
branch_name_pattern = ^(fix|feat|chore|hotfix|docs)/[a-z0-9._-]+$
allow_force_push    = false
raw_enabled         = false
```
```json
// protected_branches
[{"branch":"master"},{"branch":"main"},{"branch":"develop"}]
// repo_policies
[]
```

`push origin fix/login` ✅ · `push origin master` ❌ protected ·
`checkout -b hotfix/x` ✅ · `checkout -b temp` ❌ pattern.

### Scenario 2 — infra repo stricter than global

```json
// repo_policies
[
  {"repo":"*/org/infra","branch_pattern":"^ops/[a-z0-9-]+$","protected":"master,main,release/*","force_push":"false"}
]
```

Elsewhere `fix/x` still passes (global). In `org/infra` only `ops/*` passes, and
`release/2024-01` is closed too.

### Scenario 3 — free sandbox, strict everywhere else

```json
// repo_policies
[
  {"repo":"*/org/sandbox","branch_pattern":".*","protected":"-","force_push":"true"},
  {"repo":"*/org/*","branch_pattern":"^(fix|feat)/.+$","protected":"master,main","force_push":"false"}
]
```

`*/org/sandbox` (1 wildcard) beats `*/org/*` (2), so ordering is irrelevant. Note
`"protected":"-"` — with `""` the sandbox row would inherit the global protected list
and "free sandbox" would not actually be free.

### Scenario 4 — Conventional Commits enforced

```
commit_message_pattern = ^(feat|fix|chore|docs|refactor|test|perf)(\([a-z0-9-]+\))?!?: .{1,72}$
```

Accepts `fix: stop the login timeout`, `feat(auth): add SSO`, `chore!: drop node 18`.
Rejects `wip`, `updates`, `Fix login` (capital F is not in the type list), and a
subject longer than 72 characters.

Two things to get right, both easy to miss:

- The rule fires on `commit` only. A `push` carries no message of its own, so
  refusing one there would reject the commit that already happened.
- An **empty** message is left to git, which refuses it with its own clearer error.
  The pattern is not consulted.

Test it in the panel before relying on it: enter a message in *Commit message* and
the report says accepted or names the pattern it failed.

### Scenario 4b — two repos, two commit conventions

The global convention is Conventional Commits, but one repo is tracked in a ticket
system and one is a scratch repo where any message is fine:

```
commit_message_pattern = ^(feat|fix|chore)(\(.+\))?: .{10,}
```
```json
// repo_policies
[
  {"repo":"*/org/tickets","message_pattern":"^[A-Z]+-[0-9]+ .+"},
  {"repo":"*/org/scratch","message_pattern":"-"}
]
```

In `org/tickets`, `ABC-12 fix the timeout` passes and `fix: the timeout` is refused —
an override **replaces** the fallback rather than adding to it, so the global format
stops applying where a row matches. In `org/scratch` the `-` clears the rule and `wip`
passes. Everywhere else the global pattern still holds.

The mistake to avoid: leaving `org/scratch`'s column **empty** instead of `-`. Empty
inherits, so the scratch repo would silently keep demanding Conventional Commits.

### Scenario 5 — raw opened for read-only subcommands

```
raw_enabled = true
```
```json
// raw_rules
[
  {"subcommand":"bisect","mode":"allow"},
  {"subcommand":"worktree","mode":"allow"},
  {"subcommand":"describe","mode":"allow"},
  {"subcommand":"notes","mode":"allow"},
  {"subcommand":"blame","mode":"allow"},
  {"subcommand":"push","mode":"deny"},
  {"subcommand":"reset","mode":"deny"},
  {"subcommand":"clean","mode":"deny"},
  {"subcommand":"filter-branch","mode":"deny"}
]
```

The `deny` rows are redundant (unlisted is already denied) but written on purpose so
the intent reads clearly to whoever opens this config next.

**`bisect` here illustrates the shape, not a safe default.** `git bisect run <cmd>`
takes a command by design, so allow-listing `bisect` grants code execution to anyone
who can call the op — see "What is not contained" in §5. Allow-list it only when you
actually want that.

### Scenario 6 — self-hosted, SSH host differs from HTTPS

```json
// remote_host_map
[
  {"ssh_host":"git.internal","https_host":"code.company.com/git"},
  {"ssh_host":"ssh.abc.net","https_host":"abc.net"}
]
```

`git@git.internal:team/api.git` → network operations use
`https://code.company.com/git/team/api.git`, and the response reports
`converted: true` with both the original and effective URL.

---

## 4. Operations

Frequently-used subcommands get typed operations; everything else goes through `raw`.

### Read (`Op`)

| Key | Input | Notes |
|---|---|---|
| `status` | `repo_path` | returns raw `--porcelain=v2 --branch` output in the standard envelope |
| `log` | `repo_path`, `ref`, `limit` (default 20), `path`, `since` | `limit` keeps output small before the cap hits |
| `diff` | `repo_path`, `ref_a`, `ref_b`, `path`, `stat_only`, `max_lines` | `stat_only` returns `--stat` only |
| `branch_list` | `repo_path`, `remote`, `pattern` | |
| `show` | `repo_path`, `ref` | single commit |
| `remote_list` | `repo_path` | reports original **and** effective URL per remote |
| `ls_remote` | `repo_path`, `remote` | network; verifies auth without mutating |

### Mutating (`Op`, policy-gated)

| Key | Input | Policy checks |
|---|---|---|
| `branch_create` | `repo_path`, `name`, `from_ref`, `checkout` | `branch_name_pattern`; name must not be protected |
| `checkout` | `repo_path`, `ref`, `create` | if `create`, same as `branch_create` |
| `add` | `repo_path`, `paths` | — |
| `commit` | `repo_path`, `message`, `all`, `dry_run` | current branch must not be protected; `message` must match `commit_message_pattern` when one is set |
| `stash` | `repo_path`, `action` (`push\|pop\|list`), `message` | `drop` is **not** here — see `stash_drop` below |
| `fetch` | `repo_path`, `remote`, `prune` | network |
| `pull` | `repo_path`, `remote`, `branch`, `rebase` | network |
| `tag` | `repo_path`, `name`, `ref`, `message` | create/annotate only; deletion is `tag_delete` below |

### Destructive (`OpDestructive`, off by default per instance)

| Key | Input | Policy checks |
|---|---|---|
| `push` | `repo_path`, `remote`, `branch`, `force`, `set_upstream`, `dry_run` | target branch not protected; `force` needs `allow_force_push`; branch pattern applies to new branches |
| `merge` | `repo_path`, `ref`, `no_ff`, `abort` | current branch not protected |
| `reset` | `repo_path`, `mode`, `ref` | `--hard` needs `allow_force_push` |
| `rebase` | `repo_path`, `onto`, `abort`, `continue` | never interactive (`-i` rejected) |
| `clone` | `url`, `dest`, `branch`, `depth` | network; `url` host must resolve through the host map |
| `stash_drop` | `repo_path`, `ref` | dropped stashes are unrecoverable |
| `tag_delete` | `repo_path`, `name`, `remote` | deleting a remote tag is a network mutation |
| `raw` | `repo_path`, `args`, `dry_run` | `raw_enabled` + `raw_rules`; arg deny-list; unknown subcommand → denied |

Destructiveness is a property of the **operation**, not of an argument — a flag on a
non-destructive op would bypass the per-instance opt-in. That is why `stash_drop` and
`tag_delete` are separate operations rather than `action=drop` / `delete=true`.

`commit --amend` is **not shipped**, for the same reason. Amending rewrites an
existing commit, so if that commit was already pushed, publishing the fix needs a
force push — which makes `amend` a destructive operation wearing an argument's
clothes. If it is wanted later, it belongs as a separate `commit_amend` op declared
`OpDestructive`, not as a bool on `commit`.

### Config-only (`OpConfigOnly`, hidden from the whole MCP surface)

| Key | Purpose |
|---|---|
| `policy_manager` | Renders the per-repo rule editor and the simulator form |
| `policy_simulate` | Evaluates one hypothetical operation and reports the verdict |
| `policy_rule_save` | Replaces the per-repo rule set from the editor |

`dry_run` on mutating operations evaluates the policy and assembles the command
without running it, returning the command that *would* run. Useful for the agent to
verify before acting, and for debugging policy.

### Return shape

Every operation returns the same envelope:

```json
{
  "ok": true,
  "command": "git push origin fix/login-bug",
  "exit_code": 0,
  "stdout": "...",
  "stderr": "",
  "truncated": false,
  "duration_ms": 812,
  "policy": { "evaluated": true, "verdict": "allow", "matched_rule": "global" },
  "remote": { "original": "git@github.com:org/repo.git",
              "effective": "https://github.com/org/repo.git", "converted": true }
}
```

`remote` appears only on network operations. Reporting the effective URL every time
means a push landing on the wrong host is visible immediately rather than a mystery.

---

## 5. Auth, remotes and execution guards

### Credential injection — no disk, three methods

`AuthMethod` **selects exactly one** mechanism — they do not chain. The three exist
because environments differ, not because one falls back to the next.

1. **`askpass` (default)** — `GIT_ASKPASS=<plugin binary>`, token passed to the child
   through env. No new file, token absent from argv (invisible in `ps`), no `-c`
   needed.

   **`GIT_ASKPASS` must be a bare path, with no flag.** Git execs the helper
   directly and cannot carry an argument, so it invokes `<binary> "<prompt>"`. The
   binary therefore detects askpass mode by shape — exactly one argument that does
   not start with `-` — not by a `--askpass` flag. Gating on a flag looks right and
   silently breaks every authenticated operation: the invocation falls through to
   `wickplugin.Serve`, which exits 1 with "This binary is a plugin. These are not
   meant to be executed directly." The shape check is deliberately narrow so no
   current or future `Serve` flag is ever mistaken for a prompt.

   **An unrecognised prompt gets an empty reply, never the token.** Git uses askpass
   for more than credentials (host-key confirmations, among others), and answering
   those with a PAT leaks it. Match only `username`, `password`, and the exact
   phrase `personal access token` — matching the bare word `token` is too broad and
   would hand a GitHub PAT to a smartcard's `PIN for token:` prompt.
2. **`credential_helper`** — `-c credential.helper='!f(){ echo password=$WICK_GIT_TOKEN; };f'`.
   Token stays in env, not argv. Needs `-c`, so it goes through `injectedArgs`.
3. **`extraheader`** — `-c http.extraheader="Authorization: Basic <b64>"`. **The token
   is visible in the process list.** Opt-in only, and the field description says so.

   The token appears **base64-encoded**, which is encoding, not protection:
   `eC1hY2Nlc3MtdG9rZW46Z2hw…` decodes to `x-access-token:ghp_…` in one step. So the
   masking list must contain **both** the raw token and `basicAuthValue(user, token)`.
   Masking only the raw token would leave a fully usable credential in every logged
   command and in `connector_runs`.

The `-c` deny-list in §5.3 applies to **agent-supplied args**, never to the plugin's
own injections. Same reason the `userArgs` / `injectedArgs` split is structural.

Concretely: the runner must call `ValidateUserArgs(cmd.UserArgs)`, **never**
`ValidateUserArgs(cmd.Argv())`. The second form compiles, passes a casual read, and
silently breaks all authentication — `credential_helper` injects `-c`, which the
deny-list rejects. Worth an explicit test rather than a comment.

### Remote handling — override per execution, never rewrite `.git/config`

Repos often carry credentials baked into the remote URL
(`https://olduser:oldtoken@github.com/org/repo.git`). The plugin **ignores** them:

- Read `origin` (or the named remote), strip any `user:pass@`, build a clean URL.
- Pass that URL explicitly to network operations
  (`git push https://github.com/org/repo.git HEAD:refs/heads/fix/x`) with credentials
  from askpass.
- `.git/config` is never modified. The old credentials stay where they are, unused.
  Nothing is written, nothing is cleaned up, no state changes.

There is deliberately **no** operation that rewrites a remote. Writing connector
credentials into `.git/config` would put a token on disk permanently, surviving wick
itself, and would undo every no-disk decision above.

**SSH → HTTPS conversion** (`convert_ssh_remote_to_https`, default true):

```
git@github.com:org/repo.git          → https://github.com/org/repo.git
ssh://git@github.com/org/repo.git    → https://github.com/org/repo.git
ssh://git@github.com:22/org/repo.git → https://github.com/org/repo.git   (ssh port dropped)
git@bitbucket.org:team/repo.git      → https://bitbucket.org/team/repo.git
git@gitlab.com:group/sub/repo.git    → https://gitlab.com/group/sub/repo.git   (nested path preserved)
```

Take the host, take the path, drop `git@`, drop the SSH port, keep the `.git` suffix.
Nested paths (GitLab subgroups) must survive — a naive single `:` split breaks them.

Two cases **cannot** be converted and must fail loudly rather than guess:

1. **SSH host alias from `~/.ssh/config`** — remote `myserver:org/repo.git` where
   `myserver` is a `Host` alias. The real hostname is unknowable without parsing ssh
   config. → reject, naming the alias, and point at `remote_host_map`.
2. **Self-hosted where the HTTPS host differs** — SSH on `git.internal`, HTTPS on
   `code.company.com/git`. Mechanical conversion would produce a wrong-but-plausible
   URL. → requires a `remote_host_map` row.

With `convert_ssh_remote_to_https=false` and an SSH remote, network operations are
rejected with a clear message instead of failing with an obscure auth error.

### Execution guards (hard-coded, not configurable)

Free `repo_path` plus a `raw` operation is, without guards, arbitrary code execution:
`git -c core.pager='sh -c ...' log` runs a shell, `git -c alias.x='!cmd' x` runs a
shell, and `git commit` in a repo with `.git/hooks/pre-commit` runs that repo's
script. So the guards sit on **args and env**:

- **Agent args are rejected** if they contain `-c`, `--config-env`, `--exec-path`,
  `--upload-pack`, `--receive-pack`, `--exec` (push's alias for `--receive-pack`),
  `--interactive`, `--output`, or `ext::`.

  `-u` and `-i` are deliberately **not** banned. Verified against git 2.52: `-u` is
  not a short form of `--upload-pack` anywhere — in `fetch` it is
  `--update-head-ok`, in `push` it is `--set-upstream`, and `ls-remote` has no `-u`.
  `-i` means `--interactive` only in `add`/`rebase`; in `grep` it is
  case-insensitive matching. Banning either blocked idiomatic git without closing
  anything, since the dangerous long forms are banned on their own.
- **Env is an explicit allowlist** (`PATH`, `HOME`, askpass vars,
  `GIT_TERMINAL_PROMPT=0`), not a full inherit. `GIT_CONFIG_*`, `GIT_SSH_COMMAND`,
  `GIT_PROXY_COMMAND`, `GIT_EXTERNAL_DIFF` are never forwarded from any source.
  On Windows the list must also carry `HOMEDRIVE`/`HOMEPATH` (git resolves `~` from
  them when `HOME` is unset), `LOCALAPPDATA`/`APPDATA`/`ProgramData` (Git for
  Windows keeps system config and the CA bundle there), and `COMSPEC`/`PATHEXT`.
  A missing name is an empty value, not a fallback — omissions surface as obscure
  auth or TLS failures.
- **Every process spawn goes through `pkg/safeexec`**, never `os/exec` directly:
  `safeexec.Command` / `safeexec.LookPath`. Go's own `LookPath` calls
  `faccessat2(2)`, which Android/Termux seccomp kills with SIGSYS on kernels before
  5.8, and `safeexec` also carries the Windows `.bat`/`.cmd` quoting fix. Anything
  setting `SysProcAttr` must **add** fields (`if nil { new }`, then assign) rather
  than replace the struct, or it silently drops the `CmdLine` safeexec set.
- **`core.hooksPath` is forced to an empty directory** for hook-running operations
  (`commit`, `merge`, `push`, `checkout`, `rebase`) unless `allow_hooks` is true
  (default false).
- **`GIT_TERMINAL_PROMPT=0`** plus `GIT_ASKPASS` always set, so git never blocks on an
  interactive prompt and never falls back to the machine's credential manager.
- **`repo_path` must contain `.git`** and must not be a bare `$HOME`, so operations
  cannot run in an arbitrary directory by accident.
- **`--interactive` is rejected** on every operation — an editor-opening git process
  would hang until the timeout. The typed `rebase` op never passes it, and in raw
  mode `rebase` must additionally be allow-listed.
- **Every user value placed in a positional slot is preceded by `--end-of-options`.**
  This is not optional polish. `ValidateUserArgs` is a deny-list of known-dangerous
  *flags*; it cannot defend a positional slot, because it has no way to know which
  arguments are meant to be data. Without the terminator, a `ref` or `pattern` input
  simply becomes a flag:

  | Input | Becomes | Effect |
  |---|---|---|
  | `pattern: --edit-description` on `branch --list` | a real flag | opens an editor **and writes to config** — a mutation from a "read-only" op |
  | `ref_a: --cached` on `diff` | a real flag | silently diffs the index instead of the working tree |
  | `ref: --all` on `show` | a real flag | dumps the entire object graph |
  | `ref: -n 99999` on `log` | real flags | ignores the `limit` the op advertises |
  | `ref: --hard` on a **soft** `reset` | a real flag | **destroys the working tree, exit code 0** |

  That last row is the one that matters most. Verified against git 2.52: `git reset
  --soft --hard HEAD~1` prints `HEAD is now at …`, exits **0**, and deletes
  uncommitted work — the later flag simply wins, with no warning. Since `mode:
  "soft"` does not require `allow_force_push`, an unguarded `reset` let a crafted
  `ref` destroy a working tree while the policy returned `allow`. With the
  terminator git refuses: `fatal: option '--hard' must come before non-option
  arguments`.

  Verified against git 2.52: with `--end-of-options`, `branch --list
  --end-of-options --edit-description` is treated as a literal glob, and `diff
  --end-of-options --cached` is refused outright.

  Two input shapes are already safe and need no terminator: values emitted as
  `--flag=<value>` (cannot split into a new token — `--since=--all` parses as a
  nonsense date, not a flag), and paths placed after `--` (git's pathspec
  terminator). Numeric inputs go through `c.InputInt`, so a non-numeric value
  becomes `0` and falls back to the default.

  **Two subcommands reject the terminator** and need different handling — do not
  "fix" these back:

  - `checkout -b` consumes `--end-of-options` *as the branch name*. The terminator
    goes at the end of argv instead; `-b` binds its own value anyway, so
    `checkout -b --orphan` already fails with "not a valid branch name".
  - `tag -a NAME -m MSG` with a terminator is `fatal: too many arguments`. Since
    `-m` alone already implies an annotated tag, `-a` is dropped and the order
    becomes `tag -m MSG --end-of-options NAME`.

  Also needing no terminator because the flag binds its value: `commit -m`,
  `stash push -m`, `clone --branch`, and `add` (paths after `--`).

### What is not contained — stated plainly

Four things are deliberately not sealed. Each was reviewed and kept; they are decisions
with reasons, not gaps, and knowing them is part of configuring the connector.

**`raw` cannot be fully contained.** It receives no `--end-of-options` because
passing flags through is its entire purpose. Its defences are real — off by default,
per-subcommand allow list where unlisted means denied, `RawSubcommandOf` failing
closed, the flag deny-list, the env allowlist, hook suppression, destructive opt-in
— but a deny-list is a blocklist, not a proof. An admin who allow-lists a
subcommand that takes a command by design (`git bisect run <cmd>` is the clearest
example) has granted code execution. `raw` is an admin escape hatch: correctly
gated, not sandboxed.

**An unlisted repository gets the global fallback, which may be permissive.** A
repository matching no `repo_policies` row is judged by layer 1 alone, and if layer 1 is
empty that means no branch pattern, no commit pattern and nothing protected.

This is the owner's call, and the reason is practical: a connector may manage dozens of
clones and only a few of them need rules. Requiring a row per repository would mean
either an unmaintainable list or a connector that refuses most of its own work. Fail-open
here is a scoping decision, not an accident — the operator decides which repositories are
worth constraining. What is NOT acceptable is failing open **silently**, which is why a
scope that could not be resolved is reported (`policy.unresolved_scope`) and a remote
that does not exist is an error rather than a fallback.

**Paths are unrestricted unless `allowed_repo_roots` is set.** With it empty — the
default — `repo_path` and `clone`'s `dest` accept any location the wick process can
reach, so the connector can read and write any git repository on the machine using the
connector's credential.

That default exists because the connector's purpose is managing checkouts that already
exist, wherever they are; a mandatory sandbox would put every one of them out of reach.
Set `allowed_repo_roots` to narrow it per instance. The check resolves symlinks and `..`
before comparing, so neither traversal nor a symlinked parent escapes a root — a prefix
test would have passed both.

### Runtime limits

- Timeout: `timeout_seconds` (default 60), `network_timeout_seconds` (default 180) for
  push/pull/fetch/clone/ls-remote. On expiry the **process group** is killed, not just
  the parent — otherwise child git processes are orphaned.
- Output cap: `max_output_bytes` (default 256 KiB). Exceeding it truncates and sets
  `truncated: true` — never a silent trim.
- Read operations take their own `limit` / `max_lines` so the agent can ask for less
  rather than hit the cap.
- Secrets are masked in every logged command and in `stdout` / `stderr`.

---

## 6. Policy Simulator widget

`PolicyManager` renders through `html=policy_manager`, backed by
`OpConfigOnly("policy_manager", ...)` plus `policy_simulate` and
`policy_rule_save` for its two buttons. Because the widget forwards its own
`<input name=...>` values to the op, the simulator carries a small form.

```
┌─ Policy Simulator ─────────────────────────────────────────┐
│  Repo   [ github.com/org/infra          ]                  │
│  Op     [ push          ▾ ]                                │
│  Branch [ fix/login-bug                 ]                  │
│                                  [ Simulate ]              │
├────────────────────────────────────────────────────────────┤
│  ❌  DENIED                                                 │
│                                                             │
│  Matched rule   per-repo → */org/infra                     │
│  Reason         branch "fix/login-bug" does not match      │
│                 pattern ^ops/.+$                            │
│  Would run      git push origin fix/login-bug              │
│                 (not executed)                              │
├────────────────────────────────────────────────────────────┤
│  Effective rules for this repo:                            │
│    branch pattern   ^ops/.+$                 ← per-repo    │
│    protected        master, main, release/*  ← per-repo    │
│    force push       denied                   ← global      │
└────────────────────────────────────────────────────────────┘
```

Allowed case:

```
✅  ALLOWED
Matched rule   global (no override for this repo)
Would run      git push origin fix/login-bug
Effective      branch pattern ^(fix|feat|chore)/…  ← global
               protected      master, main         ← global
```

The simulator calls the **same evaluator** the real operations use — not a parallel
implementation that could drift. If the simulator says ALLOWED, the real operation is
ALLOWED.

Markup uses inline `style` with theme CSS variables (`var(--color-navy-*)`,
`var(--color-white-*)`), **not** Tailwind utility classes: the manager's Tailwind
build does not scan HTML returned by a connector at runtime, so unused utility
classes are purged and the widget renders unstyled or theme-broken.

---

## 7. Testing

Pure-Go units, no network, no fixtures beyond temp repos created by `git init`.

**`policy_test.go`** — table-driven:
- branch pattern accept/reject, including patterns that fail to compile (→ deny)
- protected branch glob matching (`release/*` vs `release`)
- per-repo specificity: `*/org/sandbox` beats `*/org/*`
- tie-break goes to the stricter row, and the result is independent of row order
- `""` inherits, `"-"` clears
- `raw` with `raw_enabled=false`, empty rules, unlisted subcommand → all denied
- every verdict carries a non-empty `matched_rule`

**`remote_test.go`** — the conversion table from §5 plus both must-fail cases (ssh
alias, unmapped self-hosted host), and credential stripping from
`https://user:pass@host/…`.

**`git_test.go`** — arg deny-list rejects each banned flag; `injectedArgs` are *not*
filtered; env allowlist drops `GIT_CONFIG_*` and friends; truncation sets the flag;
timeout kills the process group.

Integration checks against a temp repo (`git init`, a commit, a fake remote) cover
`status` / `log` / `branch_create` / `commit` end-to-end. Nothing touches a real
remote in tests.

---

## 7a. Why `AllowSessionConfig` stays off

`Module.AllowSessionConfig` is deliberately **not** set, and this is a security
decision rather than an oversight.

Session config cloning is all-or-nothing per module — there is no per-field
allowlist. For this connector the config *is* the security policy:
`allow_force_push`, `protected_branches`, `raw_enabled`, `raw_rules`,
`repo_policies`, `allow_hooks`. `policyFor` consults nothing else, so there is no
admin-pinned layer underneath to fall back on. Enabling session config would let
anyone with session-config access mint an instance with force push allowed, raw
enabled and the rule list empty — which defeats the whole policy engine.

The genuine use case it would serve is per-session commit identity (attributing
commits to the requesting human rather than one shared bot). That is better solved
with optional `author_name` / `author_email` **operation inputs** on `commit`,
overriding the config default through `injectedArgs`. If session config is ever
wanted, policy config must first be split away from identity config.

## 8. Deferred

Recorded so the reasoning is not relitigated later:

- **SSH key auth.** The only in-memory path is ssh-agent (`ssh-add -` reads a key from
  stdin, verified working with OpenSSH 10.2 in Git Bash), but that agent places its
  socket under `~/.ssh/agent/`, which we do not want. `ssh -i` requires a key file.
  There is no env or argv path for key material. Revisit only if a keyless mechanism
  appears.
- **Remote execution** (SSH to another host, run git there) — a whole extra dimension.
- **Policy DSL.** The kvlist layers already express everything the DSL would; adding a
  third precedence layer costs explanation and tests for no new capability. Add it
  when a rule genuinely cannot be expressed (time-based, per-user), not before.
- **Writing credentials into `.git/config`** — permanent token on disk, outliving
  wick. Deliberately absent.
