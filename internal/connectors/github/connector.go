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

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "GitHub",
		Description: "List repos, issues, and PRs; read file contents and PR diffs; create issues, create/merge PRs, edit files, and post comments via the GitHub REST API.",
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
