// Package github wraps the GitHub REST API v3 as a wick connector.
// One instance = one GitHub account or organisation (token + optional
// custom base URL for GitHub Enterprise). Operations cover the most
// common LLM-driven workflows: listing repos/issues/PRs, reading file
// contents, creating issues, and posting comments.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — URL construction, input validation, response shaping
//   - repo.go      — outbound HTTP via http.NewRequestWithContext
package github

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "github"

const defaultBaseURL = "https://api.github.com"

// Configs is the per-instance credential set.
type Configs struct {
	BaseURL string `wick:"url;desc=GitHub API base URL. Leave empty for github.com. Set to https://github.example.com/api/v3 for GitHub Enterprise."`
	Token   string `wick:"secret;required;desc=Personal Access Token (PAT) or fine-grained token. Needs repo scope for private repos, public_repo for public ones."`
}

// ListReposInput lists repositories visible to the token.
type ListReposInput struct {
	Affiliation string `wick:"desc=Comma-separated: owner,collaborator,organization_member. Default: owner."`
	Visibility  string `wick:"desc=all | public | private. Default: all."`
	PerPage     int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ListIssuesInput lists issues in a repository.
type ListIssuesInput struct {
	Owner   string `wick:"required;desc=Repository owner (user or org). Example: octocat"`
	Repo    string `wick:"required;desc=Repository name. Example: hello-world"`
	State   string `wick:"desc=open | closed | all. Default: open."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// CreateIssueInput creates a new issue.
type CreateIssueInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Title  string `wick:"required;desc=Issue title."`
	Body   string `wick:"textarea;desc=Issue body (Markdown supported)."`
	Labels string `wick:"desc=Comma-separated label names. Example: bug,help wanted"`
}

// GetFileInput reads a file from a repository.
type GetFileInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	Path  string `wick:"required;desc=File path in repo. Example: README.md or src/main.go"`
	Ref   string `wick:"desc=Branch, tag, or commit SHA. Default: repo default branch."`
}

// ListPRsInput lists pull requests.
type ListPRsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	State   string `wick:"desc=open | closed | all. Default: open."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// AddCommentInput posts a comment on an issue or PR.
type AddCommentInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=Issue or PR number."`
	Body   string `wick:"textarea;required;desc=Comment body (Markdown supported)."`
}

// GetPRDiffInput fetches the unified diff for a pull request.
type GetPRDiffInput struct {
	Owner    string `wick:"required;desc=Repository owner."`
	Repo     string `wick:"required;desc=Repository name."`
	Number   int    `wick:"required;desc=PR number."`
	MaxBytes int    `wick:"desc=Truncate diff to this many bytes; 0 = no limit."`
}

// MergePRInput merges a pull request.
type MergePRInput struct {
	Owner         string `wick:"required;desc=Repository owner."`
	Repo          string `wick:"required;desc=Repository name."`
	Number        int    `wick:"required;desc=PR number."`
	MergeMethod   string `wick:"desc=merge | squash | rebase. Default merge."`
	CommitTitle   string `wick:"desc=Title for the merge commit. Default: GitHub's auto-generated title."`
	CommitMessage string `wick:"textarea;desc=Extra detail appended to the merge commit message."`
}

// CreatePRInput opens a new pull request.
type CreatePRInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	Title string `wick:"required;desc=Pull request title."`
	Head  string `wick:"required;desc=source branch (e.g. feature-x or owner:branch for cross-fork)."`
	Base  string `wick:"required;desc=target branch e.g. main/master."`
	Body  string `wick:"textarea;desc=Pull request body (Markdown supported)."`
	Draft bool   `wick:"desc=Open as a draft PR. Default: false."`
}

// CreateOrUpdateFileInput creates or updates a single file via the contents API.
type CreateOrUpdateFileInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Path    string `wick:"required;desc=file path in repo. Example: src/main.go"`
	Content string `wick:"textarea;required;desc=new file content, PLAINTEXT — will be base64-encoded."`
	Message string `wick:"required;desc=commit message."`
	Branch  string `wick:"desc=target branch; default repo default."`
	Sha     string `wick:"desc=blob sha of the file being replaced; required by GitHub when updating an existing file."`
}

// ── REPO inputs ──────────────────────────────────────────────────────

// GetRepoInput fetches a single repository.
type GetRepoInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
}

// ListBranchesInput lists branches in a repository.
type ListBranchesInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ListCommitsInput lists commits in a repository.
type ListCommitsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Sha     string `wick:"desc=Branch, tag, or commit SHA to start listing from."`
	Path    string `wick:"desc=Only commits touching this file path."`
	Author  string `wick:"desc=Filter by commit author (GitHub login or email)."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ListForksInput lists the forks of a repository (who forked it).
type ListForksInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// CreateForkInput forks a repository.
type CreateForkInput struct {
	Owner        string `wick:"required;desc=Repository owner to fork from."`
	Repo         string `wick:"required;desc=Repository name to fork."`
	Organization string `wick:"desc=Org to fork into. Default: the authenticated user's account."`
	Name         string `wick:"desc=Name for the new fork. Default: same as source repo."`
}

// ListStargazersInput lists the users who starred a repository.
type ListStargazersInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// StarRepoInput stars a repository for the authenticated user.
type StarRepoInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
}

// UnstarRepoInput removes a star from a repository.
type UnstarRepoInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
}

// ── ISSUE inputs ─────────────────────────────────────────────────────

// GetIssueInput fetches a single issue.
type GetIssueInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=Issue number."`
}

