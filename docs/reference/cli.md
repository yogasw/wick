---
outline: deep
---

# CLI Reference

Wick ships two kinds of commands:

- **Built-in commands** are hardcoded in the `wick` binary (`init`, `run`, `server`, `worker`, `skill`, `upgrade`, `version`). They work the same across every project and the behavior is fixed by the installed wick version.
- **Task shortcuts** (`dev`, `setup`, `build`, `test`, `tidy`, `generate`) are thin wrappers that execute the matching task in your project's [`wick.yml`](./wick-yml). You can edit or extend those tasks per project; `wick run <task>` runs any arbitrary task defined there.

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

### `wick server`

Start the HTTP server directly without needing a `wick.yml` task. Equivalent to `go run . server`.

```bash
wick server
```

Use this instead of `wick dev` when you don't need hot-reload or asset generation — production-like run from source.

---

### `wick worker`

Start the background job worker directly without needing a `wick.yml` task. Equivalent to `go run . worker`.

```bash
wick worker
```

Runs the same worker process as `./myapp worker` but straight from source. Useful for running server and worker in separate terminals during development.

---

### `wick upgrade`

Bump the `github.com/yogasw/wick` dependency in the current project's `go.mod` to the latest released version, then tidy and run `dev`.

```bash
$ wick upgrade
current: v0.1.13
latest:  v0.2.0
upgrade v0.1.13 -> v0.2.0? [y/N]: y
> go get github.com/yogasw/wick@v0.4.1
> go mod tidy
> <dev task from wick.yml>
```

Steps:

1. Read the pinned version from the `require` block in `./go.mod`.
2. Fetch the latest version from `https://proxy.golang.org/github.com/yogasw/wick/@latest`.
3. If already on latest, exit without prompting.
4. Otherwise prompt `[y/N]`; only `y`/`yes` proceeds.
5. Run `go get github.com/yogasw/wick@v0.4.1`, then `go mod tidy`, then the `dev` task from [`wick.yml`](./wick-yml).

Run from a project directory (one that has a `go.mod` requiring `github.com/yogasw/wick`).

---

### `wick version`

Print the installed wick version.

```bash
$ wick version
v0.1.12
```

---

## Task shortcuts (from `wick.yml`)

Each of these runs the matching task in `wick.yml`. The commands shown in the "Default behavior" column are what the template ships — edit `wick.yml` to change them.

| Command | Default behavior |
|---------|-------------|
| `wick setup` | Download Tailwind + templ into `./bin/`, run `go mod tidy` |
| `wick dev` | Generate templ + CSS, start `go run . server` |
| `wick build` | Generate + minify CSS, compile binary to `bin/app` |
| `wick generate` | Regenerate templ, run `go generate ./...`, rebuild CSS |
| `wick test` | `go test ./... -coverprofile=./coverage.out` |
| `wick tidy` | `go fmt ./...` + `go mod tidy -v` |

See [`wick.yml` reference](./wick-yml) for the full task syntax (`if_missing`, `download`, `bg`, variable interpolation, etc.).
