---
outline: deep
---

# CLI Reference

Wick ships two kinds of commands:

- **Built-in commands** are hardcoded in the `wick` binary (`init`, `run`, `build`, `server`, `worker`, `all`, `skill`, `doctor`, `mcp`, `workflow`, `upgrade`, `version`). They work the same across every project and the behavior is fixed by the installed wick version.
- **Task shortcuts** (`dev`, `setup`, `test`, `tidy`, `generate`) are thin wrappers that execute the matching task in your project's [`wick.yml`](./wick-yml). You can edit or extend those tasks per project; `wick run <task>` runs any arbitrary task defined there.

Apps built by `wick build` also ship their own subcommand tree (`tray`, `server`, `worker`, `all`, `start`, `stop`, `status`, `service`, `config`, `mcp`, `uninstall`) — see the dedicated [App CLI Reference](./app-cli).

Run `wick --help` to print the current list.

---

## Built-in commands

### `wick init [name]`

Scaffold a new wick project in `./<name>` (default: `myapp`).

```bash
wick init my-app
wick init my-app --skip-setup   # skip "go mod tidy" and "wick setup"
```

Copies the bundled template — tools, jobs, connectors, tags, `wick.yml`, `AGENTS.md`, example tool, example job, and the example [`crudcrud` connector](../guide/connector-module) — plus the bundled skills (`tool-module`, `connector-module`, `design-system`) into `./.claude/skills/`.

---

### `wick run <task>`

Execute an arbitrary task from `wick.yml`. Useful for tasks that don't have a dedicated shortcut.

```bash
wick run css        # runs the "css" task from wick.yml
wick run clean
```

Shortcuts below (`dev`, `setup`, etc.) are equivalent to `wick run <name>`.

---

### `wick skill list`

List the AI agent skills bundled with the installed wick binary.

```bash
$ wick skill list
connector-module
tool-module
design-system
```

---

### `wick skill sync [name...]`

Replace `./.claude/skills/<name>/` with the bundled version. Use this after upgrading wick to pull in updated skill content.

```bash
wick skill sync                                # sync all bundled skills
wick skill sync design-system                  # sync one
wick skill sync tool-module connector-module   # sync several
```

Side effects on `./AGENTS.md`:

