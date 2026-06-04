---
outline: deep
---

# GitHub

`github` wraps the GitHub REST API v3. One instance = one GitHub account or organisation (Personal Access Token, optional Enterprise base URL).

Operations cover the most common LLM-driven flows: listing repos / issues / PRs, reading file contents, creating issues, and posting comments. Anything wick does NOT cover yet is one [`httprest`](./httprest) call away.

| | |
|---|---|
| **Source** | [`internal/connectors/github/`](https://github.com/yogasw/wick/tree/master/internal/connectors/github) |
| **Key** | `github` |
| **Icon** | 🐙 |
| **Tier** | builtin (every wick app) |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | | Leave empty for github.com. Set to `https://github.example.com/api/v3` for GitHub Enterprise. |
| `Token` | secret | ✅ | Personal Access Token. Needs `repo` for private repos, `public_repo` for public-only listings. Fine-grained tokens also work — the connector quirks list per-op scope needs. |

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `list_repos` | no | `affiliation`, `visibility`, `per_page` | List repos visible to the token. First page only — paginate in your workflow. |
| `list_issues` | no | `owner`, `repo`, `state`, `per_page` | List issues. Note: GitHub's endpoint returns issues **and** PRs (PR rows have `pull_request != null`); filter client-side. |
| `create_issue` | yes | `owner`, `repo`, `title`, `body`, `labels` | Create an issue. `labels` is comma-separated; the connector splits before calling. |
| `get_file` | no | `owner`, `repo`, `path`, `ref` | Read a text file. Base64 unwrapped automatically. Binary files unsupported. |
| `list_prs` | no | `owner`, `repo`, `state`, `per_page` | List pull requests. |
| `add_comment` | yes | `owner`, `repo`, `number`, `body` | Post a comment on an issue or PR. |

Destructive ops are opt-in per row at `/manager/connectors/github/{id}`.

## Example: file a bug from a Slack thread

Inside a workflow, the LLM can hand a triaged Slack message off to `create_issue`:

```yaml
- id: file_bug
  type: connector
  module: github
  op: create_issue
  arg_modes:
    title: expression
    body: expression
  args:
    owner: abc
    repo: web
    title: "{{.Node.classify.parsed.summary}}"
    body: |
      Reported in Slack by <@{{.Node.trigger.payload.user}}>:

      {{.Node.trigger.payload.text}}
    labels: bug,from-slack
```

See [Workflows ▶ Anatomy](/workflow/#anatomy) for the surrounding workflow shape.

## Quirks worth knowing

- `list_repos` defaults `affiliation=owner`. Pass `owner,collaborator,organization_member` to widen.
- `list_issues` and `list_prs` return the first page only. For deeper history, loop in your workflow or call `httprest.get` against `/repos/{owner}/{repo}/issues?page=N` directly.
- Don't prefix labels with `#` — GitHub stores them without it.
- Unknown labels are silently ignored by GitHub when creating an issue.

## See also

- [Connector Module](/guide/connector-module) — module contract.
- [HTTP / REST](./httprest) — fallback for anything not covered above.
- [Slack](./slack) — the natural inbound side of a "file bug from Slack" workflow.