// UpdateIssueInput edits an existing issue.
type UpdateIssueInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=Issue number."`
	Title  string `wick:"desc=New issue title. Omit to leave unchanged."`
	Body   string `wick:"textarea;desc=New issue body (Markdown). Omit to leave unchanged."`
	State  string `wick:"desc=open | closed. Omit to leave unchanged."`
	Labels string `wick:"desc=Comma-separated label names; replaces the existing set. Omit to leave unchanged."`
}

// ListIssueCommentsInput lists comments on an issue or PR.
type ListIssueCommentsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Number  int    `wick:"required;desc=Issue or PR number."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ── PULL REQUEST inputs ──────────────────────────────────────────────

// GetPRInput fetches a single pull request.
type GetPRInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=PR number."`
}

// ListPRFilesInput lists the files changed in a pull request.
type ListPRFilesInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Number  int    `wick:"required;desc=PR number."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// UpdatePRInput edits an existing pull request.
type UpdatePRInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=PR number."`
	Title  string `wick:"desc=New PR title. Omit to leave unchanged."`
	Body   string `wick:"textarea;desc=New PR body (Markdown). Omit to leave unchanged."`
	State  string `wick:"desc=open | closed. Omit to leave unchanged."`
	Base   string `wick:"desc=New base branch. Omit to leave unchanged."`
}

// ── RELEASE inputs ───────────────────────────────────────────────────

// ListReleasesInput lists releases in a repository.
type ListReleasesInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// GetLatestReleaseInput fetches the latest published release.
type GetLatestReleaseInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
}

// GetReleaseInput fetches a single release by ID.
type GetReleaseInput struct {
	Owner     string `wick:"required;desc=Repository owner."`
	Repo      string `wick:"required;desc=Repository name."`
	ReleaseID int    `wick:"required;desc=Release ID (numeric, from list_releases)."`
}

// CreateReleaseInput publishes a new release.
type CreateReleaseInput struct {
	Owner           string `wick:"required;desc=Repository owner."`
	Repo            string `wick:"required;desc=Repository name."`
	TagName         string `wick:"required;desc=Git tag to create/use for the release. Example: v1.2.0"`
	Name            string `wick:"desc=Release title. Default: the tag name."`
	Body            string `wick:"textarea;desc=Release notes (Markdown supported)."`
	TargetCommitish string `wick:"desc=Branch or commit the tag is created from. Default: default branch."`
	Draft           bool   `wick:"desc=Create as a draft (unpublished). Default: false."`
	Prerelease      bool   `wick:"desc=Mark as a pre-release. Default: false."`
}

// UpdateReleaseInput edits an existing release.
type UpdateReleaseInput struct {
	Owner      string `wick:"required;desc=Repository owner."`
	Repo       string `wick:"required;desc=Repository name."`
	ReleaseID  int    `wick:"required;desc=Release ID to update."`
	TagName    string `wick:"desc=New git tag. Omit to leave unchanged."`
	Name       string `wick:"desc=New release title. Omit to leave unchanged."`
	Body       string `wick:"textarea;desc=New release notes (Markdown). Omit to leave unchanged."`
	Draft      bool   `wick:"desc=Set draft state. Sent only when true."`
	Prerelease bool   `wick:"desc=Set pre-release state. Sent only when true."`
}

// DeleteReleaseInput removes a release.
type DeleteReleaseInput struct {
	Owner     string `wick:"required;desc=Repository owner."`
	Repo      string `wick:"required;desc=Repository name."`
	ReleaseID int    `wick:"required;desc=Release ID to delete."`
}

// ── TAG inputs ───────────────────────────────────────────────────────

// ListTagsInput lists tags in a repository.
type ListTagsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ── USER inputs ──────────────────────────────────────────────────────

// GetMeInput fetches the authenticated user. Takes no arguments.
type GetMeInput struct{}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "GitHub",
		Description: "Comprehensive GitHub REST API connector: repos, issues, PRs (incl. diff/merge), releases, tags, forks, stars, file contents/edits, the authenticated user, and a token health check.",
		Icon:        "🐙",
	}
}

