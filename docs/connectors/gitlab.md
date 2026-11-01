---
outline: deep
---

# GitLab

`gitlab` wraps the GitLab REST API v4, for gitlab.com or a self-managed host. One instance = one host + one access token (optional default project).

Operations cover projects and the repository (branches, commits, compare, files, tree, tags), merge requests (diff, comments, approve, merge), pipelines and job logs, and issues. Merges go through [merge rules](#merge-rules) first. To be told when an MR merges or a pipeline ends, pair `get_merge_request` / `get_pipeline` with a [schedule watch](#waiting-for-an-mr-or-a-pipeline) instead of polling from a conversation.

| | |
|---|---|
| **Source** | [`plugins/connector/gitlab/`](https://github.com/yogasw/wick/tree/master/plugins/connector/gitlab) |
| **Key** | `gitlab` |
| **Tier** | plugin — install with `<app> plugin install gitlab` |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `base_url` | URL | | GitLab host, e.g. `https://gitlab.example.com` (no `/api/v4`). Empty = `https://gitlab.com`. |
| `token` | secret | ✅ | Personal, project or group access token — legacy scopes or fine-grained, see [Token](#token). |
| `default_project` | string | | `group/repo` path used when an op gets no `project`. A bare namespace (`group`) is not a project. |

The config page adds three collapsible panels, all rendered only when opened: [Permission status](#permission-status), [Merge rules](#merge-rules) and [Test merge rules](#test-merge-rules).

## Token

Two kinds of token work.

**Legacy scopes** — the quick path. `read_api` for read-only use (enough to wait for MRs and pipelines), `api` to also create MRs, merge, retry pipelines. GitLab's token form can be opened pre-filled:

```text
https://gitlab.example.com/-/user_settings/personal_access_tokens?name=wick&scopes=read_api
```

**Fine-grained** — least privilege per group/project, but every permission is ticked by hand; GitLab's form cannot be pre-filled for these (only **Duplicate** from an existing fine-grained token copies its permissions). A fine-grained token answers a missing permission with `403 insufficient_granular_scope`. What each op needs, named as the token form shows it ("Group and project" tab unless marked *User*):

| Ops | Permission |
|---|---|
| `get_project` | Projects › Project: Read |
| `list_projects` | *User* › Projects › Project: Read |
| `get_me` | *User* › System Access › User: Read |
| `list_branches` / `create_branch` | Repository › Branch: Read / Create |
| `list_commits`, `get_commit` | Repository › Commit: Read |
| `compare`, `get_file`, `list_tree` | Repository › Repository: Read |
| `list_tags` | Repository › Repository Tag: Read |
| `list_merge_requests`, `get_merge_request`, `get_merge_request_changes`, `list_mr_pipelines` | Repository › Merge Request: Read |
| `create_merge_request` / `update_merge_request` / `approve_merge_request` / `merge_merge_request` | Repository › Merge Request: Create / Update / Approve / Merge |
| `list_mr_notes`, `add_mr_note`, `list_issues`, `get_issue`, `create_issue`, `add_issue_note` | Project Planning › Work Item: Read / Create |
| `list_pipelines`, `get_pipeline`, `list_pipeline_jobs` | CI/CD › Pipeline: Read |
| `retry_pipeline`, `cancel_pipeline` | CI/CD › Pipeline: Update |
| `get_job_log` | CI/CD › Job: Read |

The names come from GitLab's own permission catalog; an older self-managed GitLab may label a few differently or not cover an endpoint yet.

## Permission status

A live checklist of which operations this token can run, against `default_project` (or a project typed in the panel's **Test project** box, not saved). **↻ Recheck** runs it again after a token change.

- Reads are probed live, one `GET` per kind of resource (project, branches, commits, tree, tags, merge requests, pipelines, issues), so a fine-grained token is judged by what GitLab actually answers.
- Writes are never performed: they are judged from the token's scopes and your role in the project (members, inherited included). A fine-grained token cannot describe its own scopes, so its writes show `❔` unless the matching read is already refused.
- A legacy token also shows its name, scopes and expiry (⚠ at 14 days or less). A fine-grained token gets an **Add to the token in GitLab** line listing, deduped, the permissions the denied ops need.
- `404` on the project means a wrong path *or* a project this token may not see — GitLab answers both the same way.

## Merge rules

`merge_merge_request` checks the merge rules before every merge, reading the MR as it is right now (an approval withdrawn a minute ago counts as withdrawn). A refusal lists every unmet rule at once and nothing is merged.

Each row: **repo** (project path `group/repo` or its last part, `*` is a wildcard), **mode** (`allow` or `deny`), **min approvals**, **required approvers** (GitLab usernames).

- `deny` refuses every merge into a matching project, approved or not.
- `allow` needs at least *min approvals* approvals, and every required approver who is on the MR (author, assignee, reviewer or participant) must have approved. A required approver who is not on the MR is not required.
- The most specific row wins: the most literal (non-`*`) characters, then the fewest `*`, then the earlier row. So `*helm*` beats `*`, and `group/app` beats `*helm*`.
- **Else** — the `*` row always exists, renders last and cannot be deleted; only its mode and approvals change. No project merges unchecked because no row matched.

Defaults, also used by an instance that never saved any rules:

| Repo | Mode | Min approvals |
|---|---|---|
| `*helm*` | deny | — |
| `*` (Else) | allow | 1 |

The editor finds a required approver by name as you type (GitLab user search; a token that cannot search users falls back to the members of `default_project`). Every button saves; an invalid row is never written.

## Test merge rules

Paste an MR — its URL, `group/repo!12`, `group/repo/12`, or `!12` for `default_project` — and see the checklist `merge_merge_request` would apply, with **MERGE ALLOWED** or **MERGE REFUSED**. It uses the *saved* rules and never merges. Needs **Merge Request: Read** on the token.

## Operations

| Op | Destructive | What it does |
|---|---|---|
| `get_me` | no | The user the token authenticates as. |
| `list_projects` / `get_project` | no | Projects you are a member of (or all with `all=true`); one project. |
| `list_branches` / `create_branch` | no / yes | Branches; create one from a ref. |
| `list_commits` / `get_commit` / `compare` | no | Commits on a ref, one commit, diff between two refs. |
| `get_file` / `list_tree` / `list_tags` | no | Raw file at a ref, folder listing, tags. |
| `list_merge_requests` / `get_merge_request` / `get_merge_request_changes` | no | MRs, one MR (head pipeline, reviewers, merge status), per-file diffs. |
| `list_mr_notes` / `list_mr_pipelines` | no | MR comments, pipelines that ran for an MR. |
| `create_merge_request` / `update_merge_request` / `add_mr_note` / `approve_merge_request` | yes | Open, edit/close, comment, approve. |
| `merge_merge_request` | yes | Merge now, or auto-merge with `when_pipeline_succeeds` — after [merge rules](#merge-rules). |
| `list_pipelines` / `get_pipeline` / `list_pipeline_jobs` / `get_job_log` | no | Pipelines, one pipeline, its jobs, tail of a job log. |
| `retry_pipeline` / `cancel_pipeline` | yes | Retry failed jobs, cancel a running pipeline. |
| `list_issues` / `get_issue` / `create_issue` / `add_issue_note` | no / yes | Issues and their comments. |

Destructive ops are opt-in per row at `/manager/connectors/gitlab/{id}`.

## Waiting for an MR or a pipeline

Use a schedule watch: it polls in wick without an LLM turn and wakes the session once. Wait for a merge:

```json
{"action":"create","type":"watch","every":"1m","timeout":"24h",
 "message":"MR !12 merged — tell the thread",
 "steps":[
  {"kind":"connector","tool_id":"conn:<gitlab_id>/get_merge_request","params":{"project":"group/repo","mr_iid":12}},
  {"kind":"check","rules":[{"path":"state","op":"equals","value":"merged"}],
   "fail_rules":[{"path":"state","op":"equals","value":"closed"}]}]}
```

With the `gitlab_webhook` tool installed and the project's webhook pointed at it, use `wake_on` instead and skip polling — see the built-in `wick-schedules` skill (section *wake_on*).

## See also

- [Bitbucket](./bitbucket) — the same merge rules model for Bitbucket Cloud.
- [GitHub](./github) — the equivalent connector for GitHub repos.
- [HTTP / REST](./httprest) — fallback for any GitLab API wick hasn't typed yet.
