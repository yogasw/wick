# Changelog

All notable changes to Wick are documented here.

---

## [v0.1.0](https://github.com/yogasw/wick/releases/tag/v0.1.0)

Initial public release.

### Added

- `wick init <name>` — scaffold a new project from template, auto-run `go mod tidy` + `go run . setup`
- `wick.yml` cross-platform task runner — `setup`, `dev`, `build`, `test`, `tidy`, `generate`
- Tool modules (`tools/<name>/`) — mount at `/tools/{key}`, typed `Config` with `wick:"..."` tags
- Background job modules (`jobs/<name>/`) — operator surface `/jobs/{key}` + admin surface `/manager/jobs/{key}`
- Tag system — group and filter tools/jobs with `DefaultTag`, admin-managed
- Visibility control — `VisibilityPublic` / `VisibilityPrivate` per tool
- Runtime config — `Config` structs reflected into admin-editable `configs` table rows
- SSO support — configurable from `/admin/configs`, no redeploy needed
- AES-GCM stateless sessions — per-job access, theme cookie persistence
- Tailwind CSS + templ — standalone Tailwind CLI (no Node.js), type-safe Go templates
- Claude Code integration — `agent.md` + Claude skills shipped with every `wick init` project
- External link cards — register URL shortcuts as tool cards via `RegisterToolNoConfig`
- Dark/light theme — user preference persisted via cookie
