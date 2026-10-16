// Command bitbucket is the Bitbucket Cloud connector shipped as an external wick
// plugin (a standalone binary the host runs over gRPC). One instance = one
// Atlassian account (email + API token) against a workspace.
package main

import (
	"errors"
	"net/http"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

// Module assembles the full connector definition served by main.go. Mirrors the
// in-tree registry record (Meta + Configs + Operations). DefaultTags come from
// the shared plugins/tags catalog so the plugin files under the same
// "Development" section as when it was a built-in.
func Module() connector.Module {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.Development}
	return connector.Module{
		Meta:       m,
		Configs:    entity.StructToConfigs(Configs{}),
		Operations: Operations(),
	}
}

const Key = "bitbucket"

// Configs is grouped so the page opens short: credentials first, then the
// collapsible sections. "collapsed" is the third pipe segment of group=.
type Configs struct {
	BaseURL          string `wick:"url;required;default=https://api.bitbucket.org/2.0;group=Authentication;desc=Bitbucket Cloud API base URL."`
	Email            string `wick:"email;secret;required;group=Authentication;desc=Atlassian account email used with the API token for Basic Auth."`
	APIToken         string `wick:"secret;required;group=Authentication;desc=Bitbucket Cloud API token. Needs read repository scopes for read ops and write repository scopes for write ops."`
	DefaultWorkspace string `wick:"group=Authentication;desc=Optional default Bitbucket workspace slug used when an operation omits workspace."`
	// PermissionStatus is a read-only widget: the per-operation allowed/denied
	// checklist probed live from the token. Not a stored value — the html op renders it.
	PermissionStatus string `wick:"html=permission_status;group=Permission status|Which operations this token can run.|collapsed;desc=Live checklist of which operations this token can run. Fill the credentials and default_workspace first."`

	// RulesEditor is the only place merge_rules is edited: mode as a dropdown and
	// approvers found by name among the workspace's Bitbucket users.
	RulesEditor string `wick:"html=rules_editor;group=Merge rules|Checked by merge_pull_request before every merge. A repository no row matches merges without checks, so keep a * row as the default.|collapsed;desc=Edit the merge rules. Every button saves."`
	MergeRules  string `wick:"kvlist=repo|mode|min_approvals|required_approvers;hidden;default=[{\"repo\":\"*\",\"mode\":\"allow\",\"min_approvals\":\"1\",\"required_approvers\":\"5efc131e3404690bae882041\",\"approver_names\":\"Taufik Budi S\"},{\"repo\":\"integration-helm\",\"mode\":\"deny\",\"min_approvals\":\"\",\"required_approvers\":\"\"}];desc=Managed by the Merge rules editor. One row per repository. repo: slug or workspace/slug, globs allowed (* matches every repo). The row with the fewest * wins. mode: allow or deny (deny refuses every merge, e.g. integration-helm). min_approvals: approvals the PR needs. required_approvers: comma-separated account_id, {uuid} or nickname. Each one who is on the PR must have approved, someone not on the PR is not required."`
	// RulesTest checks a real pull request against the saved rules, without merging.
	RulesTest string `wick:"html=rules_test_panel;group=Test merge rules|Paste a pull request and see the checklist merge_pull_request would apply.|collapsed;desc=Test a pull request against the saved rules. Nothing is merged."`
	// The test panel's last input, persisted so a re-mount of the widget does not wipe it.
	RulesTestPR string `wick:"hidden;desc=Last pull request tested in the merge rules panel."`

	DefaultPagelen int `wick:"default=20;group=Paging||collapsed;desc=Default Bitbucket page length for list/search operations."`
	MaxPagelen     int `wick:"default=100;group=Paging;desc=Maximum page length allowed by this connector."`
}

// PermissionStatusInput: the manager's html widget always passes the current
// field value as "browser"; it is unused here.
type PermissionStatusInput struct {
	Browser string `wick:"desc=Unused."`
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
	Reviewers         string `wick:"desc=Optional reviewers, comma-separated Bitbucket account_id (e.g. 5efc131e3404690bae882041) or {uuid}. The PR author cannot be a reviewer."`
}

