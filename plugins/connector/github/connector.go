// Command github is the GitHub REST API v3 connector shipped as an external
// wick plugin (a standalone binary the host runs over gRPC).
// One instance = one GitHub account or organisation (token + optional
// custom base URL for GitHub Enterprise). Operations cover the most
// common LLM-driven workflows: listing repos/issues/PRs, reading file
// contents, creating issues, and posting comments.
//
// File layout:
//
//   - connector.go — Module, Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — URL construction, input validation, response shaping
//   - repo.go      — outbound HTTP via http.NewRequestWithContext
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

// Module assembles the full connector definition served by main.go. Mirrors the
// in-tree registry record: Meta + Configs + Operations + HealthCheck. DefaultTags
// come from the shared plugins/tags catalog so the plugin files under the same
// "Development" section as when it was a built-in.
func Module() connector.Module {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.Development}
	return connector.Module{
		Meta:        m,
		Configs:     entity.StructToConfigs(Configs{}),
		Operations:  Operations(),
		HealthCheck: HealthCheck,
	}
}

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

// ── COMMENT inputs ───────────────────────────────────────────────────

// UpdateCommentInput edits an existing issue/PR comment.
type UpdateCommentInput struct {
	Owner     string `wick:"required;desc=Repository owner."`
	Repo      string `wick:"required;desc=Repository name."`
	CommentID int    `wick:"required;desc=Comment ID (numeric, from list_issue_comments)."`
	Body      string `wick:"textarea;required;desc=New comment body (Markdown supported)."`
}

// DeleteCommentInput removes an issue/PR comment.
type DeleteCommentInput struct {
	Owner     string `wick:"required;desc=Repository owner."`
	Repo      string `wick:"required;desc=Repository name."`
	CommentID int    `wick:"required;desc=Comment ID to delete."`
}

// ── PR REVIEW inputs ─────────────────────────────────────────────────

// CreateReviewInput submits a formal review on a pull request.
type CreateReviewInput struct {
	Owner    string `wick:"required;desc=Repository owner."`
	Repo     string `wick:"required;desc=Repository name."`
	Number   int    `wick:"required;desc=PR number."`
	Event    string `wick:"desc=APPROVE | REQUEST_CHANGES | COMMENT. Default COMMENT."`
	Body     string `wick:"textarea;desc=Review body (Markdown). Required by GitHub when event is REQUEST_CHANGES or COMMENT."`
	CommitID string `wick:"desc=SHA the review applies to. Default: the PR's latest commit."`
}

// ListReviewsInput lists the reviews on a pull request.
type ListReviewsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Number  int    `wick:"required;desc=PR number."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// CreateReviewCommentInput posts an inline diff comment on a pull request.
type CreateReviewCommentInput struct {
	Owner    string `wick:"required;desc=Repository owner."`
	Repo     string `wick:"required;desc=Repository name."`
	Number   int    `wick:"required;desc=PR number."`
	Body     string `wick:"textarea;required;desc=Comment body (Markdown supported)."`
	CommitID string `wick:"required;desc=SHA of the commit to comment on (usually the PR head)."`
	Path     string `wick:"required;desc=File path the comment applies to."`
	Line     int    `wick:"required;desc=Line number in the file's diff to attach the comment to."`
	Side     string `wick:"desc=LEFT | RIGHT (which side of the diff). Default RIGHT."`
}

// ListReviewCommentsInput lists inline review comments on a pull request.
type ListReviewCommentsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	Number  int    `wick:"required;desc=PR number."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// RequestReviewersInput requests reviewers on a pull request.
type RequestReviewersInput struct {
	Owner         string `wick:"required;desc=Repository owner."`
	Repo          string `wick:"required;desc=Repository name."`
	Number        int    `wick:"required;desc=PR number."`
	Reviewers     string `wick:"desc=Comma-separated GitHub logins to request review from."`
	TeamReviewers string `wick:"desc=Comma-separated team slugs to request review from."`
}

// ── BRANCH / REF inputs ──────────────────────────────────────────────

// CreateBranchInput creates a new branch from another branch or SHA.
type CreateBranchInput struct {
	Owner      string `wick:"required;desc=Repository owner."`
	Repo       string `wick:"required;desc=Repository name."`
	Branch     string `wick:"required;desc=Name of the new branch."`
	FromBranch string `wick:"desc=Branch to branch off. Default: the repo's default branch."`
	Sha        string `wick:"desc=Commit SHA to point the new branch at. Overrides from_branch when set."`
}

// DeleteRefInput deletes a branch ref.
type DeleteRefInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Branch string `wick:"required;desc=Branch name to delete."`
}

// ── LABEL / ASSIGNEE inputs ──────────────────────────────────────────

// AddLabelsInput adds labels to an issue or PR.
type AddLabelsInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=Issue or PR number."`
	Labels string `wick:"required;desc=Comma-separated label names to add (additive)."`
}