- **Missing** — a fresh `AGENTS.md` is written from the bundled template (same file shipped by `wick init`).
- **Present, skill table matches the default shape** — the body of the `| Task | Skill |` table is regenerated from the bundled skill list. Labels for known skills come from wick's internal map; unknown skills fall back to the skill name.
- **Present, skill table is customized** (rows don't link to `./.claude/skills/<name>/SKILL.md`) — left untouched. Edit by hand if you want the new skills listed.

The skill folder contents are always replaced — local edits inside `./.claude/skills/<name>/` will be overwritten. Commit anything you want to keep.

---

### `wick build`

Compile the project to a Go binary with version metadata baked in via Go ldflags, then wrap it into the platform-native distributable (`.exe` / `.dmg` / `.deb`). Reads `name:` and `version:` from `wick.yml` by default; flags / env vars override.

```bash
wick build                                 # → bin/<name>-<goos>-<goarch>[.exe] + native bundle
wick build --target linux/arm64            # cross-compile via shorthand
wick build --goos linux --goarch arm64     # cross-compile via explicit flags
wick build --all                           # build every target the host can produce
wick build -o custom/path                  # rename the raw binary (bundle name unaffected)
wick build --headless                      # drop tray UI (-tags headless)
GOOS=linux GOARCH=arm64 wick build         # cross-compile via env (CI flow)
```

Common flags:

| Flag | Env fallback | Effect |
|---|---|---|
| `--app-name` | `APP_NAME` | Override `name:` from `wick.yml` |
| `--app-version` | `APP_VERSION` | Override `version:` from `wick.yml` |
| `--release-github-pat` | `RELEASE_GITHUB_PAT` | Bake releases PAT for self-updater |
| `--release-github-repo` | `RELEASE_GITHUB_REPOSITORY` | Bake releases repo `owner/repo` for self-updater |
| `-o`, `--output` | — | Raw binary path (default `bin/<name>-<goos>-<goarch>[.exe]`); bundle is written next to it |
| `-t`, `--target` | — | Target shorthand `<os>/<arch>` (e.g. `linux/arm64`); mutex with `--goos`/`--goarch` |
| `--goos` | `GOOS` | Target GOOS; mutex with `--target` |
| `--goarch` | `GOARCH` | Target GOARCH; mutex with `--target` |
| `--all` | — | Best-effort build all OS/arch; auto-skip darwin on non-mac host |
| `--headless` | — | Add `-tags headless` (no tray) — recommended for Termux / server targets |

Full reference incl. CI workflow templates and PAT setup: [`wick build` reference](./build).

---

### `wick server`

Start the HTTP server directly without needing a `wick.yml` task. Equivalent to `go run . server`.

```bash
wick server
wick server --host 127.0.0.1     # bind specific interface
wick server --host 192.168.1.42  # multi-NIC host: bind one IP only
wick server --localhost          # shortcut for --host 127.0.0.1
```

`--host` / `--localhost` forward to the inner `go run . server`, which sets `WICK_HOST` and makes the kernel drop SYN packets from any non-matching source. See [`<app> server`](./app-cli#app-server) for the full rationale, precedence rules, and SSH port-forward pattern.

Use this instead of `wick dev` when you don't need hot-reload or asset generation — production-like run from source.

---

### `wick worker`

Start the background job worker directly without needing a `wick.yml` task. Equivalent to `go run . worker`.

```bash
wick worker
```

Runs the same worker process as `./myapp worker` but straight from source. Useful for running server and worker in separate terminals during development.

---

### `wick all`

Start the HTTP server and the cron scheduler in one process. Equivalent to `go run . all`. See [`<app> all`](./app-cli#app-all) on the App CLI page for the single-node vs. multi-pod trade-off table — same command, just running from source instead of a built binary.

```bash
wick all
wick all --host 127.0.0.1   # bind specific interface
wick all --localhost        # shortcut for --host 127.0.0.1
```

---

### `wick mcp`

Wick's own MCP server, separate from the per-app MCP a built binary ships ([`<app> mcp`](./app-cli#mcp)). Used while developing tools / connectors in the wick repo so a Claude / Cursor session can hit the live in-tree code without going through `wick build`.

```bash
wick mcp serve                                # run over stdio (called by clients)
wick mcp config                               # print a config snippet + target paths
wick mcp install --client claude              # write the entry directly
wick mcp install --client all --mode rebuild  # all clients, force rebuild on launch
```

Build modes (`--mode`, also accepted by `mcp serve`):

| Mode | When to use |
|---|---|
| `auto` (default) | Rebuild only when HEAD commit changed; otherwise run cached binary |
| `dev` | `go run` — no binary, always recompiles, good while actively developing |
| `build` | Build once if binary missing, reuse existing binary otherwise |
| `rebuild` | Always force a full rebuild before running |

`--client` accepts `claude`, `cursor`, `gemini`, `codex`, `claude-code`, `all`. Default server name is the basename of the cwd; override with `--name`.

---

### `wick workflow test <id>`

Run the `__tests__/` fixtures bundled with a workflow under the current agents layout. Walks every case file in `<workflow_id>/__tests__/`, executes the workflow against each, and prints pass / fail counts.

```bash
wick workflow test draft-pr
#   ✓ happy-path (412ms)
#   ✓ missing-input-rejected (88ms)
#   ✗ on-error-branch
#     expected status="failed", got "succeeded"
```

Exits non-zero if any case fails. Useful in CI to gate workflow edits.

---

### `wick doctor [binary]`

Run a sequence of environment checks and print a summary. Each line reports `✓` (ok), `✗` (missing / broken), or `!` (warning). Exit code `0` when all required checks pass, `1` otherwise.

```bash
wick doctor                    # check the wick binary itself
wick doctor wick-lab.exe       # inspect a specific branded build
```

When you pass a binary path, doctor derives that build's `AppName`, locates the matching `<app>-gate` sidecar, and verifies socket / spec paths line up. Useful when you've shipped a branded MSI / .deb and want to confirm the [Command Gate](../guide/command-gate) is wired up before users see it.

The gate-specific checks are detailed in the [Command Gate guide](../guide/command-gate#diagnostics).

---

### `wick upgrade`

Bump the `github.com/yogasw/wick` dependency in the current project's `go.mod` to the latest released version, then tidy and run `dev`.

```bash
$ wick upgrade
current: v0.1.13
latest:  v0.4.2
upgrade v0.1.13 -> v0.2.0? [y/N]: y
> go get github.com/yogasw/wick@v0.20.2
> go mod tidy
> <dev task from wick.yml>
```

Steps:

1. Read the pinned version from the `require` block in `./go.mod`.
2. Fetch the latest version from `https://proxy.golang.org/github.com/yogasw/wick/@latest`.
3. If already on latest, exit without prompting.
4. Otherwise prompt `[y/N]`; only `y`/`yes` proceeds.
5. Run `go get github.com/yogasw/wick@v0.20.2`, then `go mod tidy`, then the `dev` task from [`wick.yml`](./wick-yml).

Run from a project directory (one that has a `go.mod` requiring `github.com/yogasw/wick`).

---

### `wick version`

Print the installed wick version.

```bash
$ wick version
v0.1.12
```

#### `wick version next`

Bump the **last numeric segment** of `version:` in `./wick.yml` by one, write the file back in place (preserving quotes / trailing comments / formatting), and print the new value to stdout.

```bash
$ cat wick.yml | grep '^version:'
version: 0.6.4
$ wick version next
0.6.5
$ cat wick.yml | grep '^version:'
version: 0.6.5
```

| Current `version:` | After `wick version next` |
|---|---|
| `1` | `2` |
| `0.1` | `0.2` |
| `0.6.4` | `0.6.5` |
| `1.2.3.4` | `1.2.3.5` |
| `"0.1.0"` | `"0.1.1"` (quotes preserved) |

Errors out if `wick.yml` has no `version:` line with a numeric value, or the last segment is not an integer.

Used by [`release.yml`](./build#auto-bumping-the-version) when `AUTO_VERSION=true` — `prepare` calls it to resolve the next tag, `release` calls it again on a fresh checkout (idempotent — same baseline, same bump) to commit the diff back to the source branch.

---

## Task shortcuts (from `wick.yml`)

Each of these runs the matching task in `wick.yml`. The commands shown in the "Default behavior" column are what the template ships — edit `wick.yml` to change them.

| Command | Default behavior |
|---------|-------------|
| `wick setup` | Download Tailwind + templ into `./bin/`, run `go mod tidy` |
| `wick dev` | Generate templ + CSS, start `go run . server` |
| `wick generate` | Regenerate templ, run `go generate ./...`, rebuild CSS |
| `wick test` | `go test ./... -coverprofile=./coverage.out` |
| `wick tidy` | `go fmt ./...` + `go mod tidy -v` |

::: tip `wick build` auto-runs `generate`
If `wick.yml` defines a `generate` task, `wick build` runs it before the Go compile step — keeps templ + CSS + `go generate` in sync without a separate task wrapper. Skip it by removing or renaming the task.
:::

See [`wick.yml` reference](./wick-yml) for the full task syntax (`if_missing`, `download`, `bg`, variable interpolation, etc.).

---

## See also

- [App CLI Reference](./app-cli) — subcommands of the binary `wick build` produces (`tray`, `server`, `start`, `stop`, `service`, `config`, `mcp`, `uninstall`).
- [Desktop Tray guide](../guide/desktop-tray) — what the tray menu does and how its autostart toggle relates to `<app> service install`.
- [Termux / Android guide](../guide/termux-android) — running wick headless via the `start` / `service install` flow.