// Operations returns the LLM-callable actions for this connector.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"list_repos",
			"List Repositories",
			"List repositories visible to the authenticated token. Returns name, description, language, visibility, and clone URL.",
			ListReposInput{},
			listRepos,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"repos": "Array of repo summaries (full_name, name, description, private, language, default_branch, html_url).",
				},
				Quirks: []string{
					"affiliation defaults to \"owner\" — pass \"owner,collaborator,organization_member\" to widen.",
					"GitHub paginates server-side. PerPage max 100; for >100 repos call again with the next page (this op currently returns the first page only).",
					"PAT scope: repo for private repos, public_repo for public-only listings.",
				},
				PairWith:     []string{"connector:github.list_issues", "connector:github.list_prs", "connector:github.get_file"},
				InputSample:  `{"affiliation":"owner","visibility":"all","per_page":30}`,
				OutputSample: `{"repos":[{"full_name":"abc/web","name":"web","private":false,"language":"Go","default_branch":"main","html_url":"https://github.com/abc/web"}]}`,
			},
		),
		connector.Op(
			"list_issues",
			"List Issues",
			"List issues in a repository. Returns number, title, state, labels, and author.",
			ListIssuesInput{},
			listIssues,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"issues": "Array of issue summaries (number, title, state, labels[], user.login, html_url, created_at, updated_at).",
				},
				Quirks: []string{
					"GitHub's REST issues endpoint returns BOTH issues and pull requests — PR rows have a non-null pull_request key. Filter client-side if you want issues only.",
					"state defaults to \"open\". Pass \"all\" to include closed.",
					"Pagination: PerPage max 100, page param not exposed here (first page only). Loop in your workflow if you need deeper history.",
				},
				PairWith:     []string{"connector:github.create_issue", "connector:github.add_comment"},
				InputSample:  `{"owner":"abc","repo":"web","state":"open","per_page":30}`,
				OutputSample: `{"issues":[{"number":42,"title":"Payment refund bug","state":"open","labels":[{"name":"bug"}],"user":{"login":"yoga"},"html_url":"https://github.com/abc/web/issues/42"}]}`,
			},
		),
		connector.OpDestructive(
			"create_issue",
			"Create Issue",
			"Create a new issue in a repository. Returns the created issue number and URL.",
			CreateIssueInput{},
			createIssue,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number":   "Created issue number — pass to add_comment for follow-ups.",
					"html_url": "Web URL of the new issue, useful for Slack message replies.",
					"state":    "Always \"open\" right after creation.",
				},
				TemplateableFields: []string{"owner", "repo", "title", "body", "labels"},
				Quirks: []string{
					"labels is COMMA-separated string at the wick layer; the connector splits it server-side before calling GitHub.",
					"body supports GitHub-flavoured Markdown (mentions, task lists, code fences).",
					"PAT scope: repo (private) or public_repo (public). Issues permission must be set to write in fine-grained PATs.",
					"Won't fail if labels don't exist — GitHub silently ignores unknown labels.",
				},
				PairWith: []string{"connector:github.add_comment", "connector:github.list_issues"},
				CommonPitfalls: []string{
					"Don't include \"#\" in labels (label is \"bug\", not \"#bug\").",
				},
				InputSample:  `{"owner":"abc","repo":"web","title":"Payment refund bug","body":"User U12345 reports failed refunds.\n\n## Steps\n- ...","labels":"bug,priority:high"}`,
				OutputSample: `{"number":42,"html_url":"https://github.com/abc/web/issues/42","state":"open","title":"Payment refund bug"}`,
				Examples: []wickdocs.Example{
					{
						Name: "create_from_slack",
						Body: `- id: file_bug
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
    body: "Reported in Slack by <@{{.Node.trigger.payload.user}}>:\n\n{{.Node.trigger.payload.text}}"
    labels: bug,from-slack`,
					},
				},
			},
		),
		connector.Op(
			"get_file",
			"Get File Content",
			"Read a file from a repository. Returns the decoded text content. Binary files are not supported.",
			GetFileInput{},
			getFile, wickdocs.Docs{},
		),
		connector.Op(
			"list_prs",
			"List Pull Requests",
			"List pull requests in a repository. Returns number, title, state, head/base branches, and author.",
			ListPRsInput{},
			listPRs, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"add_comment",
			"Add Comment",
			"Post a comment on an issue or pull request. Returns the comment ID and URL.",
			AddCommentInput{},
			addComment, wickdocs.Docs{},
		),
		connector.Op(
			"get_pr_diff",
			"Get PR Diff",
			"Fetch the unified diff for a pull request as raw text. Optionally truncate to a byte budget for LLM review.",
			GetPRDiffInput{},
			getPRDiff,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"diff":      "Unified diff text for the PR (the same bytes GitHub serves at the .diff URL).",
					"truncated": "true when the diff was cut to fit max_bytes; the marker line \"…[diff truncated]\" is appended.",
					"bytes":     "Length in bytes of the returned diff string (after any truncation).",
				},
				Quirks: []string{
					"Returns RAW unified diff text, not JSON — feed it straight to an LLM reviewer.",
					"Large PRs can be huge; set max_bytes (e.g. 100000) to keep the payload within model context.",
					"max_bytes truncates on a byte boundary, so the last hunk may be cut mid-line.",
				},
				PairWith:     []string{"connector:github.add_comment", "connector:github.merge_pr", "connector:github.list_prs"},
				InputSample:  `{"owner":"abc","repo":"web","number":7,"max_bytes":100000}`,
				OutputSample: `{"diff":"diff --git a/main.go b/main.go\n...","truncated":false,"bytes":512}`,
			},
		),
		connector.OpDestructive(
			"merge_pr",
			"Merge Pull Request",
			"Merge a pull request using merge, squash, or rebase. Returns the merge commit SHA.",
			MergePRInput{},
			mergePR,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"merged":  "true when the PR was merged.",
					"sha":     "SHA of the resulting merge commit.",
					"message": "Human-readable status from GitHub (e.g. \"Pull Request successfully merged\").",
				},
				Quirks: []string{
					"merge_method defaults to \"merge\". Use \"squash\" to collapse commits or \"rebase\" to replay them.",
					"GitHub returns 405 if the PR is not mergeable (conflicts, required checks failing, branch protection).",
					"PAT scope: repo (or fine-grained Pull requests: write).",
				},
				PairWith:     []string{"connector:github.get_pr_diff", "connector:github.list_prs"},
				InputSample:  `{"owner":"abc","repo":"web","number":7,"merge_method":"squash","commit_title":"Ship feature X"}`,
				OutputSample: `{"merged":true,"sha":"6dcb09b5b57875f334f61aebed695e2e4193db5e","message":"Pull Request successfully merged"}`,
			},
		),
		connector.OpDestructive(
			"create_pr",
			"Create Pull Request",
			"Open a new pull request from a head branch into a base branch. Returns the PR number and URL.",
			CreatePRInput{},
			createPR,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number":   "Created PR number — pass to merge_pr, get_pr_diff, or add_comment.",
					"html_url": "Web URL of the new PR, useful for Slack replies.",
					"state":    "Always \"open\" right after creation (\"draft\" PRs still report state \"open\").",
				},
				Quirks: []string{
					"head is the SOURCE branch; for a PR from a fork use \"owner:branch\".",
					"base is the TARGET branch (e.g. main or master) — must already exist.",
					"draft=true requires a repo plan that supports draft PRs; GitHub 422s otherwise.",
					"GitHub 422s if a PR for the same head→base already exists or head has no commits ahead of base.",
				},
				PairWith:     []string{"connector:github.create_or_update_file", "connector:github.merge_pr", "connector:github.add_comment"},
				InputSample:  `{"owner":"abc","repo":"web","title":"Add retry logic","head":"feature/retry","base":"main","body":"Closes #42","draft":false}`,
				OutputSample: `{"number":51,"html_url":"https://github.com/abc/web/pull/51","state":"open"}`,
			},
		),
		connector.OpDestructive(
			"create_or_update_file",
			"Create or Update File",
			"Create a new file or replace an existing one with a single commit. Content is plaintext and base64-encoded for you.",
			CreateOrUpdateFileInput{},
			createOrUpdateFile,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"commit.sha":       "SHA of the commit that created/updated the file.",
					"content.path":     "Path of the written file.",
					"content.html_url": "Web URL of the file at the new commit.",
				},
				Quirks: []string{
					"content is PLAINTEXT — the connector base64-encodes it before calling GitHub.",
					"Updating an existing file needs its current blob sha; leave sha empty and the connector looks it up automatically (and creates the file if it does not exist).",
					"branch defaults to the repo's default branch; set it to commit onto a feature branch (pair with create_pr).",
					"PAT scope: repo (or fine-grained Contents: write).",
				},
				PairWith:     []string{"connector:github.get_file", "connector:github.create_pr"},
				InputSample:  `{"owner":"abc","repo":"web","path":"docs/CHANGELOG.md","content":"# Changelog\n\n- v1.2.0\n","message":"docs: update changelog","branch":"main"}`,
				OutputSample: `{"commit":{"sha":"7638417db6d59f3c431d3e1f261cc637155684cd"},"content":{"path":"docs/CHANGELOG.md","html_url":"https://github.com/abc/web/blob/main/docs/CHANGELOG.md"}}`,
			},
		),

		// ── REPO ─────────────────────────────────────────────────────
		connector.Op(
			"get_repo",
			"Get Repository",
			"Fetch a single repository's metadata (description, default branch, stars, forks, visibility).",
			GetRepoInput{},
			getRepo,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"full_name":        "owner/name slug.",
					"default_branch":   "Default branch name — useful before create_or_update_file or create_pr.",
					"stargazers_count": "Number of stars.",
				},
				Quirks: []string{
					"Returns the full repo object; pick the fields you need.",
					"PAT scope: repo for private repos, public_repo (or none) for public ones.",
				},
				PairWith:    []string{"connector:github.list_branches", "connector:github.list_commits"},
				InputSample: `{"owner":"abc","repo":"web"}`,
			},
		),
		connector.Op(
			"list_branches",
			"List Branches",
			"List branches in a repository. Returns branch name, head commit SHA, and protection flag.",
			ListBranchesInput{},
			listBranches,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"branches": "Array of {name, commit.sha, protected}.",
				},
				Quirks: []string{
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.create_pr", "connector:github.list_commits"},
				InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
			},
		),
		connector.Op(
			"list_commits",
			"List Commits",
			"List commits in a repository, optionally filtered by branch/sha, file path, or author.",
			ListCommitsInput{},
			listCommits,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"commits": "Array of {sha, commit.message, commit.author, author.login, html_url}.",
				},
				Quirks: []string{
					"sha selects the branch/tag/SHA to start from; path filters to commits touching that file.",
					"author matches a GitHub login or commit email.",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.get_repo", "connector:github.list_branches"},
				InputSample: `{"owner":"abc","repo":"web","sha":"main","path":"go.mod","per_page":30}`,
			},
		),
		connector.Op(
			"list_forks",
			"List Forks",
			"List the forks of a repository — i.e. who forked it.",
			ListForksInput{},
			listForks,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"forks": "Array of forked repos (full_name, owner.login, html_url).",
				},
				Quirks: []string{
					"Each entry is a full repo object owned by the forker; owner.login is who forked.",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.create_fork", "connector:github.list_stargazers"},
				InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
			},
		),
		connector.OpDestructive(
			"create_fork",
			"Create Fork",
			"Fork a repository into the authenticated user's account or an organization.",
			CreateForkInput{},
			createFork,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"full_name": "owner/name of the new fork.",
					"html_url":  "Web URL of the new fork.",
				},
				Quirks: []string{
					"Forking is asynchronous on GitHub's side; the response describes the queued fork.",
					"organization forks into that org; name renames the fork (defaults to the source name).",
					"PAT scope: repo (or fine-grained Administration/Contents on the target).",
				},
				PairWith:    []string{"connector:github.list_forks"},
				InputSample: `{"owner":"abc","repo":"web","organization":"my-org","name":"web-fork"}`,
			},
		),
		connector.Op(
			"list_stargazers",
			"List Stargazers",
			"List the users who starred a repository.",
			ListStargazersInput{},
			listStargazers,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"stargazers": "Array of users (login, html_url).",
				},
				Quirks: []string{
					"Returns the basic user object for each stargazer.",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.star_repo", "connector:github.list_forks"},
				InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
			},
		),
		connector.OpDestructive(
			"star_repo",
			"Star Repository",
			"Star a repository for the authenticated user.",
			StarRepoInput{},
			starRepo,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok": "true when GitHub returned 204 No Content.",
				},
				Quirks: []string{
					"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
					"Idempotent — starring an already-starred repo still returns 204.",
					"PAT scope: user or public_repo.",
				},
				PairWith:    []string{"connector:github.unstar_repo", "connector:github.list_stargazers"},
				InputSample: `{"owner":"abc","repo":"web"}`,
			},
		),
		connector.OpDestructive(
			"unstar_repo",
			"Unstar Repository",
			"Remove the authenticated user's star from a repository.",
			UnstarRepoInput{},
			unstarRepo,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok": "true when GitHub returned 204 No Content.",
				},
				Quirks: []string{
					"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
					"Idempotent — unstarring a repo that isn't starred still returns 204.",
				},
				PairWith:    []string{"connector:github.star_repo"},
				InputSample: `{"owner":"abc","repo":"web"}`,
			},
		),

		// ── ISSUES ───────────────────────────────────────────────────
		connector.Op(
			"get_issue",
			"Get Issue",
			"Fetch a single issue by number. Returns title, body, state, labels, and author.",
			GetIssueInput{},
			getIssue,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number": "Issue number.",
					"state":  "open | closed.",
					"body":   "Issue body (Markdown).",
				},
				Quirks: []string{
					"GitHub's issues endpoint also serves PRs; a non-null pull_request key means this is a PR.",
				},
				PairWith:    []string{"connector:github.update_issue", "connector:github.list_issue_comments"},
				InputSample: `{"owner":"abc","repo":"web","number":42}`,
			},
		),
		connector.OpDestructive(
			"update_issue",
			"Update Issue",
			"Edit an issue's title, body, state, or labels. Only provided fields are changed.",
			UpdateIssueInput{},
			updateIssue,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number": "Issue number.",
					"state":  "Resulting state after the update.",
				},
				Quirks: []string{
					"Only fields you supply are sent — omit a field to leave it unchanged.",
					"labels is comma-separated and REPLACES the full label set (it is not additive).",
					"state must be open or closed; close an issue by passing state=closed.",
					"PAT scope: repo (or fine-grained Issues: write).",
				},
				PairWith:    []string{"connector:github.get_issue", "connector:github.add_comment"},
				InputSample: `{"owner":"abc","repo":"web","number":42,"state":"closed","labels":"bug,wontfix"}`,
			},
		),
		connector.Op(
			"list_issue_comments",
			"List Issue Comments",
			"List comments on an issue or pull request.",
			ListIssueCommentsInput{},
			listIssueComments,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"comments": "Array of {id, user.login, body, created_at, html_url}.",
				},
				Quirks: []string{
					"Works for both issues and PRs (they share the issue comments timeline).",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.add_comment", "connector:github.get_issue"},
				InputSample: `{"owner":"abc","repo":"web","number":42,"per_page":30}`,
			},
		),

		// ── PULL REQUESTS ────────────────────────────────────────────
		connector.Op(
			"get_pr",
			"Get Pull Request",
			"Fetch a single pull request by number. Returns title, state, head/base, mergeable status.",
			GetPRInput{},
			getPR,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number":    "PR number.",
					"state":     "open | closed.",
					"merged":    "true once the PR has been merged.",
					"mergeable": "GitHub's mergeability assessment (may be null while computing).",
				},
				Quirks: []string{
					"mergeable can be null right after open while GitHub computes it; poll again if needed.",
				},
				PairWith:    []string{"connector:github.get_pr_diff", "connector:github.list_pr_files", "connector:github.merge_pr"},
				InputSample: `{"owner":"abc","repo":"web","number":7}`,
			},
		),
		connector.Op(
			"list_pr_files",
			"List PR Files",
			"List the files changed in a pull request with additions/deletions and status.",
			ListPRFilesInput{},
			listPRFiles,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"files": "Array of {filename, status, additions, deletions, changes}.",
				},
				Quirks: []string{
					"status is added | modified | removed | renamed.",
					"Pagination: PerPage max 100; first page only — large PRs may have more files.",
				},
				PairWith:    []string{"connector:github.get_pr_diff", "connector:github.get_pr"},
				InputSample: `{"owner":"abc","repo":"web","number":7,"per_page":30}`,
			},
		),
		connector.OpDestructive(
			"update_pr",
			"Update Pull Request",
			"Edit a pull request's title, body, state, or base branch. Only provided fields are changed.",
			UpdatePRInput{},
			updatePR,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"number": "PR number.",
					"state":  "Resulting state after the update.",
				},
				Quirks: []string{
					"Only fields you supply are sent — omit a field to leave it unchanged.",
					"Close a PR (without merging) by passing state=closed; reopen with state=open.",
					"base retargets the PR onto a different branch (must already exist).",
					"PAT scope: repo (or fine-grained Pull requests: write).",
				},
				PairWith:    []string{"connector:github.get_pr", "connector:github.merge_pr"},
				InputSample: `{"owner":"abc","repo":"web","number":7,"title":"Updated title","state":"open"}`,
			},
		),

		// ── RELEASES ─────────────────────────────────────────────────
		connector.Op(
			"list_releases",
			"List Releases",
			"List releases in a repository. Returns tag, name, draft/prerelease flags, and publish time.",
			ListReleasesInput{},
			listReleases,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"releases": "Array of {id, tag_name, name, draft, prerelease, published_at, html_url}.",
				},
				Quirks: []string{
					"Includes drafts only if the token can see them.",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.get_latest_release", "connector:github.get_release"},
				InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
			},
		),
		connector.Op(
			"get_latest_release",
			"Get Latest Release",
			"Fetch the latest published, non-draft release of a repository.",
			GetLatestReleaseInput{},
			getLatestRelease,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"tag_name": "Tag of the latest release.",
					"name":     "Release title.",
					"body":     "Release notes (Markdown).",
				},
				Quirks: []string{
					"\"Latest\" excludes drafts and pre-releases; GitHub 404s if there is no published release.",
				},
				PairWith:    []string{"connector:github.list_releases"},
				InputSample: `{"owner":"abc","repo":"web"}`,
			},
		),
		connector.Op(
			"get_release",
			"Get Release",
			"Fetch a single release by its numeric ID.",
			GetReleaseInput{},
			getRelease,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"id":       "Release ID.",
					"tag_name": "Release tag.",
					"assets":   "Array of attached release assets.",
				},
				Quirks: []string{
					"release_id is the numeric ID from list_releases, NOT the tag name.",
				},
				PairWith:    []string{"connector:github.list_releases", "connector:github.update_release"},
				InputSample: `{"owner":"abc","repo":"web","release_id":123456}`,
			},
		),
		connector.OpDestructive(
			"create_release",
			"Create Release",
			"Publish a new release for a tag. Returns the release ID and URL.",
			CreateReleaseInput{},
			createRelease,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"id":       "Created release ID.",
					"html_url": "Web URL of the release.",
				},
				Quirks: []string{
					"tag_name is created if it doesn't exist (off target_commitish, default branch otherwise).",
					"draft=true keeps it unpublished; prerelease=true flags it as a pre-release.",
					"PAT scope: repo (or fine-grained Contents: write).",
				},
				PairWith:    []string{"connector:github.update_release", "connector:github.list_releases"},
				InputSample: `{"owner":"abc","repo":"web","tag_name":"v1.2.0","name":"v1.2.0","body":"Bug fixes","draft":false}`,
			},
		),
		connector.OpDestructive(
			"update_release",
			"Update Release",
			"Edit an existing release's tag, name, notes, or draft/pre-release flags. Only provided fields are changed.",
			UpdateReleaseInput{},
			updateRelease,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"id":       "Release ID.",
					"html_url": "Web URL of the release.",
				},
				Quirks: []string{
					"Only fields you supply are sent — omit text fields to leave them unchanged.",
					"draft and prerelease are sent only when true (pass them to set the flags on).",
					"PAT scope: repo (or fine-grained Contents: write).",
				},
				PairWith:    []string{"connector:github.get_release", "connector:github.delete_release"},
				InputSample: `{"owner":"abc","repo":"web","release_id":123456,"name":"v1.2.1","body":"Patched"}`,
			},
		),
		connector.OpDestructive(
			"delete_release",
			"Delete Release",
			"Delete a release by its numeric ID.",
			DeleteReleaseInput{},
			deleteRelease,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok": "true when GitHub returned 204 No Content.",
				},
				Quirks: []string{
					"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
					"Deletes the release record but leaves the underlying git tag in place.",
					"PAT scope: repo (or fine-grained Contents: write).",
				},
				PairWith:    []string{"connector:github.list_releases"},
				InputSample: `{"owner":"abc","repo":"web","release_id":123456}`,
			},
		),

		// ── TAGS ─────────────────────────────────────────────────────
		connector.Op(
			"list_tags",
			"List Tags",
			"List tags in a repository. Returns tag name and the commit it points to.",
			ListTagsInput{},
			listTags,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"tags": "Array of {name, commit.sha, zipball_url, tarball_url}.",
				},
				Quirks: []string{
					"Lists lightweight + annotated tags by name; ordering is GitHub's default.",
					"Pagination: PerPage max 100; first page only.",
				},
				PairWith:    []string{"connector:github.list_releases", "connector:github.list_commits"},
				InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
			},
		),

		// ── USER ─────────────────────────────────────────────────────
		connector.Op(
			"get_me",
			"Get Authenticated User",
			"Fetch the user the configured token belongs to (login, name, account type).",
			GetMeInput{},
			getMe,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"login": "The authenticated user's GitHub login.",
					"name":  "Display name (may be null).",
					"type":  "User | Organization.",
				},
				Quirks: []string{
					"Takes no arguments — identifies whoever owns the token.",
					"Also used as the connector's health-check probe (GET /user).",
				},
				PairWith:    []string{"connector:github.list_repos"},
				InputSample: `{}`,
			},
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────

