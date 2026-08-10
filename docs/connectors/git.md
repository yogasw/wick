---
outline: deep
---

# Git CLI

`git` runs the **local `git` binary** against repositories already on disk. Because it wraps the CLI rather than a hosting API, GitHub, Bitbucket, GitLab and self-hosted servers all work through the same operations — there is nothing host-specific to configure beyond a credential.

It does **not** replace or overlap the [GitHub](./github) and [Bitbucket](./bitbucket) connectors. Those wrap REST APIs and cover what `git` fundamentally cannot: pull requests, issues, reviews, releases. This one covers what the API cannot: the working tree, staging, local branches, commits, and pushes from a checkout on the machine. Use both side by side; nothing was merged.

One instance = one git identity: one author name/email, one HTTPS credential, and one policy set.

| | |
|---|---|
| **Source** | [`plugins/connector/git/`](https://github.com/yogasw/wick/tree/master/plugins/connector/git) |
| **Key** | `git` |
| **Icon** | 🌿 |
| **Tier** | plugin — install with `<app> plugin install git` |
| **Session config** | not supported (see [Why session config is off](#why-session-config-is-off)) |

> This connector ships as a plugin, not compiled into the wick binary:
>
> ```bash
> <app> plugin install git
> ```
>
> See [Connector Plugins](/guide/connector-plugins) for the full install flow.

## Prerequisite

**`git` must be on the PATH of the machine running wick.** Not the machine the agent or the browser is on — the connector spawns a local process, so the binary has to exist where wick itself runs.

If it is missing, every operation fails with:

```
git not found in PATH on the machine running wick
```

That message is deliberate. A missing binary should never surface as a vague exec error.

## Setup

### Identity

| Field | Notes |
|---|---|
| `author_name` | Name recorded on commits made through this connector, e.g. `Deploy Bot`. |
| `author_email` | Email recorded on commits, e.g. `bot@example.com`. |

Both are injected per execution as `-c user.name=…` / `-c user.email=…`. The repository's own config is not touched.

### HTTPS credential

| Field | Notes |
|---|---|
| `username` | HTTPS username. For a GitHub personal access token, use `x-access-token`. |
| `token` | Secret. Personal access token or app password. Never written to disk. |
| `auth_method` | `askpass` (default) \| `credential_helper` \| `extraheader`. |

An empty token is valid — a public repository, or an operation that never touches the network, needs no credential.

### Why `askpass` is the default

`askpass` is the only method where the credential exists in **neither** a file **nor** the process list.

- **`askpass` (default)** — `GIT_ASKPASS` points at the connector binary itself, and the token travels to the child process in an environment variable. No new file is created, and nothing sensitive appears in `argv`, so `ps` shows a clean command line. The binary has two roles: gRPC plugin, and askpass helper when git execs it with a single prompt argument.

  Two details make this safe rather than merely convenient. Git cannot pass a flag to the askpass helper, so the binary detects askpass mode **by invocation shape** (exactly one argument that is not a flag), not by a `--askpass` flag it would never receive. And an unrecognised prompt gets an **empty** reply, never the token — git uses askpass for more than passwords (host-key confirmations, smartcard `PIN for token:` prompts), so only `username`, `password` and the exact phrase `personal access token` are answered.

- **`credential_helper`** — injects `-c credential.helper=…` which reads the token from the environment. The token stays out of `argv`, but the method needs a `-c`, so it is slightly more machinery than askpass for the same result. Use it where askpass misbehaves.

- **`extraheader`** — a deliberate downgrade. It injects `-c http.extraheader="Authorization: Basic <base64>"`, which puts **the credential in the process list**: anyone who can run `ps` on that machine can read it. The value is base64, and base64 is *encoding, not protection* — `eC1hY2Nlc3MtdG9rZW46Z2hw…` decodes to `x-access-token:ghp_…` in a single step. Pick it only when a proxy or server genuinely requires the header, and understand that on a shared host it is equivalent to handing out the token.

  Because of that, the masking list contains **both** the raw token and the base64 Basic value, so neither form leaks into logged commands or run history.

### Remotes

| Field | Notes |
|---|---|
| `convert_ssh_remote_to_https` | Default `true`. Rewrites SSH remotes to HTTPS **for the duration of one execution**. `.git/config` is never modified. |
| `remote_host_map` | `kvlist` of `ssh_host` → `https_host`. Needed only for self-hosted servers where the two hosts differ. |

Conversion is mechanical — take the host, take the path, drop `git@`, drop the SSH port, keep the `.git` suffix. Nested paths (GitLab subgroups) survive:

```
git@github.com:org/repo.git           → https://github.com/org/repo.git
ssh://git@github.com:22/org/repo.git  → https://github.com/org/repo.git
git@gitlab.com:group/sub/repo.git     → https://gitlab.com/group/sub/repo.git
```

Two cases **cannot** be converted and fail loudly instead of guessing:

1. **An SSH host alias from `~/.ssh/config`** — a remote like `myserver:org/repo.git`. The real hostname is unknowable without parsing ssh config, so the error names the alias and points at `remote_host_map`.
2. **Self-hosted where the HTTPS host differs** — SSH on `git.internal`, HTTPS on `code.company.com/git`. A mechanical guess would produce a wrong-but-plausible URL, so a `remote_host_map` row is required.

With `convert_ssh_remote_to_https = false` and an SSH remote, network operations are rejected with a clear message rather than failing later with an obscure auth error.

### Runtime

| Field | Default | Notes |
|---|---|---|
| `allow_hooks` | `false` | Whether `.git/hooks` scripts may run. A hook is arbitrary code committed to the repository. |
| `timeout_seconds` | `60` | Timeout for non-network operations. |
| `network_timeout_seconds` | `180` | Timeout for `push`, `pull`, `fetch`, `clone`, `ls_remote`. |
| `max_output_bytes` | `262144` | Output cap (256 KiB). Beyond it the result is truncated and `truncated: true` is set. |

## Policy model

Every mutating operation is evaluated **before any process is spawned**. A denied operation costs nothing and changes nothing — there is no partially-applied state to clean up.

There are two layers, and both compile into a single effective policy before evaluation. No operation reads raw config.

```
layer 1  global fields      (branch_name_pattern, protected_branches, allow_force_push,
                             raw_enabled, raw_rules)                          → base
layer 2  repo_policies      per-repo rows; win over layer 1, matching repos only
```

### The rules that make this unambiguous

1. **Most specific wins, measured by wildcard count.** `*/org/sandbox` (1 wildcard) beats `*/org/*` (2), so **row ordering in the JSON is irrelevant**. Ties go to the stricter row; a tie strictness cannot break falls back to lexicographic order on the `repo` glob, so a verdict never depends on how the rows happen to be sorted.
2. **Deny beats allow, across layers.** A per-repo row may add restrictions or replace a pattern. It cannot silently un-protect a branch.
3. **An empty column means *not specified* → inherit the global value.** It does not mean "allow anything". Fail closed.
4. **`"-"` explicitly clears the inherited value.** This is the only way to say "this repo has no protected branches". Without it, a permissive-looking per-repo row would quietly inherit the global list.

   `"-"` is meaningful on the **list and pattern** columns (`protected`, `branch_pattern`), where "no value" is a real state. On the boolean `force_push` there is no third state to clear to — allow and deny are the only options — so **`"-"` on `force_push` means the same as empty: inherit.**
5. **`raw` denies everything unless both `raw_enabled` is on and the subcommand is explicitly allow-listed.** An unlisted subcommand is denied.
6. **Malformed `repo_policies` JSON blocks mutations, not reads.** The parse error is reported in the verdict, so you can still inspect the repository while fixing the config.
7. **Every verdict carries `matched_rule`** — which layer, which row — into the operation response, so "why was my push blocked" is answerable without reading code.

### Matching semantics

Two different languages appear in one row. This is the most common source of mistakes:

| Column | Language | Matched against |
|---|---|---|
| `repo` | **glob** (`*`) | both the local path **and** `host/owner/repo` |
| `branch_pattern` | **regex** (Go RE2) | branch name |
| `protected` | comma-separated **globs** | branch name (case-insensitive) |
| `force_push` | `true` / `false` / empty or `-` (both inherit) | — |

Protected-branch matching is case-insensitive on purpose: git branch names are case-sensitive on Linux but not on Windows or macOS checkouts, and treating `Master` as unprotected would be a trivial bypass.

## Policy Cookbook

Literal, paste-ready values. This section doubles as the contract for asking an AI to generate a policy later.

### Shape contract

```
protected_branches : [{"branch": "<name or glob>"}]
repo_policies      : [{"repo":"<glob>", "branch_pattern":"<regex or empty>",
                       "protected":"<csv globs, or - to clear>", "force_push":"true|false|empty (inherit)"}]
raw_rules          : [{"subcommand":"<git subcommand>", "mode":"allow|deny"}]
remote_host_map    : [{"ssh_host":"<host>", "https_host":"<host[/path]>"}]
```

Non-negotiable facts a generator must know:

- `branch_pattern` is a **regex**; `repo` and `protected` are **globs**. Different languages on the same row.
- Empty column = inherit. `-` = clear the inheritance (but on `force_push`, `-` still means inherit).
- An unlisted `raw` subcommand = denied.
- Specificity by wildcard count; ties → stricter.

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

`push origin fix/login` ✅ · `push origin master` ❌ protected · `checkout -b hotfix/x` ✅ · `checkout -b temp` ❌ pattern.

### Scenario 2 — infra repo stricter than global

```json
// repo_policies
[
  {"repo":"*/org/infra","branch_pattern":"^ops/[a-z0-9-]+$","protected":"master,main,release/*","force_push":"false"}
]
```

Elsewhere `fix/x` still passes on the global pattern. In `org/infra` only `ops/*` passes, and `release/2024-01` is closed too.

### Scenario 3 — free sandbox, strict everywhere else

```json
// repo_policies
[
  {"repo":"*/org/sandbox","branch_pattern":".*","protected":"-","force_push":"true"},
  {"repo":"*/org/*","branch_pattern":"^(fix|feat)/.+$","protected":"master,main","force_push":"false"}
]
```

`*/org/sandbox` (1 wildcard) beats `*/org/*` (2), so ordering is irrelevant. Note `"protected":"-"` — with `""` the sandbox row would inherit the global protected list and the "free sandbox" would not actually be free.

### Scenario 4 — raw opened for read-only subcommands

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

The `deny` rows are redundant — unlisted is already denied — but written on purpose so the intent reads clearly to whoever opens this config next.

> `bisect` is allow-listed here as an illustration of the shape, not as a safe default. `git bisect run <cmd>` takes a command by design, so allowing it grants code execution. See [What is not contained](#what-is-not-contained).

### Scenario 5 — self-hosted, SSH host differs from HTTPS

```json
// remote_host_map
[
  {"ssh_host":"git.internal","https_host":"code.company.com/git"},
  {"ssh_host":"ssh.abc.net","https_host":"abc.net"}
]
```

`git@git.internal:team/api.git` → network operations use `https://code.company.com/git/team/api.git`, and the response reports `converted: true` with both the original and the effective URL.

## Policy Rules widget

`repo_policies` is not meant to be hand-edited as JSON. The **Policy Rules** card on the connector's config page renders an editor plus a simulator.

**The editor** validates before saving, so a broken rule is rejected at edit time instead of at push time: `branch_pattern` must compile as a Go regex, `force_push` must be `true` / `false` / empty / `-`, and `repo` must be present.

**The simulator** takes a repo, an op and a branch, and reports the verdict without running anything:

```
❌  DENIED
Matched rule   per-repo → */org/infra
Reason         branch "fix/login-bug" does not match pattern ^ops/.+$
Would run      git push origin fix/login-bug   (not executed)

Effective rules for this repo:
  branch pattern   ^ops/.+$                 ← per-repo
  protected        master, main, release/*   ← per-repo
  force push       denied                    ← global
```

It also shows which layer each effective value came from, which is usually the fastest way to spot an unintended inherit.

**The simulator calls the same evaluator the real operations use** — not a parallel implementation that could drift. If the simulator says ALLOWED, the real operation is ALLOWED.

The widget is backed by three config-only operations (`policy_manager`, `policy_simulate`, `policy_rule_save`) which exist to serve the form and are **not** exposed to agents.

## Operations

26 operations in 5 categories. Destructive operations are marked ⚠️ and are **off by default on every new instance** — an admin has to enable each one per instance before an agent can call it.

Destructiveness is a property of the **operation**, not of an argument. That is why `stash_drop` and `tag_delete` are separate operations rather than `action=drop` / `delete=true` on `stash` / `tag`: a flag on a non-destructive op would bypass the per-instance opt-in entirely.

### Read

Nothing in this category changes repository state, so branch and force rules do not apply.

| Op | Input | Policy gates | What it does |
|---|---|---|---|
| `status` | `repo_path`\* | none | Working tree state in porcelain v2 format: staged, unstaged, untracked, plus the current branch. |
| `log` | `repo_path`\*, `ref`, `limit`, `path`, `since` | none | Commit history, one line per commit (hash, author, ISO date, subject). `limit` defaults to 20. |
| `diff` | `repo_path`\*, `ref_a`, `ref_b`, `path`, `stat_only`, `max_lines` | none | Compare `ref_a` against `ref_b`, or against the working tree when `ref_b` is empty. `stat_only` returns just the file summary, which is far smaller. `max_lines` defaults to 500. |
| `branch_list` | `repo_path`\*, `remote`, `pattern` | none | Branches with short commit and last commit date. `remote` lists remote-tracking branches instead. |
| `show` | `repo_path`\*, `ref`\* | none | One commit with its changed-file summary. |
| `remote_list` | `repo_path`\* | none | Every remote with its configured URL (credentials stripped) **and** the URL network operations would actually use, so an SSH→HTTPS conversion is visible before anything is pushed. |
| `ls_remote` | `repo_path`\*, `remote` | none | Branches a remote advertises, without fetching. The cheapest way to verify that the credential and the remote URL both work. Network. |

### Branches and Commits

| Op | Input | Policy gates |
|---|---|---|
| `branch_create` | `repo_path`\*, `name`\*, `from_ref`, `checkout` | `name` must match `branch_name_pattern` and must not be protected. |
| `checkout` | `repo_path`\*, `ref`\*, `create` | `ref` must not be protected. With `create`, the branch pattern also applies. |
| `add` | `repo_path`\*, `paths`\* | Current branch must not be protected. |
| `commit` | `repo_path`\*, `message`\*, `all`, `dry_run` | Current branch must not be protected. |
| `stash` | `repo_path`\*, `action`\* (`push` \| `pop` \| `list`), `message` | Current branch must not be protected. Dropping is **not** here — see `stash_drop`. |
| `tag` | `repo_path`\*, `name`\*, `ref`, `message` | Current branch must not be protected. Create/annotate only; deletion is `tag_delete`. |

### Network

| Op | Input | Policy gates |
|---|---|---|
| `fetch` | `repo_path`\*, `remote`, `prune` | Current branch must not be protected. |
| `pull` | `repo_path`\*, `remote`, `branch`, `rebase` | Current branch must not be protected. |

### Destructive — off by default per instance

| Op | Input | Policy gates |
|---|---|---|
| ⚠️ `push` | `repo_path`\*, `remote`, `branch`, `force`, `set_upstream`, `dry_run` | Target branch must not be protected. `force` requires `allow_force_push`. Force always means `--force-with-lease`, never a bare `--force`. |
| ⚠️ `merge` | `repo_path`\*, `ref`, `no_ff`, `abort` | Current branch must not be protected. |
| ⚠️ `reset` | `repo_path`\*, `mode`\* (`soft` \| `mixed` \| `hard`), `ref`\* | Current branch must not be protected. `mode=hard` additionally requires `allow_force_push`, because it discards committed and uncommitted work. |
| ⚠️ `rebase` | `repo_path`\*, `onto`, `abort`, `continue_` | Current branch must not be protected. Never interactive — `--interactive` is rejected everywhere, since an editor-opening git process would hang until the timeout. |
| ⚠️ `clone` | `url`\*, `dest`\*, `branch`, `depth` | Destructive opt-in plus the malformed-config guard. `url` must resolve through the host map when it is an SSH URL, and `dest` must not already exist. Network. |
| ⚠️ `stash_drop` | `repo_path`\*, `ref` | Current branch must not be protected. A dropped stash is unrecoverable. |
| ⚠️ `tag_delete` | `repo_path`\*, `name`\*, `remote` | Destructive opt-in. Supplying `remote` also deletes the tag on the remote, which is a network mutation. |
| ⚠️ `raw` | `repo_path`\*, `args`\*, `dry_run` | `raw_enabled` **and** an explicit `allow` row in `raw_rules` for the subcommand. Unlisted → denied. The flag deny-list still applies. |

`dry_run` on `commit`, `push` and `raw` evaluates the policy and assembles the command **without running it**, returning the command that *would* run. Useful for an agent to verify before acting, and for debugging a policy.

### Configuration

Three config-only operations (`policy_manager`, `policy_simulate`, `policy_rule_save`) back the [Policy Rules widget](#policy-rules-widget). They are **hidden from the entire MCP surface** — an agent cannot list or call them, and they are not agent tools. They are documented here only so the count in the manager UI is not a surprise.

### Return shape

Every operation returns the same envelope:

```json
{
  "ok": true,
  "command": "git push https://github.com/org/repo.git HEAD:refs/heads/fix/login-bug",
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

A non-zero git exit is reported in `exit_code` / `stderr`, not raised as an error — the agent needs stderr to react. `remote` appears only on network operations. Reporting the effective URL every time means a push landing on the wrong host is visible immediately instead of being a mystery.

## Safety model

A free-form `repo_path` plus a `raw` operation is, without guards, arbitrary code execution: `git -c core.pager='sh -c …' log` runs a shell, `git -c alias.x='!cmd' x` runs a shell, and `git commit` in a repository with a `.git/hooks/pre-commit` runs that repository's script. So the guards sit on **arguments and environment**, not on paths.

### What is guarded

**`--end-of-options` before every user value in a positional slot.** This is not polish. The flag deny-list is a list of known-dangerous *flags*; it cannot defend a positional slot, because it has no way to know which arguments are meant to be data. Without the terminator, an input simply becomes a flag:

| Input | Becomes | Effect |
|---|---|---|
| `pattern: --edit-description` on `branch_list` | a real flag | opens an editor **and writes to config** — a mutation from a read-only op |
| `ref_a: --cached` on `diff` | a real flag | silently diffs the index instead of the working tree |
| `ref: --all` on `show` | a real flag | dumps the entire object graph |
| `ref: --hard` on a **soft** `reset` | a real flag | **destroys the working tree, exit code 0** |

That last row is the motivating case. Verified against git 2.52: `git reset --soft --hard HEAD~1` prints `HEAD is now at …`, exits **0**, and deletes uncommitted work — the later flag simply wins, with no warning. Since `mode: "soft"` does not require `allow_force_push`, an unguarded `reset` would let a crafted `ref` destroy a working tree while the policy correctly returned `allow`. With the terminator, git refuses: `fatal: option '--hard' must come before non-option arguments`.

Two input shapes need no terminator because they are already safe: values emitted as `--flag=<value>` (they cannot split into a new token) and paths placed after `--` (git's pathspec terminator). Two subcommands reject the terminator in the usual position and are handled differently — `checkout -b` would consume it as the branch name, and `tag -a NAME -m MSG` errors with `too many arguments` — so their argv order differs on purpose.

**Flag deny-list on agent-supplied arguments.** Rejected: `-c`, `--config-env`, `--exec-path`, `--upload-pack`, `--receive-pack`, `--exec`, `--interactive`, `--output`, and anything containing `ext::`. `-u` and `-i` are deliberately **not** banned — verified against git 2.52, `-u` is never a short form of `--upload-pack` (it is `--update-head-ok` in `fetch`, `--set-upstream` in `push`), and `-i` is case-insensitive matching in `grep`. Banning either blocked idiomatic git while closing nothing, since the dangerous long forms are banned on their own.

The deny-list applies to **agent-supplied arguments only**, never to the connector's own injections. That split is structural, not a convention: the connector needs `-c` for hook suppression and identity, so filtering the combined argv would break its own authentication.

**Environment is an explicit allowlist, not an inherit.** Only what git genuinely needs is carried over — `PATH`, `HOME`/`USERPROFILE`, `HOMEDRIVE`/`HOMEPATH`, `SystemRoot`/`SystemDrive`/`windir`, `COMSPEC`/`PATHEXT`, `ProgramFiles`/`ProgramData`/`LOCALAPPDATA`/`APPDATA`, temp dir and locale — plus `GIT_TERMINAL_PROMPT=0`. `GIT_CONFIG_*`, `GIT_SSH_COMMAND`, `GIT_PROXY_COMMAND` and `GIT_EXTERNAL_DIFF` are never forwarded from any source, because each turns git into an arbitrary-command runner.

**Hooks are suppressed by default.** For the operations git would run a hook for (`commit`, `merge`, `push`, `checkout`, `rebase`), `core.hooksPath` is forced to an empty directory unless `allow_hooks` is on. A hook is arbitrary code committed to the repository by someone else.

**No interactive prompts, ever.** `GIT_TERMINAL_PROMPT=0` plus an always-set `GIT_ASKPASS` means git never blocks waiting for a human and never falls back to the machine's credential manager.

**Timeouts kill the process group.** On expiry the whole process group is killed (`taskkill /T` on Windows, the POSIX group elsewhere), not just the parent — otherwise child git processes such as `git-remote-https` are orphaned and keep running.

**Output is capped, never silently trimmed.** Beyond `max_output_bytes` the result is truncated and `truncated: true` is set. Read operations take their own `limit` / `max_lines` so an agent can ask for less rather than hit the cap.

**Secrets are masked** in every logged command and in `stdout` / `stderr` — both the raw token and its base64 Basic encoding.

**`repo_path` is validated.** It must exist, be a directory, contain a `.git` entry, and must not be the home directory itself.

**`.git/config` is never modified.** Credentials baked into a remote URL — `https://olduser:oldtoken@github.com/org/repo.git`, which is common in older checkouts — are **ignored, not consumed**. The connector reads the remote, strips any `user:pass@`, and passes the clean URL explicitly to the network operation (`git push https://github.com/org/repo.git HEAD:refs/heads/fix/x`) with its own credential from askpass. The old credential stays where it is, unused. Nothing is written, nothing is cleaned up. Stripped URLs are also run through credential removal before they enter a response, so a token from someone else's `.git/config` never lands in a network result.

### What is not contained

Two surfaces are gated but **not sealed**. Both follow from the deliberate decision to have no path allowlist — the point of the connector is to manage repositories already cloned on the machine — and both are worth knowing before you enable them.

**`raw` is an admin escape hatch, not a sandbox.** It receives no `--end-of-options`, because passing flags through is its entire purpose. Its defences are real: off by default, per-subcommand allow list where unlisted means denied, subcommand detection that fails closed on an unrecognised leading flag, the flag deny-list, the env allowlist, hook suppression, and the destructive opt-in. But **a deny-list is a blocklist, not a proof.** An admin who allow-lists a subcommand that takes a command by design — `git bisect run <cmd>` is the clearest example — has granted code execution on the machine running wick. Treat `raw_rules` as a decision about who may run code, not as a convenience list.

**`clone` can write anywhere the wick process can.** `dest` is an unrestricted absolute path, bounded only by refusing an existing directory and by the destructive opt-in. This follows directly from having no path allowlist; it is not an oversight. If that matters in your deployment, the containment belongs at the OS level — the user the wick process runs as — not in a config field.

### Why session config is off

`AllowSessionConfig` is deliberately **not** set, and this is a security decision rather than an oversight.

Session config cloning is all-or-nothing per module — there is no per-field allowlist. For this connector the config **is** the security policy: `allow_force_push`, `protected_branches`, `raw_enabled`, `raw_rules`, `repo_policies`, `allow_hooks`. Policy resolution consults nothing else, so there is no admin-pinned layer underneath to fall back on. Enabling session config would let anyone with session-config access mint an instance with force push allowed, raw enabled and an empty rule list — which defeats the whole policy engine.

The genuine use case it would serve is per-session commit identity, attributing commits to the requesting human rather than one shared bot. That is better solved with optional `author_name` / `author_email` **operation inputs** on `commit`. If session config is ever wanted here, policy config must first be split away from identity config.

## What it deliberately does not do

Recorded with reasons so nobody "adds" one of these later without the context.

- **No SSH key authentication.** The only way to hold a key without writing it to disk is ssh-agent (`ssh-add -` reads a key from stdin, verified working with OpenSSH 10.2), but that agent places its socket under `~/.ssh/agent/` — a file on disk, in the user's home directory, outliving the operation. `ssh -i` requires a key file outright. There is no env or argv path for key material, so every available mechanism breaks the no-disk rule that the whole credential design rests on. Revisit only if a genuinely keyless mechanism appears. HTTPS with a token is the supported path.
- **No remote execution.** No SSH-to-another-host-and-run-git-there. That is a whole extra dimension: a second credential type, a second failure surface, and policy that would have to be evaluated somewhere it cannot be enforced. Out of scope by choice, not by omission.
- **No policy DSL.** The two kvlist layers already express everything a DSL would, and adding a third precedence layer costs explanation and tests for no new capability. Add one when a rule genuinely cannot be expressed — time-based, per-user — and not before.
- **No writing credentials into `.git/config`.** There is deliberately no operation that rewrites a remote. Doing so would put a token on disk permanently, surviving wick itself, and would undo every no-disk decision in the auth design. Per-execution URL override achieves the same result with no residue.

## See also

- [GitHub](./github) — REST API side: pull requests, issues, reviews, releases. Complements this connector.
- [Bitbucket](./bitbucket) — REST API side for Bitbucket: pull requests, comments, diffs.
- [Connector Module](/guide/connector-module) — module contract, `wick:"..."` tag grammar.
- [Connector Plugins](/guide/connector-plugins) — install / enable / disable flow.