// RemoveLabelInput removes a single label from an issue or PR.
type RemoveLabelInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Number int    `wick:"required;desc=Issue or PR number."`
	Name   string `wick:"required;desc=Label name to remove."`
}

// AddAssigneesInput assigns users to an issue or PR.
type AddAssigneesInput struct {
	Owner     string `wick:"required;desc=Repository owner."`
	Repo      string `wick:"required;desc=Repository name."`
	Number    int    `wick:"required;desc=Issue or PR number."`
	Assignees string `wick:"required;desc=Comma-separated GitHub logins to assign."`
}

// ── COMMIT / COMPARE inputs ──────────────────────────────────────────

// GetCommitInput fetches a single commit.
type GetCommitInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	Sha   string `wick:"required;desc=Commit SHA (or branch/tag name)."`
}

// CompareCommitsInput compares two commits/branches.
type CompareCommitsInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	Base  string `wick:"required;desc=Base ref (branch, tag, or SHA)."`
	Head  string `wick:"required;desc=Head ref (branch, tag, or SHA) to compare against base."`
}

// ── SEARCH inputs (not repo-scoped) ──────────────────────────────────

// SearchIssuesInput searches issues and pull requests.
type SearchIssuesInput struct {
	Q       string `wick:"required;desc=GitHub search query. Example: repo:abc/web is:open is:issue label:bug"`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// SearchReposInput searches repositories.
type SearchReposInput struct {
	Q       string `wick:"required;desc=GitHub search query. Example: language:go stars:>100"`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// SearchCodeInput searches code.
type SearchCodeInput struct {
	Q       string `wick:"required;desc=GitHub code search query. Example: addClass in:file language:js repo:abc/web"`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ── COLLABORATOR / REPO-MGMT inputs ──────────────────────────────────

// ListCollaboratorsInput lists repository collaborators.
type ListCollaboratorsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// CreateRepoInput creates a repository for the user or an org.
type CreateRepoInput struct {
	Name        string `wick:"required;desc=Repository name."`
	Description string `wick:"desc=Short repo description."`
	Private     bool   `wick:"desc=Create as private. Default: false (public)."`
	AutoInit    bool   `wick:"desc=Initialise with an empty README. Default: false."`
	Org         string `wick:"desc=Organisation to create the repo under. Default: the authenticated user."`
}

// UpdateRepoInput edits repository settings.
type UpdateRepoInput struct {
	Owner         string `wick:"required;desc=Repository owner."`
	Repo          string `wick:"required;desc=Repository name."`
	Name          string `wick:"desc=New repo name. Omit to leave unchanged."`
	Description   string `wick:"desc=New description. Omit to leave unchanged."`
	Private       bool   `wick:"desc=Set visibility private/public. Sent only when the input is present."`
	DefaultBranch string `wick:"desc=New default branch. Omit to leave unchanged."`
	Archived      bool   `wick:"desc=Archive/unarchive. Sent only when the input is present."`
}

// ── ACTIONS inputs ───────────────────────────────────────────────────

// ListWorkflowsInput lists GitHub Actions workflows.
type ListWorkflowsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// ListWorkflowRunsInput lists GitHub Actions workflow runs.
type ListWorkflowRunsInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// DispatchWorkflowInput triggers a workflow_dispatch event.
type DispatchWorkflowInput struct {
	Owner      string `wick:"required;desc=Repository owner."`
	Repo       string `wick:"required;desc=Repository name."`
	WorkflowID string `wick:"required;desc=Numeric workflow ID or filename (e.g. ci.yml)."`
	Ref        string `wick:"required;desc=Git ref (branch or tag) to run the workflow on."`
	Inputs     string `wick:"textarea;desc=Optional JSON object of workflow inputs. Example: {\"env\":\"prod\"}"`
}

// ── WEBHOOK inputs ───────────────────────────────────────────────────

// ListHooksInput lists repository webhooks.
type ListHooksInput struct {
	Owner   string `wick:"required;desc=Repository owner."`
	Repo    string `wick:"required;desc=Repository name."`
	PerPage int    `wick:"desc=Results per page, max 100. Default: 30."`
}

// CreateHookInput creates a repository webhook.
type CreateHookInput struct {
	Owner       string `wick:"required;desc=Repository owner."`
	Repo        string `wick:"required;desc=Repository name."`
	URL         string `wick:"required;desc=Payload URL the hook POSTs to."`
	Events      string `wick:"desc=Comma-separated events to subscribe to. Default: push."`
	Secret      string `wick:"secret;desc=Optional secret used to sign hook payloads."`
	ContentType string `wick:"desc=json | form. Default: json."`
}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "GitHub",
		Description: "Comprehensive GitHub REST API connector: repos, issues, PRs (incl. diff/merge), releases, tags, forks, stars, file contents/edits, the authenticated user, and a token health check.",
		Icon:        "🐙",
	}
}

// Operations returns the LLM-callable actions for this connector, grouped
// into human-facing categories for the admin UI.
func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat(
			"Common Actions",
			"The everyday GitHub operations: list repos/issues/PRs, read files, comment, and open/merge/commit changes.",
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
		),

		// ── REPO ─────────────────────────────────────────────────────
		connector.Cat(
			"Repositories",
			"Inspect a repo and its branches, commits, forks, and stargazers; star/unstar repos.",
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
		),

		// ── ISSUES ───────────────────────────────────────────────────
		connector.Cat(
			"Issues",
			"Read and edit issues and their comment threads.",
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
		),

		// ── PULL REQUESTS ────────────────────────────────────────────
		connector.Cat(
			"Pull Requests",
			"Fetch pull requests, list their changed files, and edit PR metadata.",
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
		),

		// ── RELEASES ─────────────────────────────────────────────────
		connector.Cat(
			"Releases",
			"List, fetch, publish, edit, and delete repository releases.",
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
		),

		// ── TAGS ─────────────────────────────────────────────────────
		connector.Cat(
			"Tags",
			"List the tags in a repository.",
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
		),

		// ── USER ─────────────────────────────────────────────────────
		connector.Cat(
			"User",
			"Identify the authenticated user and probe token health.",
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
		),

		// ── COMMENTS ─────────────────────────────────────────────────
		connector.Cat(
			"Comments",
			"Edit and delete existing issue or PR conversation comments.",
			connector.OpDestructive(
				"update_comment",
				"Update Comment",
				"Edit an existing issue or PR comment by its ID.",
				UpdateCommentInput{},
				updateComment,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"id":       "Comment ID.",
						"body":     "Updated comment body.",
						"html_url": "Web URL of the comment.",
					},
					Quirks: []string{
						"comment_id is the numeric comment ID (from list_issue_comments), NOT the issue number.",
						"Works for issue and PR conversation comments (shared endpoint).",
					},
					PairWith:    []string{"connector:github.list_issue_comments", "connector:github.delete_comment"},
					InputSample: `{"owner":"abc","repo":"web","comment_id":123,"body":"edited"}`,
				},
			),
			connector.OpDestructive(
				"delete_comment",
				"Delete Comment",
				"Delete an issue or PR comment by its ID.",
				DeleteCommentInput{},
				deleteComment,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"ok": "true when GitHub returned 204 No Content.",
					},
					Quirks: []string{
						"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
						"comment_id is the numeric comment ID, not the issue number.",
					},
					PairWith:    []string{"connector:github.list_issue_comments"},
					InputSample: `{"owner":"abc","repo":"web","comment_id":123}`,
				},
			),
		),

		// ── PULL REQUEST REVIEWS ─────────────────────────────────────
		connector.Cat(
			"Pull Request Reviews",
			"Submit and list formal reviews, post inline diff comments, and request reviewers.",
			connector.OpDestructive(
				"create_review",
				"Create PR Review",
				"Submit a formal review (approve, request changes, or comment) on a pull request.",
				CreateReviewInput{},
				createReview,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"id":    "Review ID.",
						"state": "APPROVED | CHANGES_REQUESTED | COMMENTED.",
					},
					Quirks: []string{
						"event defaults to COMMENT; use APPROVE or REQUEST_CHANGES for a verdict.",
						"GitHub 422s on a COMMENT/REQUEST_CHANGES review with an empty body.",
						"commit_id pins the review to a specific SHA; omit to use the PR head.",
					},
					PairWith:    []string{"connector:github.get_pr_diff", "connector:github.list_reviews", "connector:github.create_review_comment"},
					InputSample: `{"owner":"abc","repo":"web","number":7,"event":"APPROVE","body":"LGTM"}`,
				},
			),
			connector.Op(
				"list_reviews",
				"List PR Reviews",
				"List the reviews submitted on a pull request.",
				ListReviewsInput{},
				listReviews,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"reviews": "Array of {id, user.login, state, body, submitted_at}.",
					},
					Quirks: []string{
						"state is APPROVED | CHANGES_REQUESTED | COMMENTED | PENDING | DISMISSED.",
						"Pagination: PerPage max 100; first page only.",
					},
					PairWith:    []string{"connector:github.create_review", "connector:github.list_review_comments"},
					InputSample: `{"owner":"abc","repo":"web","number":7,"per_page":30}`,
				},
			),
			connector.OpDestructive(
				"create_review_comment",
				"Create PR Review Comment",
				"Post an inline comment on a specific line of a pull request's diff.",
				CreateReviewCommentInput{},
				createReviewComment,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"id":       "Review comment ID.",
						"path":     "File path the comment is attached to.",
						"html_url": "Web URL of the inline comment.",
					},
					Quirks: []string{
						"This is an INLINE diff comment (different from add_comment, which posts to the conversation).",
						"commit_id, path, and line are required and must match a line present in the diff.",
						"side selects LEFT (old) or RIGHT (new) of the diff; default RIGHT.",
					},
					PairWith:    []string{"connector:github.get_pr_diff", "connector:github.list_review_comments", "connector:github.create_review"},
					InputSample: `{"owner":"abc","repo":"web","number":7,"body":"nit: rename","commit_id":"abc123","path":"main.go","line":10,"side":"RIGHT"}`,
				},
			),
			connector.Op(
				"list_review_comments",
				"List PR Review Comments",
				"List the inline diff comments on a pull request.",
				ListReviewCommentsInput{},
				listReviewComments,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"comments": "Array of {id, user.login, body, path, line, commit_id, html_url}.",
					},
					Quirks: []string{
						"These are INLINE diff comments, not the conversation comments from list_issue_comments.",
						"Pagination: PerPage max 100; first page only.",
					},
					PairWith:    []string{"connector:github.create_review_comment"},
					InputSample: `{"owner":"abc","repo":"web","number":7,"per_page":30}`,
				},
			),
			connector.OpDestructive(
				"request_reviewers",
				"Request PR Reviewers",
				"Request one or more users or teams to review a pull request.",
				RequestReviewersInput{},
				requestReviewers,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"requested_reviewers": "Array of users now requested.",
						"requested_teams":     "Array of teams now requested.",
					},
					Quirks: []string{
						"reviewers and team_reviewers are comma-separated; supply at least one.",
						"reviewers are GitHub logins; team_reviewers are team slugs (org repos only).",
						"GitHub 422s if you request a reviewer who is the PR author.",
					},
					PairWith:    []string{"connector:github.create_review", "connector:github.get_pr"},
					InputSample: `{"owner":"abc","repo":"web","number":7,"reviewers":"yoga,riska"}`,
				},
			),
		),

		// ── BRANCHES / REFS ──────────────────────────────────────────
		connector.Cat(
			"Branches",
			"Create and delete branch refs.",
			connector.OpDestructive(
				"create_branch",
				"Create Branch",
				"Create a new branch from another branch's head (or an explicit SHA).",
				CreateBranchInput{},
				createBranch,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"ref":        "Full ref of the new branch (refs/heads/<branch>).",
						"object.sha": "SHA the new branch points at.",
					},
					Quirks: []string{
						"Pass sha to branch off a specific commit; otherwise from_branch's head is used.",
						"from_branch defaults to the repo's default branch (resolved via GET /repos/{o}/{r}).",
						"GitHub 422s if the branch already exists.",
					},
					PairWith:    []string{"connector:github.create_or_update_file", "connector:github.create_pr", "connector:github.delete_ref"},
					InputSample: `{"owner":"abc","repo":"web","branch":"feature/x","from_branch":"main"}`,
				},
			),
			connector.OpDestructive(
				"delete_ref",
				"Delete Branch",
				"Delete a branch ref from a repository.",
				DeleteRefInput{},
				deleteRef,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"ok": "true when GitHub returned 204 No Content.",
					},
					Quirks: []string{
						"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
						"Deletes refs/heads/{branch}; cannot delete a protected branch.",
					},
					PairWith:    []string{"connector:github.create_branch", "connector:github.list_branches"},
					InputSample: `{"owner":"abc","repo":"web","branch":"feature/x"}`,
				},
			),
		),

		// ── LABELS / ASSIGNEES ───────────────────────────────────────
		connector.Cat(
			"Labels & Assignees",
			"Add or remove labels and assign users on issues and PRs.",
			connector.OpDestructive(
				"add_labels",
				"Add Labels",
				"Add labels to an issue or PR (additive — does not replace existing labels).",
				AddLabelsInput{},
				addLabels,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"labels": "Array of all labels on the issue after adding.",
					},
					Quirks: []string{
						"Additive — unlike update_issue's labels which replaces the whole set.",
						"labels is comma-separated; unknown labels are created/ignored by GitHub.",
					},
					PairWith:    []string{"connector:github.remove_label", "connector:github.update_issue"},
					InputSample: `{"owner":"abc","repo":"web","number":42,"labels":"bug,priority:high"}`,
				},
			),
			connector.OpDestructive(
				"remove_label",
				"Remove Label",
				"Remove a single label from an issue or PR.",
				RemoveLabelInput{},
				removeLabel,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"labels": "Array of remaining labels on the issue.",
					},
					Quirks: []string{
						"Removes exactly one label by name; GitHub 404s if the label isn't applied.",
						"name is the label text (e.g. \"bug\"), URL-escaped by the connector.",
					},
					PairWith:    []string{"connector:github.add_labels"},
					InputSample: `{"owner":"abc","repo":"web","number":42,"name":"bug"}`,
				},
			),
			connector.OpDestructive(
				"add_assignees",
				"Add Assignees",
				"Assign one or more users to an issue or PR.",
				AddAssigneesInput{},
				addAssignees,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"assignees": "Array of users now assigned to the issue.",
					},
					Quirks: []string{
						"assignees is comma-separated GitHub logins; only users with repo access can be assigned.",
						"Additive — leaves existing assignees in place.",
					},
					PairWith:    []string{"connector:github.update_issue", "connector:github.add_labels"},
					InputSample: `{"owner":"abc","repo":"web","number":42,"assignees":"yoga,riska"}`,
				},
			),
		),

		// ── COMMITS / COMPARE ────────────────────────────────────────
		connector.Cat(
			"Commits",
			"Fetch a single commit and compare two commits or branches.",
			connector.Op(
				"get_commit",
				"Get Commit",
				"Fetch a single commit with its message, author, and changed files.",
				GetCommitInput{},
				getCommit,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"sha":            "Commit SHA.",
						"commit.message": "Commit message.",
						"files":          "Array of changed files with patch/additions/deletions.",
					},
					Quirks: []string{
						"sha may be a full/short SHA or a branch/tag name.",
					},
					PairWith:    []string{"connector:github.list_commits", "connector:github.compare_commits"},
					InputSample: `{"owner":"abc","repo":"web","sha":"main"}`,
				},
			),
			connector.Op(
				"compare_commits",
				"Compare Commits",
				"Compare two commits or branches (base...head). Returns the diff stats and commits between them.",
				CompareCommitsInput{},
				compareCommits,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"status":   "ahead | behind | identical | diverged.",
						"ahead_by": "How many commits head is ahead of base.",
						"commits":  "Array of commits between base and head.",
						"files":    "Array of changed files.",
					},
					Quirks: []string{
						"Compares base...head (three-dot, merge-base diff), matching GitHub's compare view.",
						"base/head can be branches, tags, or SHAs; cross-fork uses owner:branch.",
					},
					PairWith:    []string{"connector:github.get_commit", "connector:github.list_commits"},
					InputSample: `{"owner":"abc","repo":"web","base":"main","head":"feature/x"}`,
				},
			),
		),

		// ── SEARCH ───────────────────────────────────────────────────
		connector.Cat(
			"Search",
			"Search issues/PRs, repositories, and code across GitHub.",
			connector.Op(
				"search_issues",
				"Search Issues",
				"Search issues and pull requests across GitHub using the search query syntax.",
				SearchIssuesInput{},
				searchIssues,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"total_count": "Total matches (may exceed the returned page).",
						"items":       "Array of matching issues/PRs.",
					},
					Quirks: []string{
						"q uses GitHub search syntax (qualifiers like repo:, is:open, label:, author:).",
						"Both issues and PRs are returned; add is:issue or is:pr to filter.",
						"Search is rate-limited separately and returns the first page only here.",
					},
					PairWith:    []string{"connector:github.get_issue", "connector:github.get_pr"},
					InputSample: `{"q":"repo:abc/web is:open is:issue label:bug","per_page":30}`,
				},
			),
			connector.Op(
				"search_repos",
				"Search Repositories",
				"Search repositories across GitHub using the search query syntax.",
				SearchReposInput{},
				searchRepos,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"total_count": "Total matches.",
						"items":       "Array of matching repositories.",
					},
					Quirks: []string{
						"q uses GitHub search syntax (language:, stars:>, topic:, org:).",
						"First page only; sort/order qualifiers go in q.",
					},
					PairWith:    []string{"connector:github.get_repo"},
					InputSample: `{"q":"language:go stars:>100","per_page":30}`,
				},
			),
			connector.Op(
				"search_code",
				"Search Code",
				"Search code across GitHub using the code search query syntax.",
				SearchCodeInput{},
				searchCode,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"total_count": "Total matches.",
						"items":       "Array of matching files (name, path, repository).",
					},
					Quirks: []string{
						"Code search needs a qualifier scoping the search (e.g. repo:, org:, user:).",
						"q uses code search syntax (in:file, language:, filename:); first page only.",
					},
					PairWith:    []string{"connector:github.get_file"},
					InputSample: `{"q":"addClass in:file language:js repo:abc/web","per_page":30}`,
				},
			),
		),

		// ── COLLABORATORS / REPO MANAGEMENT ──────────────────────────
		connector.Cat(
			"Repository Management",
			"List collaborators and create or edit repository settings.",
			connector.Op(
				"list_collaborators",
				"List Collaborators",
				"List the collaborators on a repository.",
				ListCollaboratorsInput{},
				listCollaborators,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"collaborators": "Array of users (login, permissions, html_url).",
					},
					Quirks: []string{
						"Requires push access to the repo to see the full collaborator list.",
						"Pagination: PerPage max 100; first page only.",
					},
					PairWith:    []string{"connector:github.add_assignees", "connector:github.request_reviewers"},
					InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
				},
			),
			connector.OpDestructive(
				"create_repo",
				"Create Repository",
				"Create a new repository for the authenticated user, or under an organisation.",
				CreateRepoInput{},
				createRepo,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"full_name": "owner/name of the new repo.",
						"html_url":  "Web URL of the new repo.",
					},
					Quirks: []string{
						"Set org to create under an organisation (POST /orgs/{org}/repos); otherwise it's created for the token's user.",
						"auto_init=true seeds an empty README so the repo has a default branch.",
						"PAT scope: repo (or fine-grained Administration: write).",
					},
					PairWith:    []string{"connector:github.update_repo", "connector:github.create_or_update_file"},
					InputSample: `{"name":"web","description":"My app","private":true,"auto_init":true}`,
				},
			),
			connector.OpDestructive(
				"update_repo",
				"Update Repository",
				"Edit repository settings (name, description, visibility, default branch, archived). Only provided fields change.",
				UpdateRepoInput{},
				updateRepo,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"full_name": "owner/name (reflects rename).",
						"private":   "Resulting visibility.",
						"archived":  "Resulting archive state.",
					},
					Quirks: []string{
						"Only fields you supply are sent; private/archived are sent only when their input key is non-empty.",
						"archived=true archives the repo (read-only); set false to unarchive.",
						"PAT scope: repo (or fine-grained Administration: write).",
					},
					PairWith:    []string{"connector:github.get_repo"},
					InputSample: `{"owner":"abc","repo":"web","description":"Updated","default_branch":"main"}`,
				},
			),
		),

		// ── ACTIONS ──────────────────────────────────────────────────
		connector.Cat(
			"Actions",
			"List GitHub Actions workflows and runs, and trigger workflow dispatches.",
			connector.Op(
				"list_workflows",
				"List Workflows",
				"List the GitHub Actions workflows defined in a repository.",
				ListWorkflowsInput{},
				listWorkflows,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"total_count": "Number of workflows.",
						"workflows":   "Array of {id, name, path, state}.",
					},
					Quirks: []string{
						"id and path (filename) can both be used as workflow_id in dispatch_workflow.",
					},
					PairWith:    []string{"connector:github.dispatch_workflow", "connector:github.list_workflow_runs"},
					InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
				},
			),
			connector.Op(
				"list_workflow_runs",
				"List Workflow Runs",
				"List recent GitHub Actions workflow runs in a repository.",
				ListWorkflowRunsInput{},
				listWorkflowRuns,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"total_count":   "Number of runs.",
						"workflow_runs": "Array of {id, name, status, conclusion, head_branch, html_url}.",
					},
					Quirks: []string{
						"status is queued | in_progress | completed; conclusion holds the result when completed.",
						"Pagination: PerPage max 100; first page only.",
					},
					PairWith:    []string{"connector:github.list_workflows", "connector:github.dispatch_workflow"},
					InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
				},
			),
			connector.OpDestructive(
				"dispatch_workflow",
				"Dispatch Workflow",
				"Trigger a workflow_dispatch event to run a GitHub Actions workflow on a ref.",
				DispatchWorkflowInput{},
				dispatchWorkflow,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"ok": "true when GitHub returned 204 No Content.",
					},
					Quirks: []string{
						"GitHub returns 204 with no body on success; the connector reports {\"ok\":true}.",
						"workflow_id can be the numeric ID or the filename (e.g. ci.yml).",
						"The workflow must declare an on: workflow_dispatch trigger or GitHub 404s.",
						"inputs is an optional JSON object string; invalid JSON is ignored.",
					},
					PairWith:    []string{"connector:github.list_workflows", "connector:github.list_workflow_runs"},
					InputSample: `{"owner":"abc","repo":"web","workflow_id":"ci.yml","ref":"main","inputs":"{\"env\":\"prod\"}"}`,
				},
			),
		),

		// ── WEBHOOKS ─────────────────────────────────────────────────
		connector.Cat(
			"Webhooks",
			"List and create repository webhooks.",
			connector.Op(
				"list_hooks",
				"List Webhooks",
				"List the webhooks configured on a repository.",
				ListHooksInput{},
				listHooks,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"hooks": "Array of {id, name, active, events, config.url}.",
					},
					Quirks: []string{
						"Requires admin access to the repo.",
					},
					PairWith:    []string{"connector:github.create_hook"},
					InputSample: `{"owner":"abc","repo":"web","per_page":30}`,
				},
			),
			connector.OpDestructive(
				"create_hook",
				"Create Webhook",
				"Create a repository webhook that POSTs events to a payload URL.",
				CreateHookInput{},
				createHook,
				wickdocs.Docs{
					OutputShape: map[string]string{
						"id":     "Webhook ID.",
						"config": "The stored config (url, content_type).",
						"active": "Whether the hook is active.",
					},
					Quirks: []string{
						"events defaults to [\"push\"]; pass a comma-separated list to subscribe to more.",
						"content_type is json (default) or form; secret signs the payloads (X-Hub-Signature-256).",
						"Requires admin access to the repo.",
					},
					PairWith:    []string{"connector:github.list_hooks"},
					InputSample: `{"owner":"abc","repo":"web","url":"https://example.com/hook","events":"push,pull_request"}`,
				},
			),
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

