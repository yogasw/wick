---
outline: deep
---

# CLI Reference

Wick ships two kinds of commands:

- **Built-in commands** are hardcoded in the `wick` binary (`init`, `run`, `skill`, `version`). They work the same across every project and the behavior is fixed by the installed wick version.
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

Copies the bundled template (tools, jobs, tags, `wick.yml`, `AGENTS.md`, example tool and job) and the shared `design-system` skill into `./.claude/skills/design-system/`.

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
tool-module
design-system
```

---

### `wick skill sync [name...]`

Replace `./.claude/skills/<name>/` with the bundled version. Use this after upgrading wick to pull in updated skill content.

```bash
wick skill sync                       # sync all bundled skills
wick skill sync design-system         # sync one
wick skill sync tool-module design-system
```

Side effects on `./AGENTS.md`:

- **Missing** — a fresh `AGENTS.md` is written from the bundled template (same file shipped by `wick init`).
- **Present, skill table matches the default shape** — the body of the `| Task | Skill |` table is regenerated from the bundled skill list. Labels for known skills come from wick's internal map; unknown skills fall back to the skill name.
- **Present, skill table is customized** (rows don't link to `./.claude/skills/<name>/SKILL.md`) — left untouched. Edit by hand if you want the new skills listed.

The skill folder contents are always replaced — local edits inside `./.claude/skills/<name>/` will be overwritten. Commit anything you want to keep.

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