func listRepos(c *connector.Ctx) (any, error) {
	affiliation := firstNonEmpty(c.Input("affiliation"), "owner")
	visibility := firstNonEmpty(c.Input("visibility"), "all")
	perPage := firstNonZero(c.InputInt("per_page"), 30)

	url := buildURL(c, "/user/repos") +
		fmt.Sprintf("?affiliation=%s&visibility=%s&per_page=%d", affiliation, visibility, perPage)
	return doRequest(c, "GET", url, nil)
}

func listIssues(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	state := firstNonEmpty(c.Input("state"), "open")
	perPage := firstNonZero(c.InputInt("per_page"), 30)

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues", owner, repo)) +
		fmt.Sprintf("?state=%s&per_page=%d", state, perPage)
	return doRequest(c, "GET", url, nil)
}

func createIssue(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(c.Input("title"))
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	body := map[string]any{"title": title}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if labels := parseCSV(c.Input("labels")); len(labels) > 0 {
		body["labels"] = labels
	}

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues", owner, repo))
	return doRequest(c, "POST", url, body)
}

func getFile(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(c.Input("path"))
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, strings.TrimPrefix(path, "/")))
	if ref := strings.TrimSpace(c.Input("ref")); ref != "" {
		url += "?ref=" + ref
	}

	raw, err := doRequest(c, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Decode base64 content that GitHub wraps file blobs in.
	if m, ok := raw.(map[string]any); ok {
		if encoded, ok := m["content"].(string); ok {
			clean := strings.ReplaceAll(encoded, "\n", "")
			decoded, decErr := base64.StdEncoding.DecodeString(clean)
			if decErr == nil {
				m["content"] = string(decoded)
			}
		}
	}
	return raw, nil
}

