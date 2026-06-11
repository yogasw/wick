package bitbucket

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "bitbucket"

type Configs struct {
	BaseURL          string `wick:"url;required;default=https://api.bitbucket.org/2.0;desc=Bitbucket Cloud API base URL."`
	Email            string `wick:"email;secret;required;desc=Atlassian account email used with the API token for Basic Auth."`
	APIToken         string `wick:"secret;required;desc=Bitbucket Cloud API token. Needs read repository scopes for read ops and write repository scopes for write ops."`
	DefaultWorkspace string `wick:"desc=Optional default Bitbucket workspace slug used when an operation omits workspace."`
	DefaultPagelen   int    `wick:"default=20;desc=Default Bitbucket page length for list/search operations."`
	MaxPagelen       int    `wick:"default=100;desc=Maximum page length allowed by this connector."`
}

type SearchRepositoriesInput struct {
	Workspace  string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	Query      string `wick:"desc=Plain text search against repository name, slug, and description."`
	ProjectKey string `wick:"desc=Optional Bitbucket project key filter. Example: SUP"`
	IsPrivate  string `wick:"dropdown=all|true|false;default=all;desc=Filter by repository visibility."`
	Sort       string `wick:"dropdown=-updated_on|updated_on|name|-name;default=-updated_on;desc=Sort order."`
	Pagelen    int    `wick:"desc=Page size. Defaults to connector default_pagelen."`
	Page       int    `wick:"desc=Page number. Default 1."`
}

type GetRepositoryInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug. Example: support-tools"`
}

type ListCommitsInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug."`
	Revision  string `wick:"desc=Optional branch, tag, or commit hash to list from."`
	Path      string `wick:"desc=Optional path filter."`
	Pagelen   int    `wick:"desc=Page size. Defaults to connector default_pagelen."`
	Page      int    `wick:"desc=Page number. Default 1."`
}

type CommitInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug."`
	Commit    string `wick:"required;desc=Commit hash or ref."`
}

type ListPullRequestsInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug."`
	State     string `wick:"dropdown=all|OPEN|MERGED|DECLINED|SUPERSEDED;default=OPEN;desc=Pull request state filter."`
	Query     string `wick:"desc=Optional Bitbucket q filter. Example: title~\"bug\""`
	Pagelen   int    `wick:"desc=Page size. Defaults to connector default_pagelen."`
	Page      int    `wick:"desc=Page number. Default 1."`
}

type PullRequestInput struct {
	Workspace     string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug      string `wick:"required;desc=Repository slug."`
	PullRequestID int    `wick:"required;desc=Pull request ID."`
}

type CreateBranchInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug."`
	Name      string `wick:"required;desc=New branch name."`
	Target    string `wick:"required;desc=Source branch, tag, or commit hash."`
}

type CreateFileCommitInput struct {
	Workspace     string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug      string `wick:"required;desc=Repository slug."`
	Branch        string `wick:"required;desc=Target branch name."`
	Path          string `wick:"required;desc=File path to create or update."`
	Content       string `wick:"textarea;required;desc=Full file content to commit."`
	CommitMessage string `wick:"required;desc=Commit message."`
}

type CreatePullRequestInput struct {
	Workspace         string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug          string `wick:"required;desc=Repository slug."`
	Title             string `wick:"required;desc=Pull request title."`
	Description       string `wick:"textarea;desc=Pull request description."`
	SourceBranch      string `wick:"required;desc=Source branch name."`
	DestinationBranch string `wick:"required;desc=Destination branch name. Example: main"`
	CloseSourceBranch bool   `wick:"default=false;desc=Close source branch after merge."`
}

type CreatePullRequestCommentInput struct {
	Workspace     string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug      string `wick:"required;desc=Repository slug."`
	PullRequestID int    `wick:"required;desc=Pull request ID."`
	Body          string `wick:"textarea;required;desc=Comment body."`
	InlinePath    string `wick:"key=inline_path;desc=Optional. File path to anchor an inline comment (e.g. src/main.go). Required for any inline comment."`
	InlineTo      int    `wick:"key=inline_to;number;desc=Optional. Line number in the NEW (post-diff) version to anchor the inline comment. Needs inline_path."`
	InlineFrom    int    `wick:"key=inline_from;number;desc=Optional. Line number in the OLD (pre-diff) version; use instead of inline_to to comment on a removed/old line. Needs inline_path."`
}

type MergePullRequestInput struct {
	Workspace         string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug          string `wick:"required;desc=Repository slug."`
	PullRequestID     int    `wick:"required;desc=Pull request ID."`
	MergeStrategy     string `wick:"key=merge_strategy;dropdown=merge_commit|squash|fast_forward;default=merge_commit;desc=Merge strategy. If omitted, Bitbucket repository default applies."`
	Message           string `wick:"textarea;desc=Optional merge commit message. Defaults to Bitbucket's generated message."`
	CloseSourceBranch bool   `wick:"key=close_source_branch;default=false;desc=Close the source branch after a successful merge."`
}

func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Bitbucket",
		Description: "Search repositories, inspect commits and pull requests, and create branch/commit/PR changes in Bitbucket Cloud.",
		Icon:        "BB",
	}
}

