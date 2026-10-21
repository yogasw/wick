// Command gitlab is the GitLab REST API v4 connector shipped as an external
// wick plugin. One instance = one GitLab host (gitlab.com or self-managed)
// plus a personal/project access token.
//
// To wait for an MR merge or a pipeline result, use a wick schedule
// type=watch whose connector step calls get_merge_request / get_pipeline.
//
// File layout:
//
//   - connector.go — Module, Meta, Configs, Input structs, Operations
//   - service.go   — handlers and response shaping
//   - repo.go      — outbound HTTP
package main

import (
	"net/http"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "gitlab"

func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         Key,
			Name:        "GitLab",
			Description: "GitLab REST API v4 (gitlab.com or self-managed): projects, branches, commits, files, merge requests (diff, notes, approve, merge), pipelines and job logs, and issues. Pair get_merge_request / get_pipeline with a schedule watch to be told when an MR merges or a pipeline ends.",
			Icon:        "🦊",
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.Development},
		},
		Configs:     entity.StructToConfigs(Configs{}),
		Operations:  Operations(),
		HealthCheck: HealthCheck,
	}
}

// Configs is the per-instance credential set.
type Configs struct {
	BaseURL        string `wick:"url;desc=GitLab host, e.g. https://gitlab.example.com (no /api/v4). Empty = https://gitlab.com."`
	Token          string `wick:"secret;required;desc=Personal or project access token. Scope api for writes, read_api for read-only use."`
	DefaultProject string `wick:"desc=group/repo path used when an op gets no project. Optional."`
	// PermissionStatus is a read-only widget: the per-operation allowed/denied
	// checklist from the token's scopes and its role in default_project. Not a
	// stored value — the html op renders it.
	PermissionStatus string `wick:"html=permission_status;group=Permission status|Which operations this token can run.|collapsed;desc=Live checklist of which operations this token can run: scopes and expiry of the token, and its role in default_project. Fill token and default_project first."`
}

// PermissionStatusInput: the manager's html widget always passes the current
// field value as "browser"; it is unused here.
type PermissionStatusInput struct {
	Browser string `wick:"desc=Unused."`
	Project string `wick:"desc=Project to check instead of default_project (the panel's test box). Not saved."`
}

// ── inputs ───────────────────────────────────────────────────────────

type NoInput struct{}

type ListProjectsInput struct {
	Search  string `wick:"desc=Filter by name/path."`
	All     bool   `wick:"desc=true = every project visible to the token, not only the ones you are a member of."`
	OrderBy string `wick:"desc=last_activity_at (default) | name | path | created_at | updated_at."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type ProjectInput struct {
	Project string `wick:"desc=Numeric id or group/repo path. Default: default_project."`
}

type ListBranchesInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Search  string `wick:"desc=Filter branch names."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type CreateBranchInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Branch  string `wick:"required;desc=New branch name."`
	Ref     string `wick:"required;desc=Branch, tag or SHA to branch from."`
}

type ListCommitsInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Ref     string `wick:"desc=Branch, tag or SHA. Default: default branch."`
	Path    string `wick:"desc=Only commits touching this path."`
	Since   string `wick:"desc=ISO 8601, e.g. 2026-01-01T00:00:00Z."`
	Until   string `wick:"desc=ISO 8601."`
	Author  string `wick:"desc=Author name or email."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type GetCommitInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Sha     string `wick:"required;desc=Commit SHA, branch or tag."`
}

type CompareInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	From     string `wick:"required;desc=Base ref (branch, tag, SHA)."`
	To       string `wick:"required;desc=Head ref."`
	MaxBytes int    `wick:"desc=Cap on total diff text. 0 = no cap."`
}

type GetFileInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	Path     string `wick:"required;desc=File path in the repo, e.g. src/main.go."`
	Ref      string `wick:"desc=Branch, tag or SHA. Default HEAD."`
	MaxBytes int    `wick:"desc=Keep only the last N bytes. 0 = whole file."`
}

type ListTreeInput struct {
	Project   string `wick:"desc=Numeric id or group/repo path."`
	Path      string `wick:"desc=Folder inside the repo. Default: root."`
	Ref       string `wick:"desc=Branch, tag or SHA."`
	Recursive bool   `wick:"desc=Walk sub-folders."`
	PerPage   int    `wick:"desc=Max 100. Default 20."`
}

type ListTagsInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Search  string `wick:"desc=Filter tag names."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type ListMRsInput struct {
	Project      string `wick:"desc=Numeric id or group/repo path."`
	State        string `wick:"desc=opened (default) | merged | closed | locked | all."`
	TargetBranch string `wick:"desc=Filter by target branch."`
	SourceBranch string `wick:"desc=Filter by source branch."`
	Author       string `wick:"desc=Author username."`
	Search       string `wick:"desc=Search title and description."`
	PerPage      int    `wick:"desc=Max 100. Default 20."`
}

type MRInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	MRIID   int    `wick:"key=mr_iid;required;desc=Merge request number (the !N shown in GitLab)."`
}

type MRChangesInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	MRIID    int    `wick:"key=mr_iid;required;desc=Merge request number."`
	MaxBytes int    `wick:"desc=Cap on total diff text. 0 = no cap."`
}

type CreateMRInput struct {
	Project            string `wick:"desc=Numeric id or group/repo path."`
	SourceBranch       string `wick:"required;desc=Branch with the changes."`
	TargetBranch       string `wick:"required;desc=Branch to merge into."`
	Title              string `wick:"required;desc=MR title."`
	Description        string `wick:"textarea;desc=MR description (Markdown)."`
	Draft              bool   `wick:"desc=Open as draft (prefixes Draft:)."`
	RemoveSourceBranch bool   `wick:"desc=Delete the source branch after merge."`
	Squash             bool   `wick:"desc=Squash commits on merge."`
}

type UpdateMRInput struct {
	Project      string `wick:"desc=Numeric id or group/repo path."`
	MRIID        int    `wick:"key=mr_iid;required;desc=Merge request number."`
	Title        string `wick:"desc=New title."`
	Description  string `wick:"textarea;desc=New description."`
	TargetBranch string `wick:"desc=New target branch."`
	StateEvent   string `wick:"desc=close | reopen."`
}

type MergeMRInput struct {
	Project              string `wick:"desc=Numeric id or group/repo path."`
	MRIID                int    `wick:"key=mr_iid;required;desc=Merge request number."`
	Squash               bool   `wick:"desc=Squash commits."`
	RemoveSourceBranch   bool   `wick:"desc=Delete the source branch."`
	WhenPipelineSucceeds bool   `wick:"desc=Set auto-merge: merge once the pipeline passes."`
	MergeCommitMessage   string `wick:"desc=Custom merge commit message."`
	Sha                  string `wick:"desc=Only merge if the MR head is still this SHA."`
}

type MRNoteInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	MRIID   int    `wick:"key=mr_iid;required;desc=Merge request number."`
	Body    string `wick:"textarea;required;desc=Comment (Markdown)."`
}

type ListMRNotesInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	MRIID   int    `wick:"key=mr_iid;required;desc=Merge request number."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type ListPipelinesInput struct {
	Project string `wick:"desc=Numeric id or group/repo path."`
	Ref     string `wick:"desc=Branch or tag."`
	Status  string `wick:"desc=created | pending | running | success | failed | canceled | skipped | manual."`
	Sha     string `wick:"desc=Commit SHA."`
	PerPage int    `wick:"desc=Max 100. Default 20."`
}

type PipelineInput struct {
	Project    string `wick:"desc=Numeric id or group/repo path."`
	PipelineID int    `wick:"required;desc=Pipeline id."`
}

type PipelineJobsInput struct {
	Project    string `wick:"desc=Numeric id or group/repo path."`
	PipelineID int    `wick:"required;desc=Pipeline id."`
	Scope      string `wick:"desc=Only jobs in this state, e.g. failed."`
}

type JobLogInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	JobID    int    `wick:"required;desc=Job id (from list_pipeline_jobs)."`
	MaxBytes int    `wick:"desc=Keep the last N bytes of the log. Default 20000."`
}

type ListIssuesInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	State    string `wick:"desc=opened (default) | closed | all."`
	Labels   string `wick:"desc=Comma-separated label names."`
	Search   string `wick:"desc=Search title and description."`
	Assignee string `wick:"desc=Assignee username."`
	PerPage  int    `wick:"desc=Max 100. Default 20."`
}

type IssueInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	IssueIID int    `wick:"key=issue_iid;required;desc=Issue number (#N)."`
}

type CreateIssueInput struct {
	Project     string `wick:"desc=Numeric id or group/repo path."`
	Title       string `wick:"required;desc=Issue title."`
	Description string `wick:"textarea;desc=Issue body (Markdown)."`
	Labels      string `wick:"desc=Comma-separated label names."`
}

type IssueNoteInput struct {
	Project  string `wick:"desc=Numeric id or group/repo path."`
	IssueIID int    `wick:"key=issue_iid;required;desc=Issue number."`
	Body     string `wick:"textarea;required;desc=Comment (Markdown)."`
}

// ── operations ───────────────────────────────────────────────────────

func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat("Projects & repository", "Find projects and read branches, commits, files and tags.",
			connector.Op("get_me", "Who Am I", "The user the token authenticates as.", NoInput{}, getMe, wickdocs.Docs{}),
			connector.Op("list_projects", "List Projects", "Projects you are a member of (or all visible with all=true), most recently active first.", ListProjectsInput{}, listProjects,
				wickdocs.Docs{Quirks: []string{"Use path_with_namespace from the result as the project param of every other op."}, InputSample: `{"search":"api"}`}),
			connector.Op("get_project", "Get Project", "One project: default branch, visibility, URL.", ProjectInput{}, getProject, wickdocs.Docs{}),
			connector.Op("list_branches", "List Branches", "Branches with their head commit.", ListBranchesInput{}, listBranches, wickdocs.Docs{}),
			connector.OpDestructive("create_branch", "Create Branch", "Create a branch from a ref.", CreateBranchInput{}, createBranch, wickdocs.Docs{}),
			connector.Op("list_commits", "List Commits", "Commits on a ref, optionally for one path / author / time window.", ListCommitsInput{}, listCommits, wickdocs.Docs{}),
			connector.Op("get_commit", "Get Commit", "One commit with message, parents and stats.", GetCommitInput{}, getCommit, wickdocs.Docs{}),
			connector.Op("compare", "Compare Refs", "Commits and diffs between two refs.", CompareInput{}, compareRefs, wickdocs.Docs{}),
			connector.Op("get_file", "Get File", "Raw content of one file at a ref.", GetFileInput{}, getFile, wickdocs.Docs{}),
			connector.Op("list_tree", "List Tree", "Files and folders at a path.", ListTreeInput{}, listTree, wickdocs.Docs{}),
			connector.Op("list_tags", "List Tags", "Tags, newest first.", ListTagsInput{}, listTags, wickdocs.Docs{}),
		),
		connector.Cat("Merge requests", "Read, open, comment on, approve and merge merge requests.",
			connector.Op("list_merge_requests", "List Merge Requests", "MRs of a project, open ones by default.", ListMRsInput{}, listMRs, wickdocs.Docs{}),
			connector.Op("get_merge_request", "Get Merge Request", "One MR with description, head pipeline, reviewers and merge status.", MRInput{}, getMR,
				wickdocs.Docs{Quirks: []string{"To wait for a merge, use it as the connector step of a wick schedule type=watch with a check rule state == merged (fail_rules state == closed)."},
					InputSample: `{"project":"acme/web","mr_iid":12}`}),
			connector.Op("get_merge_request_changes", "Get MR Changes", "Per-file diffs of an MR.", MRChangesInput{}, getMRChanges,
				wickdocs.Docs{Quirks: []string{"Uses /diffs (GitLab 15.7+) and falls back to /changes on older hosts."}}),
			connector.Op("list_mr_notes", "List MR Comments", "Comments and system notes on an MR, oldest first.", ListMRNotesInput{}, listMRNotes, wickdocs.Docs{}),
			connector.Op("list_mr_pipelines", "List MR Pipelines", "Pipelines that ran for an MR.", MRInput{}, listMRPipelines, wickdocs.Docs{}),
			connector.OpDestructive("create_merge_request", "Create Merge Request", "Open an MR.", CreateMRInput{}, createMR, wickdocs.Docs{}),
			connector.OpDestructive("update_merge_request", "Update Merge Request", "Edit title/description/target, or close/reopen.", UpdateMRInput{}, updateMR, wickdocs.Docs{}),
			connector.OpDestructive("add_mr_note", "Comment on MR", "Post a comment on an MR.", MRNoteInput{}, addMRNote, wickdocs.Docs{}),
			connector.OpDestructive("approve_merge_request", "Approve MR", "Approve an MR as the token's user.", MRInput{}, approveMR, wickdocs.Docs{}),
			connector.OpDestructive("merge_merge_request", "Merge MR", "Merge an MR now, or set auto-merge with when_pipeline_succeeds.", MergeMRInput{}, mergeMR,
				wickdocs.Docs{Quirks: []string{"405/406 from GitLab = not mergeable yet (conflicts, pipeline, approvals). Read detailed_merge_status from get_merge_request."}}),
		),
		connector.Cat("Pipelines", "CI/CD pipelines, jobs and job logs.",
			connector.Op("list_pipelines", "List Pipelines", "Pipelines of a project, newest first.", ListPipelinesInput{}, listPipelines, wickdocs.Docs{}),
			connector.Op("get_pipeline", "Get Pipeline", "One pipeline with status and duration.", PipelineInput{}, getPipeline, wickdocs.Docs{}),
			connector.Op("list_pipeline_jobs", "List Pipeline Jobs", "Jobs of a pipeline with status and failure reason.", PipelineJobsInput{}, listPipelineJobs, wickdocs.Docs{}),
			connector.Op("get_job_log", "Get Job Log", "Tail of a job's log.", JobLogInput{}, getJobLog, wickdocs.Docs{}),
			connector.OpDestructive("retry_pipeline", "Retry Pipeline", "Retry the failed jobs of a pipeline.", PipelineInput{}, pipelineAction("retry"), wickdocs.Docs{}),
			connector.OpDestructive("cancel_pipeline", "Cancel Pipeline", "Cancel a running pipeline.", PipelineInput{}, pipelineAction("cancel"), wickdocs.Docs{}),
		),
		connector.Cat("Maintenance", "Backs the manager's config widget (permission status); not meant for agent use.",
			connector.OpConfigOnly("permission_status", "Permission Status",
				"Render the allowed/denied checklist of operations for the configured token. Read-only; used by the manager UI's permission-status widget.",
				PermissionStatusInput{}, permissionStatus, wickdocs.Docs{}),
		),
		connector.Cat("Issues", "Project issues and their comments.",
			connector.Op("list_issues", "List Issues", "Issues of a project, open ones by default.", ListIssuesInput{}, listIssues, wickdocs.Docs{}),
			connector.Op("get_issue", "Get Issue", "One issue with description.", IssueInput{}, getIssue, wickdocs.Docs{}),
			connector.OpDestructive("create_issue", "Create Issue", "Open an issue.", CreateIssueInput{}, createIssue, wickdocs.Docs{}),
			connector.OpDestructive("add_issue_note", "Comment on Issue", "Post a comment on an issue.", IssueNoteInput{}, addIssueNote, wickdocs.Docs{}),
		),
	}
}

func HealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	if _, err := doJSON(c, http.MethodGet, apiURL(c, "/user", nil), nil); err != nil {
		return []connector.OpHealth{{Key: "auth", OK: false, Reason: err.Error()}}, nil
	}
	return []connector.OpHealth{{Key: "auth", OK: true}}, nil
}