func listPRs(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	state := firstNonEmpty(c.Input("state"), "open")
	perPage := firstNonZero(c.InputInt("per_page"), 30)

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)) +
		fmt.Sprintf("?state=%s&per_page=%d", state, perPage)
	return doRequest(c, "GET", url, nil)
}

func addComment(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number))
	return doRequest(c, "POST", url, map[string]any{"body": body})
}

func getPRDiff(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	maxBytes := c.InputInt("max_bytes")

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number))
	diff, err := doRequestText(c, "GET", url, "application/vnd.github.v3.diff", nil)
	if err != nil {
		return nil, err
	}

	truncated := false
	if maxBytes > 0 && len(diff) > maxBytes {
		diff = diff[:maxBytes] + "\n…[diff truncated]"
		truncated = true
	}

	return map[string]any{
		"diff":      diff,
		"truncated": truncated,
		"bytes":     len(diff),
	}, nil
}

func mergePR(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}

	body := map[string]any{"merge_method": firstNonEmpty(c.Input("merge_method"), "merge")}
	if title := strings.TrimSpace(c.Input("commit_title")); title != "" {
		body["commit_title"] = title
	}
	if msg := strings.TrimSpace(c.Input("commit_message")); msg != "" {
		body["commit_message"] = msg
	}

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repo, number))
	return doRequest(c, "PUT", url, body)
}