func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"search_repositories",
			"Search Repositories",
			"Search repositories in a Bitbucket workspace. Returns a paginated list with name, slug, full_name, project, visibility, main branch, updated_on, and html_url.",
			SearchRepositoriesInput{},
			searchRepositories,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_repository",
			"Get Repository",
			"Fetch one Bitbucket repository by workspace and repo slug.",
			GetRepositoryInput{},
			getRepository,
			wickdocs.Docs{},
		),
		connector.Op(
			"list_commits",
			"List Commits",
			"List commits in a repository, optionally scoped to a branch/tag/hash revision and path.",
			ListCommitsInput{},
			listCommits,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_commit",
			"Get Commit",
			"Fetch one commit by hash or ref.",
			CommitInput{},
			getCommit,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_commit_diff",
			"Get Commit Diff",
			"Fetch the unified diff for a commit hash or ref. Returns text diff content.",
			CommitInput{},
			getCommitDiff,
			wickdocs.Docs{},
		),
		connector.Op(
			"list_pull_requests",
			"List Pull Requests",
			"List pull requests in a repository. Use this to find Bitbucket PRs by state or q filter.",
			ListPullRequestsInput{},
			listPullRequests,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_pull_request",
			"Get Pull Request",
			"Fetch one pull request by ID.",
			PullRequestInput{},
			getPullRequest,
			wickdocs.Docs{},
		),
		connector.Op(
			"list_pull_request_commits",
			"List Pull Request Commits",
			"List commits attached to one pull request.",
			PullRequestInput{},
			listPullRequestCommits,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"create_branch",
			"Create Branch",
			"Create a new branch from a branch, tag, or commit hash. Mutates the repository.",
			CreateBranchInput{},
			createBranch,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"create_file_commit",
			"Create File Commit",
			"Create or update one file on a branch via Bitbucket's source endpoint. Mutates the repository by creating a commit.",
			CreateFileCommitInput{},
			createFileCommit,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"create_pull_request",
			"Create Pull Request",
			"Create a pull request from source_branch to destination_branch. Mutates the repository.",
			CreatePullRequestInput{},
			createPullRequest,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"create_pull_request_comment",
			"Create Pull Request Comment",
			"Post a comment to a pull request — top-level, or inline on a file via inline_path plus inline_to (new-side line) or inline_from (old-side line). Mutates the PR discussion.",
			CreatePullRequestCommentInput{},
			createPullRequestComment,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"approve_pull_request",
			"Approve Pull Request",
			"Approve a pull request as the authenticated user. Returns the participant approval state. Idempotent — approving an already-approved PR is a no-op.",
			PullRequestInput{},
			approvePullRequest,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"request_changes_pull_request",
			"Request Changes on Pull Request",
			"Flag a pull request as needing changes (request-changes) as the authenticated user. Returns the participant state. Mutually exclusive with approve.",
			PullRequestInput{},
			requestChangesPullRequest,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"merge_pull_request",
			"Merge Pull Request",
			"Merge a pull request into its destination branch. Optional merge_strategy (merge_commit, squash, fast_forward), commit message, and close_source_branch. Returns the merged pull request. Irreversible.",
			MergePullRequestInput{},
			mergePullRequest,
			wickdocs.Docs{},
		),
	}
}

func searchRepositories(c *connector.Ctx) (any, error) {
	p, err := validateSearchRepositories(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func getRepository(c *connector.Ctx) (any, error) {
	p, err := validateGetRepository(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func listCommits(c *connector.Ctx) (any, error) {
	p, err := validateListCommits(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func getCommit(c *connector.Ctx) (any, error) {
	p, err := validateCommit(c, "commit")
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func getCommitDiff(c *connector.Ctx) (any, error) {
	p, err := validateCommit(c, "diff")
	if err != nil {
		return nil, err
	}
	return fetchText(c, p)
}

func listPullRequests(c *connector.Ctx) (any, error) {
	p, err := validateListPullRequests(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func getPullRequest(c *connector.Ctx) (any, error) {
	p, err := validatePullRequest(c, "pullrequest")
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func listPullRequestCommits(c *connector.Ctx) (any, error) {
	p, err := validatePullRequest(c, "commits")
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func createBranch(c *connector.Ctx) (any, error) {
	p, body, err := validateCreateBranch(c)
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, body)
}

func createFileCommit(c *connector.Ctx) (any, error) {
	p, form, err := validateCreateFileCommit(c)
	if err != nil {
		return nil, err
	}
	return sendMultipart(c, p, form)
}

func createPullRequest(c *connector.Ctx) (any, error) {
	p, body, err := validateCreatePullRequest(c)
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, body)
}

func createPullRequestComment(c *connector.Ctx) (any, error) {
	p, body, err := validateCreatePullRequestComment(c)
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, body)
}

func approvePullRequest(c *connector.Ctx) (any, error) {
	p, err := validatePullRequestAction(c, "approve")
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, map[string]any{})
}

func requestChangesPullRequest(c *connector.Ctx) (any, error) {
	p, err := validatePullRequestAction(c, "request-changes")
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, map[string]any{})
}

func mergePullRequest(c *connector.Ctx) (any, error) {
	p, body, err := validateMergePullRequest(c)
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, body)
}