type AddPullRequestReviewersInput struct {
	Workspace     string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug      string `wick:"required;desc=Repository slug."`
	PullRequestID int    `wick:"required;desc=Pull request ID."`
	Reviewers     string `wick:"required;desc=Reviewers to add, comma-separated Bitbucket account_id (e.g. 5efc131e3404690bae882041) or {uuid}. Existing reviewers are kept. The PR author cannot be a reviewer."`
}

type CreatePullRequestCommentInput struct {
	Workspace     string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug      string `wick:"required;desc=Repository slug."`
	PullRequestID int    `wick:"required;desc=Pull request ID."`
	Body          string `wick:"textarea;required;desc=Comment body."`
	InlinePath    string `wick:"key=inline_path;desc=Optional. File path to anchor an inline comment (e.g. src/main.go). Required for any inline comment."`
	InlineTo      int    `wick:"key=inline_to;number;desc=Optional. Line number in the NEW (post-diff) version to anchor the inline comment. Needs inline_path."`
	InlineFrom    int    `wick:"key=inline_from;number;desc=Optional. Line number in the OLD (pre-diff) version, use instead of inline_to to comment on a removed/old line. Needs inline_path."`
}

type RunPipelineInput struct {
	Workspace    string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug     string `wick:"required;desc=Repository slug."`
	Branch       string `wick:"required;desc=Branch to run the pipeline on. Example: main"`
	Pattern      string `wick:"desc=Optional pipeline name to run. With selector_type custom (default) it is a name from the custom: section of bitbucket-pipelines.yml, with branches it is the branch pattern (e.g. staging), with pull-requests it is the PR pattern (e.g. **). Empty runs the default pipeline for the branch."`
	SelectorType string `wick:"key=selector_type;dropdown=custom|branches|pull-requests;desc=Which section of bitbucket-pipelines.yml pattern refers to. Default custom."`
	Variables    string `wick:"textarea;desc=Optional pipeline variables. A JSON object or one KEY=VALUE per line."`
}

type GetPipelineInput struct {
	Workspace    string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug     string `wick:"required;desc=Repository slug."`
	PipelineUUID string `wick:"required;desc=Pipeline UUID or build number from run_pipeline or list_pipelines."`
}