func createPR(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(c.Input("title"))
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	head := strings.TrimSpace(c.Input("head"))
	if head == "" {
		return nil, fmt.Errorf("head is required")
	}
	base := strings.TrimSpace(c.Input("base"))
	if base == "" {
		return nil, fmt.Errorf("base is required")
	}

	body := map[string]any{"title": title, "head": head, "base": base}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if c.InputBool("draft") {
		body["draft"] = true
	}

	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls", owner, repo))
	return doRequest(c, "POST", url, body)
}

func createOrUpdateFile(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	path := strings.TrimPrefix(strings.TrimSpace(c.Input("path")), "/")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	content := c.Input("content")
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content is required")
	}
	message := strings.TrimSpace(c.Input("message"))
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}
	branch := strings.TrimSpace(c.Input("branch"))
	sha := strings.TrimSpace(c.Input("sha"))

	contentsURL := buildURL(c, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path))

	// Discover the current blob sha when updating an existing file and the
	// caller didn't supply one. Be tolerant: any lookup failure (404, network,
	// decode) just means "treat as a create" and proceed without a sha.
	if sha == "" {
		lookupURL := contentsURL
		if branch != "" {
			lookupURL += "?ref=" + branch
		}
		if existing, lookupErr := doRequest(c, "GET", lookupURL, nil); lookupErr == nil {
			if m, ok := existing.(map[string]any); ok {
				if s, ok := m["sha"].(string); ok {
					sha = s
				}
			}
		}
	}

	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
	}
	if branch != "" {
		body["branch"] = branch
	}
	if sha != "" {
		body["sha"] = sha
	}

	return doRequest(c, "PUT", contentsURL, body)
}