// ── COMMENT handlers ─────────────────────────────────────────────────

func updateComment(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	commentID := c.InputInt("comment_id")
	if commentID == 0 {
		return nil, fmt.Errorf("comment_id is required")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, commentID))
	return doRequest(c, "PATCH", u, map[string]any{"body": body})
}

func deleteComment(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	commentID := c.InputInt("comment_id")
	if commentID == 0 {
		return nil, fmt.Errorf("comment_id is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, commentID))
	if _, err := doRequest(c, "DELETE", u, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── PULL REQUEST REVIEW handlers ─────────────────────────────────────

func createReview(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	body := map[string]any{"event": firstNonEmpty(c.Input("event"), "COMMENT")}
	if text := strings.TrimSpace(c.Input("body")); text != "" {
		body["body"] = text
	}
	if commit := strings.TrimSpace(c.Input("commit_id")); commit != "" {
		body["commit_id"] = commit
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, number))
	return doRequest(c, "POST", u, body)
}

func listReviews(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, number)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func createReviewComment(c *connector.Ctx) (any, error) {
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
	commit := strings.TrimSpace(c.Input("commit_id"))
	if commit == "" {
		return nil, fmt.Errorf("commit_id is required")
	}
	path := strings.TrimSpace(c.Input("path"))
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	line := c.InputInt("line")
	if line == 0 {
		return nil, fmt.Errorf("line is required")
	}
	payload := map[string]any{
		"body":      body,
		"commit_id": commit,
		"path":      path,
		"line":      line,
		"side":      firstNonEmpty(c.Input("side"), "RIGHT"),
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", owner, repo, number))
	return doRequest(c, "POST", u, payload)
}

func listReviewComments(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", owner, repo, number)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func requestReviewers(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	body := map[string]any{}
	if reviewers := parseCSV(c.Input("reviewers")); len(reviewers) > 0 {
		body["reviewers"] = reviewers
	}
	if teams := parseCSV(c.Input("team_reviewers")); len(teams) > 0 {
		body["team_reviewers"] = teams
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number))
	return doRequest(c, "POST", u, body)
}

// ── BRANCH / REF handlers ────────────────────────────────────────────

func createBranch(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(c.Input("branch"))
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}

	sha := strings.TrimSpace(c.Input("sha"))
	if sha == "" {
		fromBranch := strings.TrimSpace(c.Input("from_branch"))
		if fromBranch == "" {
			// Resolve the repo's default branch.
			repoInfo, repoErr := doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s", owner, repo)), nil)
			if repoErr != nil {
				return nil, repoErr
			}
			if m, ok := repoInfo.(map[string]any); ok {
				if db, ok := m["default_branch"].(string); ok {
					fromBranch = db
				}
			}
			if fromBranch == "" {
				return nil, fmt.Errorf("could not resolve default branch")
			}
		}
		refURL := buildURL(c, fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, repo, fromBranch))
		ref, refErr := doRequest(c, "GET", refURL, nil)
		if refErr != nil {
			return nil, refErr
		}
		if m, ok := ref.(map[string]any); ok {
			if obj, ok := m["object"].(map[string]any); ok {
				if s, ok := obj["sha"].(string); ok {
					sha = s
				}
			}
		}
		if sha == "" {
			return nil, fmt.Errorf("could not resolve head sha for from_branch %q", fromBranch)
		}
	}

	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo))
	return doRequest(c, "POST", u, map[string]any{
		"ref": "refs/heads/" + branch,
		"sha": sha,
	})
}

