# Contributing

## Quick start

```bash
git clone https://github.com/yogasw/wick.git
cd wick
cp .env.example .env
go run . setup
go run . dev
```

Dev server starts at `http://localhost:9425`.

## Repo layout

```
app/              # RegisterTool / RegisterJob wiring (framework core)
cmd/cli/          # wick CLI commands (init, dev, server, worker, skill, build)
internal/         # framework internals — admin UI, auth, SSO, tags, job runner, agents
pkg/              # public packages used by scaffolded projects (tool, job, connector, entity)
template/         # project scaffold — copied verbatim by `wick init`
docs/             # this documentation site (VitePress)
```

## Where to make changes

| What you're changing | Where |
|---|---|
| Framework internals (new admin page, new pkg/ API) | `internal/` or `pkg/` |
| Project scaffold (example tool/job, `AGENTS.md`, `wick.yml`) | `template/` |
| Bundled AI skills (`tool-module`, `connector-module`, `design-system`) | `.claude/skills/` |
| Docs | `docs/` |

::: tip Changing the scaffold
`template/` is copied as-is by `wick init`. If you add a new file there, every new project gets it. If you change an existing file, run `wick init` in a temp dir and verify the output looks right before opening a PR.
:::

## Running tests

```bash
go build ./...          # must compile cleanly
go test ./... -race     # must pass, no data races
```

No additional setup required — tests use SQLite in-memory.

## Building the binary

```bash
go build -o wick .
./wick version
```

## Working on the docs site

```bash
cd docs
npm install
npm run dev   # http://localhost:5173
```

Docs use [VitePress](https://vitepress.dev/). Pages live in `docs/guide/`.

## Commit style

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(cli): add --dry-run flag to wick init
fix(job): prevent duplicate cron registration on restart
docs: add agent-host quickstart page
refactor(agents): extract pool slot allocation into separate struct
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`.

Scope is the affected package or area: `cli`, `agents`, `job`, `tool`, `connector`, `mcp`, `admin`, `auth`, `docs`.

Subject line ≤ 72 characters. No period at the end. Body only when the *why* isn't obvious from the diff.

## Submitting a PR

1. Fork and branch: `git checkout -b feat/my-feature`
2. Make changes, add tests where appropriate
3. `go build ./...` and `go test ./... -race` — both must pass
4. Open a pull request against `master`
5. Fill in the PR description: what changed, why, how to test

## Reporting issues

Open an issue at [github.com/yogasw/wick/issues](https://github.com/yogasw/wick/issues) with:

- What you expected
- What happened
- Steps to reproduce
- OS, Go version, wick version (`wick version`)
