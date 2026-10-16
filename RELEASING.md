# Releasing wick

Day-to-day work lands on `develop`. `master` only takes squash commits, one
per promotion, so its history stays short. `release` takes a PR from
`master` and publishes the tag.

```
feature PR ──► develop ──(Merge develop to master, approved)──► master ──(PR)──► release
                  ▲                                               │
                  └──────────────── Sync develop ◄────────────────┘  (every push to master)
```

## Workflows

| Workflow | Trigger | What it does |
|---|---|---|
| `merge-develop-to-master.yml` ("Merge develop to master") | manual (Actions → Run workflow, from `master` or `develop`) | `prepare`: fails if develop is behind master; writes the develop commits not on master yet (since the previous squash) to the job summary, with the last `v*` tag for reference; stops green when develop and master have the same tree. `promote`: waits for approval in the `release-approval` environment, optionally bumps `VERSION` on develop, opens or reuses the PR develop → master, squash-merges it with the commit list as the body, optionally opens the PR master → release. |
| `sync-develop.yml` ("Sync develop") | push to `master` (any source), or manual | Merges master into develop: fast-forward when possible, otherwise a merge commit. On a conflict it pushes master's commit to a new branch `sync/master-into-develop-<sha>`, opens the PR "Sync master into develop" from it (or moves an open one forward) and warns in the job summary. Never force-pushes; a conflict ends green. |
| `master-source-guard.yml` | `pull_request` into `master` | Fails unless the PR comes from `develop` or `release-sync/*` in this repository. Required check in the master ruleset. |
| `release.yml` | PR master → release | Tests, tag `v<VERSION>`, GitHub release, squash of `release-sync/v<VERSION>` back into master, merge of the PR into release. |
| `release-plugins.yml` | dispatched by `release.yml` | Builds plugins, then pushes the regenerated `plugins/plugins.json` to master (retries on a moved master, never forced). |

The inputs of "Merge develop to master":

- `version` (optional, `1.2.3`): commits `chore: bump VERSION to 1.2.3` on
  develop before the squash, so the next master → release PR is a core
  release. Must not be lower than develop's `VERSION` and not tagged yet.
  Equal is fine: a rerun with the same `version` after a failed merge skips
  the bump the first run already pushed.
- `open_release_pr` (default off): open the PR master → release right after
  the squash.

The squash commit body ends with a line `Develop-Head: <40-hex sha>` (the
develop commit that was squashed). The next run lists only the develop
commits after it, so a summary never repeats commits that already reached
master. Any line of the body counts, the last one of a commit wins; a
malformed or unknown value is reported as a warning and skipped (the run
falls back to the older squash, or to "everything not on master").

`promote` and **Sync develop** use separate concurrency groups
(`wick-promote`, `wick-sync-develop`), neither shared with `release.yml`:
GitHub keeps one pending run per group and cancels the older one, so a
shared group could drop a sync. Running side by side is safe, because every
push is non-forced and promote re-checks develop/master and pins the merge
to the approved sha.

## Cutting a release

1. Actions → **Merge develop to master** → Run workflow. Set `version` for a
   core release; tick `open_release_pr` to go straight on.
2. Read the commit list in the run summary, then approve the
   `release-approval` deployment.
3. The squash lands on master; **Sync develop** merges it back into develop.
4. From here the master → release PR (opened by step 1, or by
   hand) runs `release.yml`, which tags, publishes, squashes the
   `release-sync` branch into master and dispatches the plugin release.
   Each of those master pushes runs **Sync develop** again.

When something is off:

- *develop is behind master* in `prepare`: wait for **Sync develop** (or run
  it by hand), or merge the open "Sync master into develop" PR.
- *develop moved after this run was prepared*: someone merged into develop
  while the run waited for approval. Start a new run so the approved list
  matches what gets merged.
- *Sync master into develop* PR: its head is a branch
  `sync/master-into-develop-<sha>` cut from master. Resolve the conflict on
  that branch (GitHub's "Resolve conflicts" or a local checkout of it),
  **never commit to master**, then merge the PR with a merge commit (not
  squash) and delete the branch. If the job summary says the PR still
  carries an older master, run **Sync develop** by hand after merging.
- *A predictable conflict: `docs/changelog.md`.* `release.yml` resets the
  `## [Unreleased]` block on master when it tags. If develop added notes to
  that block after the last promotion, the next sync conflicts there. Keep
  develop's new notes in the empty scaffold that master now has, drop the
  ones the release already published, and merge the sync PR. Avoiding it:
  do not add Unreleased notes on develop between a promotion and the
  release that follows it.

## GitHub settings (by hand, never via API)

- Environment `release-approval`:
  - Required reviewers: the owner/admins.
  - "Prevent self-review" off.
  - Deployment branches: **required**, limited to `master` and `develop`.
    `ADMIN_TOKEN` is a repository secret, so this is what keeps a workflow
    copy started from another branch from reaching the approval step.
  - Optional: a wait timer.
- Ruleset `master`:
  - Target `master` only, not repo-wide (`release.yml` merges into `release`
    with a merge commit).
  - Require a pull request with **at least 1 approval**, and "Repository
    admin" on the bypass list (the workflows merge with `ADMIN_TOKEN`). Do
    not use 0 approvals: the environment only gates the workflow, so with 0
    anyone with write access could squash-merge a develop → master PR in the
    UI.
  - Allowed merge method: squash only.
  - Required status check: `master-source-guard`.
  - Block force pushes; restrict deletions.
- Ruleset `develop`:
  - Pull request required, approvals 0, linear history off, restrict
    deletions, bypass "Repository admin" (Sync develop and the VERSION bump
    push directly).
  - Allowed merge methods: merge and rebase (no squash), so per-item commits
    stay visible in the release summary.
  - Block force pushes.
- Ruleset `release-sync/**`: restrict creations, updates and deletions,
  bypass "Repository admin" only, so only `release.yml` (`ADMIN_TOKEN`) can
  create the branches that `master-source-guard` lets into master.
- `sync/**` needs no ruleset: those branches only open PRs into develop
  (which takes PRs from any branch anyway) and `master-source-guard` never
  lets them into master. Sync develop creates them with `GITHUB_TOKEN`, so a
  ruleset that restricts their creation would also need "GitHub Actions" on
  its bypass list. Turn on "Automatically delete head branches" (or delete
  them by hand after the merge).
- Tag ruleset for `v*` and `*/v*`: block update and deletion (Go checksum
  databases make a moved tag dangerous).
- Secret `ADMIN_TOKEN`: a token of a repository admin, so it matches the
  ruleset bypass. A GitHub App token is the better long-term option.

## Bootstrap (first time)

`workflow_dispatch` only shows a workflow once its file is on the default
branch (`master`), and the `release.yml`/`release-plugins.yml` changes only
apply from there too. So the first landing is by hand:

1. Push develop so it contains master (develop must not be behind master).
2. Open one PR develop → master and **squash-merge** it manually. In the
   squash commit message add this line, with the PR's head commit as a full
   40-character sha (`git rev-parse origin/develop`), so the first automated
   run does not list these commits again:

   ```
   Develop-Head: 0123456789abcdef0123456789abcdef01234567
   ```

   It can sit anywhere in the message (next to GitHub's `Co-authored-by:`
   lines is fine) but must be on its own line, exactly `Develop-Head: `
   followed by the sha.
3. Sync develop runs on that push and brings the squash back into develop.
4. Add `master-source-guard` as a required check in the master ruleset
   (the check name only exists after it has run once, on that PR).
5. From then on, use **Merge develop to master**.