func deleteRef(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(c.Input("branch"))
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, branch))
	if _, err := doRequest(c, "DELETE", u, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── LABEL / ASSIGNEE handlers ────────────────────────────────────────

func addLabels(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	labels := parseCSV(c.Input("labels"))
	if len(labels) == 0 {
		return nil, fmt.Errorf("labels is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, number))
	return doRequest(c, "POST", u, map[string]any{"labels": labels})
}

func removeLabel(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", owner, repo, number, url.PathEscape(name)))
	return doRequest(c, "DELETE", u, nil)
}

func addAssignees(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	number := c.InputInt("number")
	if number == 0 {
		return nil, fmt.Errorf("number is required")
	}
	assignees := parseCSV(c.Input("assignees"))
	if len(assignees) == 0 {
		return nil, fmt.Errorf("assignees is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", owner, repo, number))
	return doRequest(c, "POST", u, map[string]any{"assignees": assignees})
}

// ── COMMIT / COMPARE handlers ────────────────────────────────────────

func getCommit(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(c.Input("sha"))
	if sha == "" {
		return nil, fmt.Errorf("sha is required")
	}
	return doRequest(c, "GET", buildURL(c, fmt.Sprintf("/repos/%s/%s/commits/%s", owner, repo, sha)), nil)
}

func compareCommits(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(c.Input("base"))
	if base == "" {
		return nil, fmt.Errorf("base is required")
	}
	head := strings.TrimSpace(c.Input("head"))
	if head == "" {
		return nil, fmt.Errorf("head is required")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/compare/%s...%s", owner, repo, base, head))
	return doRequest(c, "GET", u, nil)
}

// ── SEARCH handlers ──────────────────────────────────────────────────

func searchIssues(c *connector.Ctx) (any, error) {
	return doSearch(c, "/search/issues")
}

func searchRepos(c *connector.Ctx) (any, error) {
	return doSearch(c, "/search/repositories")
}

func searchCode(c *connector.Ctx) (any, error) {
	return doSearch(c, "/search/code")
}

func doSearch(c *connector.Ctx, path string) (any, error) {
	q := strings.TrimSpace(c.Input("q"))
	if q == "" {
		return nil, fmt.Errorf("q is required")
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, path) + fmt.Sprintf("?q=%s&per_page=%d", url.QueryEscape(q), perPage)
	return doRequest(c, "GET", u, nil)
}

// ── COLLABORATOR / REPO-MGMT handlers ────────────────────────────────

func listCollaborators(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/collaborators", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func createRepo(c *connector.Ctx) (any, error) {
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	body := map[string]any{"name": name}
	if desc := strings.TrimSpace(c.Input("description")); desc != "" {
		body["description"] = desc
	}
	if c.InputBool("private") {
		body["private"] = true
	}
	if c.InputBool("auto_init") {
		body["auto_init"] = true
	}

	path := "/user/repos"
	if org := strings.TrimSpace(c.Input("org")); org != "" {
		path = fmt.Sprintf("/orgs/%s/repos", org)
	}
	return doRequest(c, "POST", buildURL(c, path), body)
}

func updateRepo(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if name := strings.TrimSpace(c.Input("name")); name != "" {
		body["name"] = name
	}
	if desc := strings.TrimSpace(c.Input("description")); desc != "" {
		body["description"] = desc
	}
	if strings.TrimSpace(c.Input("private")) != "" {
		body["private"] = c.InputBool("private")
	}
	if db := strings.TrimSpace(c.Input("default_branch")); db != "" {
		body["default_branch"] = db
	}
	if strings.TrimSpace(c.Input("archived")) != "" {
		body["archived"] = c.InputBool("archived")
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s", owner, repo))
	return doRequest(c, "PATCH", u, body)
}

// ── ACTIONS handlers ─────────────────────────────────────────────────

func listWorkflows(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/actions/workflows", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func listWorkflowRuns(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/actions/runs", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func dispatchWorkflow(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	workflowID := strings.TrimSpace(c.Input("workflow_id"))
	if workflowID == "" {
		return nil, fmt.Errorf("workflow_id is required")
	}
	ref := strings.TrimSpace(c.Input("ref"))
	if ref == "" {
		return nil, fmt.Errorf("ref is required")
	}
	body := map[string]any{"ref": ref}
	if raw := strings.TrimSpace(c.Input("inputs")); raw != "" {
		var inputs map[string]any
		if err := json.Unmarshal([]byte(raw), &inputs); err == nil && len(inputs) > 0 {
			body["inputs"] = inputs
		}
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, workflowID))
	if _, err := doRequest(c, "POST", u, body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ── WEBHOOK handlers ─────────────────────────────────────────────────

func listHooks(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	perPage := firstNonZero(c.InputInt("per_page"), 30)
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/hooks", owner, repo)) +
		fmt.Sprintf("?per_page=%d", perPage)
	return doRequest(c, "GET", u, nil)
}

func createHook(c *connector.Ctx) (any, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	hookURL := strings.TrimSpace(c.Input("url"))
	if hookURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	events := parseCSV(c.Input("events"))
	if len(events) == 0 {
		events = []string{"push"}
	}
	config := map[string]any{
		"url":          hookURL,
		"content_type": firstNonEmpty(c.Input("content_type"), "json"),
	}
	if secret := strings.TrimSpace(c.Input("secret")); secret != "" {
		config["secret"] = secret
	}
	body := map[string]any{
		"name":   "web",
		"active": true,
		"events": events,
		"config": config,
	}
	u := buildURL(c, fmt.Sprintf("/repos/%s/%s/hooks", owner, repo))
	return doRequest(c, "POST", u, body)
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
