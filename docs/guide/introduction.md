# Introduction

Wick is a Go framework for building **internal tools**, **admin panels**, and **background jobs** — designed to be driven by AI. Scaffold a project, describe what you need, and the framework handles admin, tags, SSO, config management, and routing automatically.

![Home](/screenshots/home.png)
*Home page — tools grouped by tag, searchable, compact or detailed view.*

## UI Stack

Wick uses **Tailwind CSS** for styling and **[templ](https://templ.guide)** for HTML templating. Both are installed automatically by `go run . setup` — no manual configuration needed.

- **Tailwind CSS** — utility-first CSS, standalone CLI (no Node.js required)
- **templ** — type-safe Go HTML templates that compile to Go code

`go run . dev` runs `templ generate` and rebuilds CSS before starting the server. `wick.yml` handles everything.

## Project Structure

```
my-app/
├── main.go          # register tools and jobs here
├── AGENTS.md        # AI agent instructions (read by Claude)
├── .claude/skills/  # bundled AI skills (tool-module, design-system)
├── wick.yml         # task runner config
├── .env             # environment variables
├── tools/
│   ├── convert-text/   # example tool
│   └── external/       # external link cards
├── jobs/
│   └── auto-get-data/  # example background job
└── tags/
    └── defaults.go     # shared tag catalog
```

## Two Module Types

| Type | Location | Entry Point | URL |
|------|----------|-------------|-----|
| Tool | `tools/{name}/` | `Register(r tool.Router)` | `/tools/{key}` |
| Job  | `jobs/{name}/`  | `Run(ctx) (string, error)` | `/jobs/{key}` |

## What the Framework Handles

You write the business logic. Wick handles everything else:

- **Admin UI** — config editor, tag management, job schedule
- **Tags & visibility** — group tools, set public/private per tool
- **SSO** — configurable from admin panel, no code changes
- **Runtime config** — typed `Config` structs reflected into admin-editable rows
- **Run history** — every job execution logged automatically
- **Routing** — tools mount at `/tools/{key}`, jobs at `/jobs/{key}`

### Admin Panel

![Admin Dashboard](/screenshots/admin-dashboard.png)
*Dashboard — tools, jobs, enabled/running count, and missing configs at a glance.*

![Admin Users](/screenshots/admin-users.png)
*Users — approve accounts, assign roles and access tags.*

![Admin Tools](/screenshots/admin-tools.png)
*Tool Permissions — enable/disable tools, set visibility, assign tags.*

![Admin Jobs](/screenshots/admin-jobs.png)
*Job Permissions — enable/disable jobs, assign access tags.*

![Admin Tags](/screenshots/admin-tags.png)
*Tags — create group tags (home grouping) and filter tags (access control).*

![Admin Configs](/screenshots/admin-configs.png)
*Configs — runtime variables and SSO providers, no redeploy needed.*
