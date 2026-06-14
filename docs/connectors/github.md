---
outline: deep
---

# GitHub

`github` wraps the GitHub REST API v3. One instance = one GitHub account or organisation (Personal Access Token, optional Enterprise base URL).

Operations cover the full PR-review and release loop — reading repos / issues / PRs, diffing and merging PRs, creating PRs, editing files, cutting releases, plus forks, stars, and tags. Anything wick does NOT cover yet is one [`httprest`](./httprest) call away.

| | |
|---|---|
| **Source** | [`internal/connectors/github/`](https://github.com/yogasw/wick/tree/master/internal/connectors/github) |
| **Key** | `github` |
| **Icon** | 🐙 |
| **Tier** | builtin (every wick app) |
| **Health check** | ✅ — verifies the token via `GET /user` |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | | Leave empty for github.com. Set to `https://github.example.com/api/v3` for GitHub Enterprise. |
| `Token` | secret | ✅ | Personal Access Token. Needs `repo` for private repos, `public_repo` for public-only listings. Fine-grained tokens also work — see [scopes](#scopes--permissions). |

### Health check

The connector reports a single `auth` check that calls `GET /user`. A green check means the configured `Token` is valid and reachable (right base URL, not expired/revoked). It does **not** assert per-repo permissions — a token can pass the check but still lack `repo` scope for a specific private repo.

## Operations

`owner` and `repo` are required on every repo-scoped op unless noted. `per_page` caps a single page (max 100) — list ops return the first page only; paginate in your workflow. Destructive ops are opt-in per row at `/manager/connectors/github/{id}`.

### Repositories — read

| Op | Input | What it does |
|---|---|---|
| `list_repos` | `affiliation`, `visibility`, `per_page` | Repos visible to the token. Defaults `affiliation=owner`. |
| `get_repo` | `owner`, `repo` | Full repo metadata (default branch, visibility, counts, URLs). |
| `list_branches` | `owner`, `repo`, `per_page` | Branches with their head commit. |
| `list_commits` | `owner`, `repo`, `sha`, `path`, `author`, `per_page` | Commit history. Optional filters: start `sha`, `path`, `author`. |
| `list_forks` | `owner`, `repo`, `per_page` | Who forked the repo. |
| `list_stargazers` | `owner`, `repo`, `per_page` | Who starred the repo. |

### Repositories — write *(destructive)*

| Op | Input | What it does |
|---|---|---|
| `create_fork` | `owner`, `repo`, `organization`, `name` | Fork into the token's account (or `organization`). |
| `star_repo` | `owner`, `repo` | Star the repo as the authenticated user. |
| `unstar_repo` | `owner`, `repo` | Remove the star. |

### Issues — read

| Op | Input | What it does |
|---|---|---|
| `list_issues` | `owner`, `repo`, `state`, `per_page` | List issues. GitHub returns issues **and** PRs (PR rows have `pull_request != null`); filter client-side. |
| `get_issue` | `owner`, `repo`, `number` | One issue. |
| `list_issue_comments` | `owner`, `repo`, `number`, `per_page` | Comments on an issue or PR. |

### Issues — write *(destructive)*

| Op | Input | What it does |
|---|---|---|
| `create_issue` | `owner`, `repo`, `title`, `body`, `labels` | Create an issue. `labels` is comma-separated. |
| `update_issue` | `owner`, `repo`, `number`, `title`, `body`, `state`, `labels` | Edit / close / reopen. Only provided fields change; `labels` replaces the set. |
| `add_comment` | `owner`, `repo`, `number`, `body` | Comment on an issue **or** PR (PRs are issues for comments). |

### Pull requests — read

| Op | Input | What it does |
|---|---|---|
| `list_prs` | `owner`, `repo`, `state`, `per_page` | List pull requests. |
| `get_pr` | `owner`, `repo`, `number` | One PR with branches, mergeable state, counts. |
| `get_pr_diff` | `owner`, `repo`, `number`, `max_bytes` | Raw unified diff. `max_bytes > 0` truncates (returns `truncated: true`) — keep prompts small. |
| `list_pr_files` | `owner`, `repo`, `number`, `per_page` | Changed files with additions / deletions / status. |

### Pull requests — write *(destructive)*

| Op | Input | What it does |
|---|---|---|
| `create_pr` | `owner`, `repo`, `title`, `head`, `base`, `body`, `draft` | Open a PR. `head` may be `owner:branch` for cross-fork. |
| `update_pr` | `owner`, `repo`, `number`, `title`, `body`, `state`, `base` | Edit / close / retarget. Only provided fields change. |
| `merge_pr` | `owner`, `repo`, `number`, `merge_method`, `commit_title`, `commit_message` | Merge. `merge_method` = `merge` (default) / `squash` / `rebase`. |

### Files

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `get_file` | no | `owner`, `repo`, `path`, `ref` | Read a text file. Base64 unwrapped automatically; binary unsupported. |
| `create_or_update_file` | yes | `owner`, `repo`, `path`, `content`, `message`, `branch`, `sha` | Create or update a file. `content` is plaintext (base64-encoded for you). On update, omit `sha` and the connector looks it up. |

### Releases

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `list_releases` | no | `owner`, `repo`, `per_page` | List releases. |
| `get_latest_release` | no | `owner`, `repo` | The latest published (non-draft, non-prerelease) release. |
| `get_release` | no | `owner`, `repo`, `release_id` | One release by numeric ID. |
| `create_release` | yes | `owner`, `repo`, `tag_name`, `name`, `body`, `target_commitish`, `draft`, `prerelease` | Cut a release. Creates the tag if it doesn't exist. |
| `update_release` | yes | `owner`, `repo`, `release_id`, `tag_name`, `name`, `body`, `draft`, `prerelease` | Edit a release. |
| `delete_release` | yes | `owner`, `repo`, `release_id` | Delete a release (does not delete the git tag). |

### Tags & user

| Op | Input | What it does |
|---|---|---|
| `list_tags` | `owner`, `repo`, `per_page` | List git tags with their commit SHA. |
| `get_me` | — | The authenticated user behind the token. |

Every write op is `connector.OpDestructive`. The MCP layer appends a destructive warning to these ops' descriptions so the LLM confirms before calling; admins can disable individual ops per (row, op) at `/manager/connectors/github/{id}`.

## Example: automated PR review

A workflow fetches the diff with the connector (kept off the agent prompt when large via `max_bytes`), lets an agent decide whether it's worth reviewing, and posts the comment back:

```yaml
- id: diff
  type: connector
  module: github
  op: get_pr_diff
  args:
    owner: "{{.Event.Payload.body.repository.owner.login}}"
    repo: "{{.Event.Payload.body.repository.name}}"
    number: "{{.Event.Payload.body.pull_request.number}}"
    max_bytes: 40000
  arg_modes: { owner: expression, repo: expression, number: expression }

- id: review
  type: agent
  provider: claude
  skills: [pr-review]
  prompt: "Review this PR and, if it needs it, post a comment via github.add_comment.\n\n{{.Node.diff.diff}}"
```

See [Workflows ▶ Anatomy](/workflow/#anatomy) for the surrounding shape.

## Example: file a bug from a Slack thread

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

## Scopes / permissions

- **Classic PAT:** `repo` for private repos (covers read, comment, merge, edit files, releases); `public_repo` for public-only.
- **Fine-grained PAT:** Repository permissions — **Contents** read/write (read diffs/files, merge, edit files, tags), **Pull requests** read/write (PR details, comments, create/merge), **Issues** read/write (issues + comments), and the auto-included **Metadata** read. Add **Administration**/**Workflows** only if you script those.
- Merging respects branch protection — the token's account must satisfy required reviews/checks.

## Quirks worth knowing

- List ops return the **first page only**. Loop with `per_page` + a page param via [`httprest`](./httprest) for deeper history.
- `get_pr_diff` requests the `application/vnd.github.v3.diff` media type and returns raw text under `diff`; use `max_bytes` to cap what you feed an LLM.
- `create_or_update_file`: leave `sha` empty to update — the connector resolves the current blob SHA first; pass `branch` to target a non-default branch.
- `update_issue` / `update_pr` / `update_release` only send the fields you provide — empty fields are left untouched.
- `get_latest_release` skips drafts and pre-releases; use `list_releases` to see those.
- `delete_release` removes the release entry but leaves the underlying git tag in place.
- Don't prefix labels with `#`; GitHub stores them without it, and unknown labels are silently ignored.

## See also

- [Connector Module](/guide/connector-module) — module contract.
- [HTTP / REST](./httprest) — fallback for any GitHub endpoint not typed here.
- [Slack](./slack) — the natural inbound side of a "file bug from Slack" workflow.