// ── REPO handlers ────────────────────────────────────────────────────

func getRepo(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s", owner, repo)), nil)
}

func listBranches(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	url := buildURL(c, fmt.Sprintf("/repos/%s/%s/branches", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", url, nil)
}

func listCommits(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	params := url.Values{}
	params.Set("per_page", fmt.Sprintf("%d", perPage))
	if sha := strings.TrimSpace(c.Input("sha")); sha != "" {
		params.Set("sha", sha)
	}
	if path := strings.TrimSpace(c.Input("path")); path != "" {
		params.Set("path", path)
	}
	if author := strings.TrimSpace(c.Input("author")); author != "" {
		params.Set("author", author)
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/commits", owner, repo)) + "?" + params.Encode()
	return doRequest(c, "GET", u, nil)
}

func listForks(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/forks", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func createFork(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if org := strings.TrimSpace(c.Input("organization")); org != "" {
		body["organization"] = org
	}
	if name := strings.TrimSpace(c.Input("name")); name != "" {
		body["name"] = name
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/forks", owner, repo))
	return doRequest(c, "POST", u, body)
}

func listStargazers(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/stargazers", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func starRepo(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	u := buildURL(c, fmt.Sprintf("/user/starred/%s/%s", owner, repo))
	if _, err := doRequest(c, "PUT", u, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func unstarRepo(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	u := buildURL(c, fmt.Sprintf("/user/starred/%s/%s", owner, repo))
	if _, err := doRequest(c, "DELETE", u, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── ISSUE handlers ───────────────────────────────────────────────────

func getIssue(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)), nil)
}

func updateIssue(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	body := map[string]any{}
	if title := strings.TrimSpace(c.Input("title")); title != "" {
		body["title"] = title
	}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if state := strings.TrimSpace(c.Input("state")); state != "" {
		body["state"] = state
	}
	if labels := parseCSV(c.Input("labels")); len(labels) > 0 {
		body["labels"] = labels
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number))
	return doRequest(c, "PATCH", u, body)
}

func listIssueComments(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

// ── PULL REQUEST handlers ────────────────────────────────────────────

func getPR(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)), nil)
}

func listPRFiles(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/files", owner, repo, number)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func updatePR(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	body := map[string]any{}
	if title := strings.TrimSpace(c.Input("title")); title != "" {
		body["title"] = title
	}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if state := strings.TrimSpace(c.Input("state")); state != "" {
		body["state"] = state
	}
	if base := strings.TrimSpace(c.Input("base")); base != "" {
		body["base"] = base
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number))
	return doRequest(c, "PATCH", u, body)
}