type ListPipelinesInput struct {
	Workspace string `wick:"desc=Bitbucket workspace slug. If empty, uses default_workspace config."`
	RepoSlug  string `wick:"required;desc=Repository slug."`
	Pagelen   int    `wick:"desc=Page size. Defaults to connector default_pagelen."`
	Page      int    `wick:"desc=Page number. Default 1."`
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

func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat(
			"Maintenance",
			"Backs the manager's config widget (permission status); not meant for agent use.",
			connector.OpConfigOnly(
				"rules_editor",
				"Merge Rules Editor",
				"Render the merge rules editor in the config page. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditor,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_save",
				"Save Merge Rules",
				"Save the rules from the merge rules editor. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorSave,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_add_row",
				"Add Merge Rule Row",
				"Add an empty row to the merge rules editor. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorAddRow,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_delete_row",
				"Delete Merge Rule Row",
				"Delete one row from the merge rules and save. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorDeleteRow,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_search",
				"Search Bitbucket Users",
				"Find Bitbucket workspace members by name for the merge rules editor. Read-only. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorSearch,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_add_approver",
				"Add Required Approver",
				"Add a required approver to one merge rule and save. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorAddApprover,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_editor_remove_approver",
				"Remove Required Approver",
				"Remove a required approver from one merge rule and save. Backs the config form only; never called by an agent.",
				RulesEditorInput{},
				rulesEditorRemoveApprover,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_test_panel",
				"Merge Rules Test Panel",
				"Render the merge rules test form in the config page. Backs the config form only; never called by an agent.",
				RulesTestInput{},
				rulesTestPanel,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"rules_test_run",
				"Run Merge Rules Test",
				"Check one pull request against the saved merge_rules and render the checklist. Read-only, never merges. Backs the config form only; never called by an agent.",
				RulesTestInput{},
				rulesTestRun,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"permission_status",
				"Permission Status",
				"Render the allowed/denied checklist of operations for the configured token. Read-only; used by the manager UI's permission-status widget.",
				PermissionStatusInput{},
				permissionStatus,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Repositories",
			"Search and inspect Bitbucket repositories and branches.",
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
			connector.OpDestructive(
				"create_branch",
				"Create Branch",
				"Create a new branch from a branch, tag, or commit hash. Mutates the repository.",
				CreateBranchInput{},
				createBranch,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Commits",
			"List and inspect commits and their diffs.",
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
			connector.OpDestructive(
				"create_file_commit",
				"Create File Commit",
				"Create or update one file on a branch via Bitbucket's source endpoint. Mutates the repository by creating a commit.",
				CreateFileCommitInput{},
				createFileCommit,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Pull Requests",
			"List, review, comment on, and merge pull requests.",
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
				"create_pull_request",
				"Create Pull Request",
				"Create a pull request from source_branch to destination_branch. Mutates the repository.",
				CreatePullRequestInput{},
				createPullRequest,
				wickdocs.Docs{},
			),
			connector.OpDestructive(
				"add_pull_request_reviewers",
				"Add Pull Request Reviewers",
				"Add reviewers to an existing pull request by account_id or {uuid}. Reviewers already on the PR are kept; nobody is removed. Returns the updated pull request.",
				AddPullRequestReviewersInput{},
				addPullRequestReviewers,
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
				"Merge a pull request into its destination branch. Optional merge_strategy (merge_commit, squash, fast_forward), commit message, and close_source_branch. Refused when the connector's merge_rules say so (repo denied, too few approvals, or a required approver on the PR has not approved); the error lists every failed rule. Returns the merged pull request. Irreversible.",
				MergePullRequestInput{},
				mergePullRequest,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Pipelines",
			"Trigger and inspect Bitbucket Pipelines builds.",
			connector.OpDestructive(
				"run_pipeline",
				"Run Pipeline",
				"Trigger a Bitbucket Pipelines build on a branch, optionally a custom pipeline by name and with variables. Returns the pipeline (uuid, build_number, state). Starts a real build.",
				RunPipelineInput{},
				runPipeline,
				wickdocs.Docs{},
			),
			connector.Op(
				"get_pipeline",
				"Get Pipeline",
				"Fetch one pipeline's state and result by UUID or build number.",
				GetPipelineInput{},
				getPipeline,
				wickdocs.Docs{},
			),
			connector.Op(
				"list_pipelines",
				"List Pipelines",
				"List recent pipelines of a repository, newest first.",
				ListPipelinesInput{},
				listPipelines,
				wickdocs.Docs{},
			),
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

// addPullRequestReviewers reads the PR first because Bitbucket's update
// replaces the reviewer list: sending only the new names would drop everyone
// already on it.
func addPullRequestReviewers(c *connector.Ctx) (any, error) {
	add, err := parseReviewers(c.Input("reviewers"))
	if err != nil {
		return nil, err
	}
	if len(add) == 0 {
		return nil, errors.New("reviewers is required")
	}
	get, err := validatePullRequest(c, "pullrequest")
	if err != nil {
		return nil, err
	}
	current, err := fetchJSON(c, get)
	if err != nil {
		return nil, err
	}
	pr, _ := current.(map[string]any)
	body := map[string]any{
		"title":     pr["title"],
		"reviewers": mergeReviewers(pr["reviewers"], add),
	}
	return sendJSON(c, requestParams{Method: http.MethodPut, URL: get.URL}, body)
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

// mergePullRequest checks merge_rules before merging. The rules are read from
// the pull request as it is right now, so an approval withdrawn a minute ago
// counts as withdrawn.
func mergePullRequest(c *connector.Ctx) (any, error) {
	p, body, err := validateMergePullRequest(c)
	if err != nil {
		return nil, err
	}
	workspace, repo, _ := workspaceAndRepo(c)
	v, _, err := checkMergeRules(c, workspace, repo, c.InputInt("pull_request_id"))
	if err != nil {
		return nil, err
	}
	if !v.Allowed {
		return nil, errMergeRefused(v)
	}
	return sendJSON(c, p, body)
}

func runPipeline(c *connector.Ctx) (any, error) {
	p, body, err := validateRunPipeline(c)
	if err != nil {
		return nil, err
	}
	return sendJSON(c, p, body)
}

func getPipeline(c *connector.Ctx) (any, error) {
	p, err := validateGetPipeline(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}

func listPipelines(c *connector.Ctx) (any, error) {
	p, err := validateListPipelines(c)
	if err != nil {
		return nil, err
	}
	return fetchJSON(c, p)
}
