---
outline: deep
---

# Bitbucket

`bitbucket` wraps the Bitbucket Cloud REST API (v2.0). One instance = one Bitbucket account (account email + API token, optional default workspace).

Operations cover the common review flows: searching repos, reading commits and diffs, listing and creating pull requests, posting PR comments — including **inline** comments anchored to a specific file and line — and taking review actions (approve, request changes, merge). Anything wick doesn't type yet is one [`httprest`](./httprest) call away.

| | |
|---|---|
| **Source** | [`plugins/connector/bitbucket/`](https://github.com/yogasw/wick/tree/master/plugins/connector/bitbucket) |
| **Key** | `bitbucket` |
| **Tier** | plugin — install with `<app> plugin install bitbucket` |

> This connector is no longer compiled into the wick binary. Install it as a plugin:
>
> ```bash
> <app> plugin install bitbucket
> ```
>
> Existing credential rows are preserved on upgrade — the connector reappears automatically once the plugin is installed and enabled. See [Connector Plugins](/guide/connector-plugins) for the full install flow.

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | | Defaults to `https://api.bitbucket.org/2.0`. |
| `Email` | secret | ✅ | Account email used for Basic Auth. |
| `APIToken` | secret | ✅ | Bitbucket Cloud API token. |
| `DefaultWorkspace` | string | | Workspace slug used when an op omits one. |
| `DefaultPagelen` | int | | Page size for list ops. Default `20`. |
| `MaxPagelen` | int | | Upper bound wick will request. Default `100`. |

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `search_repositories` | no | `workspace`, `query`, `pagelen` | Search repos in a workspace. |
| `get_repository` | no | `workspace`, `repo_slug` | Fetch one repo. |
| `list_commits` | no | `workspace`, `repo_slug`, `revision`, `path`, `pagelen` | List commits, optionally scoped to a branch/tag/hash and path. |
| `get_commit` | no | `workspace`, `repo_slug`, `commit` | Fetch one commit by hash or ref. |
| `get_commit_diff` | no | `workspace`, `repo_slug`, `commit` | Unified diff for a commit (returns text). |
| `list_pull_requests` | no | `workspace`, `repo_slug`, `state`, `query`, `pagelen` | List PRs. |
| `get_pull_request` | no | `workspace`, `repo_slug`, `pull_request_id` | Fetch one PR. |
| `list_pull_request_commits` | no | `workspace`, `repo_slug`, `pull_request_id` | Commits in a PR. |
| `create_branch` | yes | `workspace`, `repo_slug`, `name`, `target` | Create a branch. |
| `create_file_commit` | yes | `workspace`, `repo_slug`, `branch`, `path`, `content`, `message` | Create/update a file via a commit. |
| `create_pull_request` | yes | `workspace`, `repo_slug`, `title`, `source`, `destination`, `description` | Open a PR. |
| `create_pull_request_comment` | yes | see below | Comment on a PR — top-level or inline. |
| `approve_pull_request` | yes | `workspace`, `repo_slug`, `pull_request_id` | Approve a PR as the authenticated user. Idempotent. |
| `request_changes_pull_request` | yes | `workspace`, `repo_slug`, `pull_request_id` | Flag a PR as needing changes. Mutually exclusive with approve. |
| `merge_pull_request` | yes | see below | Merge a PR into its destination branch. Irreversible. |

Destructive ops are opt-in per row at `/manager/connectors/bitbucket/{id}`.

## Inline PR comments

`create_pull_request_comment` posts a top-level comment by default. To anchor it to a specific line in the diff, add the inline fields:

| Field | Type | Notes |
|---|---|---|
| `inline_path` | string | File path the comment attaches to (e.g. `src/main.go`). Required for any inline comment. |
| `inline_to` | int | Line number in the **new** (post-change) side of the diff. Needs `inline_path`. |
| `inline_from` | int | Line number in the **old** (pre-change) side — use for removed/old lines instead of `inline_to`. Needs `inline_path`. |

Set `inline_to` **or** `inline_from`, not both; `inline_to` wins if both are given. Omit all three for an ordinary top-level comment.

```yaml
- id: review_note
  type: connector
  module: bitbucket
  op: create_pull_request_comment
  arg_modes:
    body: expression
  args:
    workspace: my-team
    repo_slug: web
    pull_request_id: "42"
    body: "{{.Node.review.result}}"
    inline_path: src/handler.go
    inline_to: 88
```

## Merging a pull request

`merge_pull_request` accepts optional fields in addition to the required `workspace`, `repo_slug`, and `pull_request_id`:

| Field | Type | Notes |
|---|---|---|
| `merge_strategy` | string | One of `merge_commit` (default), `squash`, or `fast_forward`. Omit to use the repository's default setting. |
| `message` | string | Optional merge commit message. Defaults to Bitbucket's auto-generated message. |
| `close_source_branch` | bool | Close the source branch after a successful merge. Default `false`. |

```yaml
- id: merge_pr
  type: connector
  module: bitbucket
  op: merge_pull_request
  args:
    workspace: my-team
    repo_slug: web
    pull_request_id: "42"
    merge_strategy: squash
    close_source_branch: true
```

## See also

- [Connector Module](/guide/connector-module) — module contract.
- [HTTP / REST](./httprest) — fallback for any Bitbucket API wick hasn't typed yet.
- [GitHub](./github) — the equivalent connector for GitHub repos.