// ── RELEASE handlers ─────────────────────────────────────────────────

func listReleases(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/releases", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func getLatestRelease(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s/releases/latest", owner, repo)), nil)
}

func getRelease(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	releaseID := c.InputInt("release_id")
	if releaseID == 0 {
		return nil, fmt.Errorf("release_id is required")
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s/releases/%d", owner, repo, releaseID)), nil)
}

func createRelease(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(c.Input("tag_name"))
	if tag == "" {
		return nil, fmt.Errorf("tag_name is required")
	}
	body := map[string]any{"tag_name": tag}
	if name := strings.TrimSpace(c.Input("name")); name != "" {
		body["name"] = name
	}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if target := strings.TrimSpace(c.Input("target_commitish")); target != "" {
		body["target_commitish"] = target
	}
	if c.InputBool("draft") {
		body["draft"] = true
	}
	if c.InputBool("prerelease") {
		body["prerelease"] = true
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/releases", owner, repo))
	return doRequest(c, "POST", u, body)
}

func updateRelease(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	releaseID := c.InputInt("release_id")
	if releaseID == 0 {
		return nil, fmt.Errorf("release_id is required")
	}
	body := map[string]any{}
	if tag := strings.TrimSpace(c.Input("tag_name")); tag != "" {
		body["tag_name"] = tag
	}
	if name := strings.TrimSpace(c.Input("name")); name != "" {
		body["name"] = name
	}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if c.InputBool("draft") {
		body["draft"] = true
	}
	if c.InputBool("prerelease") {
		body["prerelease"] = true
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/releases/%d", owner, repo, releaseID))
	return doRequest(c, "PATCH", u, body)
}

func deleteRelease(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	releaseID := c.InputInt("release_id")
	if releaseID == 0 {
		return nil, fmt.Errorf("release_id is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/releases/%d", owner, repo, releaseID))
	if _, err := doRequest(c, "DELETE", u, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── TAG handlers ─────────────────────────────────────────────────────

func listTags(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/tags", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

// ── USER handlers ────────────────────────────────────────────────────

func getMe(c *connector.Ctx) (any, error) {
	return doRequest(c, "GET", buildURL(c, "/user"), nil)
}

// HealthCheck verifies the configured token by calling GET /user. It
// reports a single OpHealth entry ("auth"): OK on a 2xx response, or
// OK=false with GitHub's error message otherwise. Surfaced in the admin
// UI's "Check Permissions" button when wired into the connector module.
func HealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	if _, err := doRequest(c, "GET", buildURL(c, "/user"), nil); err != nil {
		return []connector.OpHealth{{Key: "auth", OK: false, Reason: err.Error()}}, nil
	}
	return []connector.OpHealth{{Key: "auth", OK: true}}, nil
}
