# Contributing

Thanks for your interest in contributing to Wick!

## Getting Started

```bash
git clone https://github.com/yogasw/wick.git
cd wick
cp .env.example .env
go run . setup
go run . dev
```

The dev server starts at `http://localhost:8080`.

## Project Layout

```
app/              # RegisterTool / RegisterJob / Run wiring
cmd/cli/          # wick CLI commands (init, run, task)
internal/         # framework internals (admin, auth, SSO, tags, jobs runner)
pkg/              # public packages used by scaffolded projects (tool, job, entity)
template/         # project template — copied by wick init
docs/             # this documentation site (VitePress)
```

## Adding a Feature

- **Framework change** (new `pkg/` API, new admin page) → edit under `internal/` or `pkg/`
- **Template change** (new example tool/job, updated agent.md) → edit under `template/`
- **Docs change** → edit under `docs/`

## Submitting a PR

1. Fork and create a branch: `git checkout -b feat/my-feature`
2. Make your changes
3. Run `go build ./...` and `go test ./... -race` — both must pass
4. Open a pull request against `main`

## Commit Style

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(cli): add --dry-run flag to wick init
fix(job): prevent duplicate cron registration on restart
docs: add contributing guide
```

## Reporting Issues

Open an issue at [github.com/yogasw/wick/issues](https://github.com/yogasw/wick/issues) with:
- What you expected
- What happened
- Steps to reproduce
