// Command git is the git CLI connector shipped as an external wick plugin.
//
// It wraps the local git binary rather than a hosting API, so GitHub, Bitbucket,
// GitLab and self-hosted servers all work through the same operations. A policy
// engine gates every mutation: branch naming rules, protected branches, force
// push, and an allow-list for the raw escape hatch.
//
// Credentials never touch disk. HTTPS tokens reach git through an askpass helper
// (this same binary, re-invoked with --askpass) and the plugin never rewrites
// .git/config.
//
// File layout:
//
//   - connector.go — Module, Meta, Config, Input structs, Operations, handlers
//   - service.go   — config → typed policy/auth/runner values
//   - policy.go    — policy resolution and evaluation (pure)
//   - remote.go    — remote URL parsing, credential stripping, SSH→HTTPS
//   - git.go       — process runner: argv, env, timeout, output caps
//   - policyui.go  — Policy Manager config widget
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "git"

// Config is the per-instance setup: one identity, one credential, one policy set.
type Config struct {
	// First field, and in a group of its own. Groups render in the order their
	// fields are declared, so this puts the guide at the top — read before filling
	// anything in — and being alone in its group gives it the full page width for
	// its two-column layout instead of half a row beside a text input.
	// "collapsed" is the THIRD PIPE SEGMENT of group=, not a separate semicolon
	// flag: group=Title|Description|collapsed. Written as ";collapsed;" it is
	// silently ignored — the group value simply ends at the semicolon.
	SetupGuide string `wick:"html=setup_guide;group=Setting up credentials|Pick your git host to see the exact username, token type and permissions it needs|collapsed;desc=Reference only. Nothing here is saved."`

	AuthorName  string `wick:"group=Identity||collapsed;desc=Name used for commits made through this connector. Example: Deploy Bot"`
	AuthorEmail string `wick:"group=Identity;desc=Email used for commits. Example: bot@example.com"`

	Username   string `wick:"group=Authentication||collapsed;desc=HTTPS username, paired with the token below. GitHub: use the literal x-access-token (GitHub only checks the token). Bitbucket Cloud with an App Password: your Bitbucket USERNAME, not your email — find it under Personal settings, Account settings, Username. Bitbucket Cloud with an API token: use the literal x-bitbucket-api-token-auth. Bitbucket Server or Data Center: your username. GitLab: use the literal oauth2, or your GitLab username. Pick your host in the guide above to see the exact value."`
	Token      string `wick:"secret;group=Authentication;desc=Personal access token, App Password, or API token. Passed to git through an askpass helper and never written to disk. Scopes — GitHub classic: repo (private) or public_repo. GitHub fine-grained: contents:write, or contents:read for read-only. Bitbucket Cloud App Password: read:repository:bitbucket plus write:repository:bitbucket to push. Bitbucket Server HTTP access token: PROJECT_READ and REPO_READ, plus REPO_WRITE to push. GitLab: read_repository plus write_repository to push. Pick your host in the guide above for the exact list."`
	AuthMethod string `wick:"dropdown=askpass|credential_helper|extraheader;default=askpass;group=Authentication;desc=How the token reaches git. askpass keeps it out of the process list and is the safest. extraheader makes the token visible to anyone who can list processes."`

	ConvertSSHRemoteToHTTPS bool   `wick:"bool;default=true;group=Remote||collapsed;desc=Rewrite SSH remotes to HTTPS for network operations. The repository's .git/config is never modified."`
	RemoteHostMap           string `wick:"kvlist=ssh_host|https_host;group=Remote;desc=Map SSH hosts to HTTPS hosts for self-hosted servers. Leave empty for GitHub, GitLab and Bitbucket."`

	BranchNamePattern string `wick:"group=Branch Policy||collapsed;desc=Regular expression a new branch name must match. Example: ^(fix|feat|chore)/[a-z0-9._-]+$"`
	ProtectedBranches string `wick:"kvlist=branch;group=Branch Policy;desc=Protected branches. Direct pushes and commits are blocked. Globs allowed, for example release/*"`
	AllowForcePush    bool   `wick:"bool;group=Branch Policy;desc=Allow --force and --force-with-lease. Off by default."`

	RepoPolicies  string `wick:"hidden;desc=Per-repo policy rows, managed by the Policy Rules widget."`
	PolicyManager string `wick:"html=policy_manager;group=Policy Rules|Per-repo overrides and a simulator to test them before relying on them.|collapsed;desc=Edit and test per-repo policy rules."`

	RawEnabled bool   `wick:"bool;group=Raw Operation||collapsed;desc=Enable the raw operation, which runs an arbitrary git subcommand. Off by default."`
	RawRules   string `wick:"kvlist=subcommand|mode;group=Raw Operation;desc=Per-subcommand rules for raw. mode is allow or deny. A subcommand that is not listed is denied."`

	AllowHooks            bool `wick:"bool;group=Runtime||collapsed;desc=Let repository hooks in .git/hooks run. Off by default, because a hook is arbitrary code from the repository."`
	TimeoutSeconds        int  `wick:"default=60;group=Runtime;desc=Timeout in seconds for operations that do not touch the network."`
	NetworkTimeoutSeconds int  `wick:"default=180;group=Runtime;desc=Timeout in seconds for push, pull, fetch, clone and ls-remote."`
	MaxOutputBytes        int  `wick:"default=262144;group=Runtime;desc=Maximum bytes of output returned. Larger output is truncated and flagged."`

	// Last, in its own group: a check is the thing you do once everything above is
	// filled in, and it needs the full width for its results.
	TestPanel string `wick:"html=test_panel;group=Test against a repository|Point the connector at a real repository and confirm the credential, remote and policy all work.|collapsed;desc=Diagnostics only. Nothing here is saved."`

	// The three inputs the test panel renders are persisted as hidden config rows.
	//
	// They have to be, because the widget's markup is not durable state: the
	// manager re-mounts an html= widget whenever the surrounding form re-renders,
	// and a re-mount re-runs the render op — which used to come back as an empty
	// form and silently wipe whatever had been typed. Storing the values means a
	// re-mount restores them instead of losing them, and the operator's last target
	// is still there next time the page is opened.
	TestRepo   string `wick:"hidden;desc=Last repository tested, kept so the diagnostics form survives a re-render."`
	TestRemote string `wick:"hidden;desc=Last remote tested."`
	TestBranch string `wick:"hidden;desc=Last branch tested."`
}

// Meta identifies the connector. Key must equal the folder name.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Git CLI",
		Description: "Run git against local repositories with policy guards on branch names, protected branches and force pushes. Wraps the git binary, so it works with any host.",
		Icon:        "🌿",
	}
}

// ---------------------------------------------------------------------------
// Read operations
// ---------------------------------------------------------------------------

// StatusInput reports the working tree state.
type StatusInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository. Must contain a .git directory. Example: /srv/code/api"`
}

// LogInput reads commit history. Limit keeps the response small; raise it only
// when a wider window is genuinely needed.
type LogInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"desc=Branch, tag or commit to read. Default: the checked-out branch."`
	Limit    int    `wick:"default=20;desc=Maximum commits to return. Default 20."`
	Path     string `wick:"desc=Limit history to this file or directory, relative to the repository root."`
	Since    string `wick:"desc=Only commits after this date. Accepts anything git understands, for example 2026-01-01 or 2 weeks ago."`
}

// DiffInput compares two refs. StatOnly returns just the file summary, which is
// usually enough and far smaller than a full patch.
type DiffInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	RefA     string `wick:"desc=Left side of the comparison. Default: HEAD."`
	RefB     string `wick:"desc=Right side. Leave empty to diff the working tree against RefA."`
	Path     string `wick:"desc=Limit the diff to this file or directory."`
	StatOnly bool   `wick:"bool;desc=Return only the changed-file summary instead of the full patch."`
	MaxLines int    `wick:"default=500;desc=Maximum patch lines to return. Output beyond this is cut and flagged as truncated. Default 500."`
}

// BranchListInput lists branches.
type BranchListInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   bool   `wick:"bool;desc=List remote-tracking branches instead of local ones."`
	Pattern  string `wick:"desc=Only branches matching this glob. Example: fix/*"`
}

// ShowInput reads one commit.
type ShowInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"required;desc=Commit, tag or branch to show. Example: HEAD or a1b2c3d"`
}

// RemoteListInput lists remotes with the URL each operation would actually use.
type RemoteListInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
}

// LsRemoteInput probes a remote without changing anything — the cheapest way to
// verify that credentials and the remote URL both work.
type LsRemoteInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
}

func doStatus(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "status", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		return []string{"status", "--porcelain=v2", "--branch"}, nil
	}, false)
}

func doLog(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "log", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		limit := c.InputInt("limit")
		if limit <= 0 {
			limit = 20
		}
		args := []string{"log", "--format=%H%x09%an%x09%aI%x09%s", "-n", strconv.Itoa(limit)}
		if since := strings.TrimSpace(c.Input("since")); since != "" {
			args = append(args, "--since="+since)
		}
		// Everything after --end-of-options is a ref or a path, never a flag. Without
		// it a ref of "--all" or "-p" would be read by git as an option and quietly
		// change what the operation does — ValidateUserArgs only knows the specific
		// flags that are dangerous, not every flag that is unintended.
		args = append(args, "--end-of-options")
		if ref := strings.TrimSpace(c.Input("ref")); ref != "" {
			args = append(args, ref)
		}
		if p := strings.TrimSpace(c.Input("path")); p != "" {
			args = append(args, "--", p)
		}
		return args, nil
	}, false)
}

func doDiff(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	statOnly := c.InputBool("stat_only")
	maxLines := c.InputInt("max_lines")
	if maxLines <= 0 {
		maxLines = 500
	}

	out, err := execute(c, "diff", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		args := []string{"diff"}
		if statOnly {
			args = append(args, "--stat")
		} else {
			args = append(args, "--unified=3")
		}
		// See doLog: a ref must never be parsed as an option.
		args = append(args, "--end-of-options")
		args = append(args, firstNonEmpty(strings.TrimSpace(c.Input("ref_a")), "HEAD"))
		if refB := strings.TrimSpace(c.Input("ref_b")); refB != "" {
			args = append(args, refB)
		}
		if p := strings.TrimSpace(c.Input("path")); p != "" {
			args = append(args, "--", p)
		}
		return args, nil
	}, false)
	if err != nil {
		return nil, err
	}
	if statOnly {
		return out, nil
	}
	// git has no "stop after N patch lines" flag, so the cap is applied here. The
	// input promises it, and a 40k-line patch would blow the agent's context even
	// though it is well inside max_output_bytes.
	return capEnvelopeLines(out, maxLines), nil
}

// capEnvelopeLines truncates an envelope's stdout to max lines, flagging the cut
// so a caller never mistakes a partial patch for the whole one.
func capEnvelopeLines(env any, max int) any {
	m, ok := env.(map[string]any)
	if !ok || max <= 0 {
		return env
	}
	body, ok := m["stdout"].(string)
	if !ok {
		return env
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= max {
		return env
	}
	m["stdout"] = strings.Join(lines[:max], "\n")
	m["truncated"] = true
	return m
}

func doBranchList(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "branch_list", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		args := []string{"branch", "--format=%(refname:short)%09%(objectname:short)%09%(committerdate:iso8601)"}
		if c.InputBool("remote") {
			args = append(args, "--remotes")
		}
		if pat := strings.TrimSpace(c.Input("pattern")); pat != "" {
			// --list takes the glob as a following token, so end options first: a
			// pattern of "--edit-description" would otherwise run a different command.
			args = append(args, "--list", "--end-of-options", pat)
		}
		return args, nil
	}, false)
}

func doShow(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "show", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		ref := strings.TrimSpace(c.Input("ref"))
		if ref == "" {
			return nil, errors.New("ref is required")
		}
		return []string{"show", "--stat", "--end-of-options", ref}, nil
	}, false)
}

func doRemoteList(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	res, err := Run(c.Context(),
		Cmd{RepoPath: repo, UserArgs: []string{"remote", "-v"}}, runOpts(c, false))
	if err != nil {
		return nil, err
	}

	// Report the URL each network operation would actually use, not just what is
	// configured — that difference is exactly what surprises people.
	hostMap := ParseHostMap(c.Cfg("remote_host_map"))
	convert := c.CfgBool("convert_ssh_remote_to_https")
	remotes := make([]map[string]any, 0, 4)
	for _, name := range remoteNames(res.Stdout) {
		raw := remoteURL(c, repo, name)
		entry := map[string]any{"name": name, "configured": StripCredentials(raw)}
		if info, cerr := ConvertRemote(raw, hostMap, convert); cerr == nil {
			entry["effective"] = info.Effective
			entry["converted"] = info.Converted
		} else {
			entry["error"] = cerr.Error()
		}
		remotes = append(remotes, entry)
	}
	return map[string]any{"ok": res.OK, "remotes": remotes, "stderr": res.Stderr}, nil
}

func doLsRemote(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "ls_remote"),
		UserArgs:     []string{"ls-remote", "--heads", "--end-of-options", info.Effective},
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, Verdict{Allow: true, MatchedRule: "n/a (read-only)"}, &info), nil
}

// remoteNames extracts unique remote names from `git remote -v` output.
func remoteNames(out string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		names = append(names, fields[0])
	}
	return names
}

// ---------------------------------------------------------------------------
// Mutating and destructive operations
//
// Every user value that lands in a positional slot is preceded by
// --end-of-options. Values consumed by a value-taking flag (-b, -m, --branch)
// are already safe: git binds the next token to that flag, so a flag-shaped
// value arrives as data. Verified against git 2.52 — "checkout -b --orphan"
// reports "not a valid branch name" rather than acting on --orphan, and
// "commit -m --amend" records a commit whose subject is "--amend".
//
// Two subcommands reject the terminator in the obvious position and are handled
// differently, both verified against git 2.52:
//
//	checkout -b   "checkout -b --end-of-options NAME" consumes the terminator as
//	              the branch name. The guard is unnecessary there anyway (-b
//	              protects its own value) so it is placed at the end of the argv,
//	              where it still guards nothing user-supplied but stays harmless.
//	tag -a        "tag -a --end-of-options NAME -m MSG" is "fatal: too many
//	              arguments". -m alone already implies an annotated tag, so -a is
//	              dropped and the terminator sits after -m's value.
// ---------------------------------------------------------------------------

// BranchCreateInput creates a branch. The name is checked against the policy's
// branch pattern and must not be a protected branch.
type BranchCreateInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=New branch name. Must satisfy the connector's branch pattern. Example: fix/login-timeout"`
	FromRef  string `wick:"desc=Base commit or branch. Default: the current HEAD."`
	Checkout bool   `wick:"bool;desc=Switch to the new branch after creating it."`
}

// CheckoutInput switches refs, optionally creating the branch.
type CheckoutInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"required;desc=Branch, tag or commit to switch to."`
	Create   bool   `wick:"bool;desc=Create the branch if it does not exist. The branch pattern then applies."`
}

// AddInput stages paths.
type AddInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Paths    string `wick:"required;desc=Comma-separated paths to stage, relative to the repository root. Use . to stage everything."`
}

// CommitInput records staged changes. The current branch must not be protected.
type CommitInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Message  string `wick:"required;textarea;desc=Commit message."`
	All      bool   `wick:"bool;desc=Stage every tracked modified file before committing."`
	DryRun   bool   `wick:"bool;desc=Evaluate the policy and assemble the command without running it."`
}

// StashInput saves or restores work in progress. Dropping a stash is a separate
// operation because it cannot be undone.
type StashInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Action   string `wick:"required;dropdown=push|pop|list;desc=push saves the working tree, pop restores the most recent entry, list shows entries."`
	Message  string `wick:"desc=Label for the stash entry. Used with push."`
}

// FetchInput updates remote-tracking refs.
type FetchInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
	Prune    bool   `wick:"bool;desc=Delete remote-tracking refs whose upstream branch is gone."`
}

// PullInput fetches and integrates.
type PullInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
	Branch   string `wick:"desc=Branch to pull. Default: the current branch's upstream."`
	Rebase   bool   `wick:"bool;desc=Rebase local commits onto the fetched head instead of merging."`
}

// TagInput creates a tag.
type TagInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=Tag name. Example: v1.2.0"`
	Ref      string `wick:"desc=Commit to tag. Default: HEAD."`
	Message  string `wick:"desc=Annotation message. Supplying it creates an annotated tag instead of a lightweight one."`
}

// PushInput publishes commits. The target branch must not be protected, and
// force requires the policy to allow it.
type PushInput struct {
	RepoPath    string `wick:"required;desc=Absolute path to the repository."`
	Remote      string `wick:"default=origin;desc=Remote name whose URL is used. Default: origin."`
	Branch      string `wick:"desc=Target branch. Default: the current branch."`
	Force       bool   `wick:"bool;desc=Force push using --force-with-lease. Requires allow_force_push in the policy."`
	SetUpstream bool   `wick:"bool;desc=Record the remote branch as the upstream of the current branch."`
	DryRun      bool   `wick:"bool;desc=Evaluate the policy and assemble the command without running it."`
}

// MergeInput integrates another ref into the current branch.
type MergeInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"desc=Branch or commit to merge in. Required unless {abort} is set."`
	NoFF     bool   `wick:"bool;desc=Always create a merge commit, even when a fast-forward is possible."`
	Abort    bool   `wick:"bool;desc=Abort a merge that stopped on conflicts and restore the previous state."`
}

// ResetInput moves HEAD. A hard reset discards work, so it requires the same
// opt-in as a force push.
type ResetInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Mode     string `wick:"required;dropdown=soft|mixed|hard;desc=soft keeps the index and working tree, mixed resets the index, hard discards all local changes. hard requires allow_force_push."`
	Ref      string `wick:"required;desc=Commit to reset to. Example: HEAD~1"`
}

// RebaseInput replays commits. Never interactive — an editor would hang.
type RebaseInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Onto     string `wick:"desc=Branch or commit to rebase onto. Required unless {abort} or {continue_} is set."`
	Abort    bool   `wick:"bool;desc=Abort an in-progress rebase and restore the previous state."`
	Continue bool   `wick:"key=continue_;bool;desc=Continue a rebase after conflicts were resolved and staged."`
}

// CloneInput copies a remote repository to disk.
type CloneInput struct {
	URL    string `wick:"required;desc=Repository URL. An SSH URL is converted to HTTPS when the connector allows it."`
	Dest   string `wick:"required;desc=Absolute destination directory. Must not already exist."`
	Branch string `wick:"desc=Branch to check out. Default: the remote's default branch."`
	Depth  int    `wick:"desc=Create a shallow clone with this many commits of history. Omit for a full clone."`
}

// StashDropInput deletes a stash entry permanently.
type StashDropInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"default=stash@{0};desc=Stash entry to delete. Default: the most recent one."`
}

// TagDeleteInput removes a tag, optionally on the remote too.
type TagDeleteInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=Tag to delete."`
	Remote   string `wick:"desc=Also delete the tag on this remote. Leave empty to delete locally only."`
}

// RawInput runs an arbitrary git subcommand, gated by the raw rules.
type RawInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Args     string `wick:"required;desc=Arguments after the word git. Example: bisect start. The subcommand must be allowed by the connector's raw rules."`
	DryRun   bool   `wick:"bool;desc=Evaluate the policy and assemble the command without running it."`
}

func doBranchCreate(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	name := strings.TrimSpace(c.Input("name"))
	return execute(c, "branch_create", repo,
		Request{Branch: name, NewBranch: true},
		func(EffectivePolicy) ([]string, error) {
			if name == "" {
				return nil, errors.New("name is required")
			}
			if c.InputBool("checkout") {
				// "checkout -b --end-of-options NAME" would consume the terminator as
				// the branch name, so it goes last. -b already binds NAME as a value,
				// so a flag-shaped name is rejected as an invalid branch name.
				args := []string{"checkout", "-b", name}
				if from := strings.TrimSpace(c.Input("from_ref")); from != "" {
					args = append(args, from)
				}
				return append(args, "--end-of-options"), nil
			}
			// "branch" takes the name positionally, so the terminator must precede it.
			args := []string{"branch", "--end-of-options", name}
			if from := strings.TrimSpace(c.Input("from_ref")); from != "" {
				args = append(args, from)
			}
			return args, nil
		}, false)
}

func doCheckout(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	ref := strings.TrimSpace(c.Input("ref"))
	create := c.InputBool("create")
	return execute(c, "checkout", repo,
		Request{Branch: ref, NewBranch: create},
		func(EffectivePolicy) ([]string, error) {
			if ref == "" {
				return nil, errors.New("ref is required")
			}
			if create {
				// See doBranchCreate: -b guards its own value; the terminator goes last.
				return []string{"checkout", "-b", ref, "--end-of-options"}, nil
			}
			return []string{"checkout", "--end-of-options", ref}, nil
		}, false)
}

func doAdd(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "add", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			paths := splitCSV(c.Input("paths"))
			if len(paths) == 0 {
				return nil, errors.New("paths is required")
			}
			// Paths sit after "--", git's pathspec terminator, so they can never be
			// read as flags — no --end-of-options needed.
			return append([]string{"add", "--"}, paths...), nil
		}, false)
}

func doCommit(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	branch := currentBranch(c, repo)
	msg := strings.TrimSpace(c.Input("message"))

	build := func(EffectivePolicy) ([]string, error) {
		if msg == "" {
			return nil, errors.New("message is required")
		}
		// -m binds the message as its value, so a message of "--amend" is recorded
		// as text rather than acted on. --all is a plain flag.
		args := []string{"commit", "-m", msg}
		if c.InputBool("all") {
			args = append(args, "--all")
		}
		return args, nil
	}

	if c.InputBool("dry_run") {
		args, err := build(EffectivePolicy{})
		if err != nil {
			return nil, err
		}
		return dryRun(c, "commit", repo, Request{Branch: branch}, args)
	}
	return execute(c, "commit", repo, Request{Branch: branch}, build, false)
}

func doStash(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "stash", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			action := strings.TrimSpace(c.Input("action"))
			switch action {
			case "push":
				// -m binds the label as its value; there is no positional here.
				args := []string{"stash", "push"}
				if m := strings.TrimSpace(c.Input("message")); m != "" {
					args = append(args, "-m", m)
				}
				return args, nil
			case "pop":
				return []string{"stash", "pop"}, nil
			case "list":
				return []string{"stash", "list"}, nil
			default:
				return nil, fmt.Errorf("action %q is not one of push, pop, list", action)
			}
		}, false)
}

func doFetch(c *connector.Ctx) (any, error) {
	return networkOp(c, "fetch", func(url string) []string {
		args := []string{"fetch"}
		if c.InputBool("prune") {
			args = append(args, "--prune")
		}
		return append(args, "--end-of-options", url)
	})
}

func doPull(c *connector.Ctx) (any, error) {
	return networkOp(c, "pull", func(url string) []string {
		args := []string{"pull"}
		if c.InputBool("rebase") {
			args = append(args, "--rebase")
		}
		args = append(args, "--end-of-options", url)
		if b := strings.TrimSpace(c.Input("branch")); b != "" {
			args = append(args, b)
		}
		return args
	})
}

func doTag(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "tag", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			name := strings.TrimSpace(c.Input("name"))
			if name == "" {
				return nil, errors.New("name is required")
			}
			args := []string{"tag"}
			if m := strings.TrimSpace(c.Input("message")); m != "" {
				// -m alone already means "annotated", and adding -a as well makes
				// "tag -a --end-of-options NAME -m MSG" fail with "too many
				// arguments". Emitting -m first keeps the terminator before the name.
				args = append(args, "-m", m)
			}
			args = append(args, "--end-of-options", name)
			if ref := strings.TrimSpace(c.Input("ref")); ref != "" {
				args = append(args, ref)
			}
			return args, nil
		}, false)
}

func doPush(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	branch := firstNonEmpty(strings.TrimSpace(c.Input("branch")), currentBranch(c, repo))
	if branch == "" {
		return nil, errors.New("branch is required (HEAD is detached, so there is no current branch)")
	}

	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}

	pol := policyFor(c, repo, info.Slug)
	force := c.InputBool("force")
	v := pol.Evaluate(Request{Op: "push", Branch: branch, Remote: remote, Force: force})
	if !v.Allow {
		return deniedEnvelope(v, "git push "+remote+" "+branch), nil
	}

	userArgs := buildPushArgs(info.Effective, branch, force, c.InputBool("set_upstream"))
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	if c.InputBool("dry_run") {
		return envelope(Result{
			OK:      true,
			Command: mask("git "+strings.Join(userArgs, " "), o.Masks),
			Stdout:  "dry run: the policy allows this command; nothing was executed",
		}, v, &info), nil
	}

	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "push"),
		UserArgs:     userArgs,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doMerge(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "merge", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			if c.InputBool("abort") {
				return []string{"merge", "--abort"}, nil
			}
			ref := strings.TrimSpace(c.Input("ref"))
			if ref == "" {
				return nil, errors.New("ref is required unless abort is set")
			}
			// --no-edit keeps git from opening an editor, which would hang until the
			// timeout. The ref is positional, so the terminator precedes it.
			args := []string{"merge", "--no-edit"}
			if c.InputBool("no_ff") {
				args = append(args, "--no-ff")
			}
			return append(args, "--end-of-options", ref), nil
		}, false)
}

func doReset(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	mode := strings.TrimSpace(c.Input("mode"))
	// A hard reset discards committed and uncommitted work, so it needs the same
	// explicit opt-in as a force push.
	return execute(c, "reset", repo,
		Request{Branch: currentBranch(c, repo), Force: mode == "hard"},
		func(EffectivePolicy) ([]string, error) {
			ref := strings.TrimSpace(c.Input("ref"))
			if ref == "" {
				return nil, errors.New("ref is required")
			}
			// The mode is checked against a closed set, so "--mode" can only ever be
			// one of three literals — a crafted mode never reaches git.
			switch mode {
			case "soft", "mixed", "hard":
			default:
				return nil, fmt.Errorf("mode %q is not one of soft, mixed, hard", mode)
			}
			// The terminator is what stops a ref of "--hard" from upgrading a soft
			// reset. Verified against git 2.52: "reset --soft --hard" silently performs
			// a HARD reset and exits 0, while "reset --soft --end-of-options --hard"
			// is refused.
			return []string{"reset", "--" + mode, "--end-of-options", ref}, nil
		}, false)
}

func doRebase(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "rebase", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			switch {
			case c.InputBool("abort"):
				return []string{"rebase", "--abort"}, nil
			case c.InputBool("continue_"):
				return []string{"rebase", "--continue"}, nil
			}
			onto := strings.TrimSpace(c.Input("onto"))
			if onto == "" {
				return nil, errors.New("onto is required unless abort or continue_ is set")
			}
			// Without the terminator an onto of "-i" starts an interactive rebase,
			// which blocks on an editor until the timeout; "--root" would rewrite the
			// entire history. With it, git reports "invalid upstream" for both.
			return []string{"rebase", "--end-of-options", onto}, nil
		}, false)
}

func doClone(c *connector.Ctx) (any, error) {
	dest := strings.TrimSpace(c.Input("dest"))
	if dest == "" {
		return nil, errors.New("dest is required")
	}
	// Refuse an existing destination rather than clone into it: git would either
	// fail confusingly or, for an empty directory, succeed and make it unclear
	// whether the contents came from this clone.
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("dest %q already exists; clone would not be a fresh checkout", dest)
	}
	info, err := ConvertRemote(strings.TrimSpace(c.Input("url")),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}

	pol := policyFor(c, dest, info.Slug)
	v := pol.Evaluate(Request{Op: "clone", Remote: info.Effective})
	if !v.Allow {
		return deniedEnvelope(v, "git clone "+info.Effective), nil
	}

	args := []string{"clone"}
	if b := strings.TrimSpace(c.Input("branch")); b != "" {
		// --branch binds its value, so a flag-shaped branch stays data.
		args = append(args, "--branch", b)
	}
	if d := c.InputInt("depth"); d > 0 {
		args = append(args, "--depth", strconv.Itoa(d))
	}
	// Both the URL and the destination are positional. Verified against git 2.52:
	// after the terminator, a dest of "--bare" is used as a directory name
	// ("Cloning into '--bare'") instead of turning the clone into a bare one.
	args = append(args, "--end-of-options", info.Effective, dest)

	if err := ValidateUserArgs(args); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	// Clone has no existing repo to run inside, so the working directory is the
	// destination's parent rather than a repo path.
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create the parent of dest %q: %w", dest, err)
	}
	res, err := Run(c.Context(), Cmd{
		RepoPath:     parent,
		InjectedArgs: injectedArgs(c, o.Auth, "clone"),
		UserArgs:     args,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doStashDrop(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "stash_drop", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			ref := firstNonEmpty(strings.TrimSpace(c.Input("ref")), "stash@{0}")
			return []string{"stash", "drop", "--end-of-options", ref}, nil
		}, false)
}

func doTagDelete(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	name := strings.TrimSpace(c.Input("name"))
	remote := strings.TrimSpace(c.Input("remote"))

	if remote == "" {
		return execute(c, "tag_delete", repo, Request{},
			func(EffectivePolicy) ([]string, error) {
				if name == "" {
					return nil, errors.New("name is required")
				}
				return []string{"tag", "-d", "--end-of-options", name}, nil
			}, false)
	}

	// Deleting a remote tag is a network mutation: push an empty ref.
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("name is required")
	}
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: "tag_delete", Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git push "+remote+" :refs/tags/"+name), nil
	}
	userArgs := []string{"push", "--end-of-options", info.Effective, ":refs/tags/" + name}
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "tag_delete"),
		UserArgs:     userArgs,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doRaw(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	args := SplitRawArgs(c.Input("args"))
	if len(args) == 0 {
		return nil, errors.New("args is required")
	}
	// RawSubcommandOf fails closed: an unrecognised leading flag yields "", which
	// the policy denies. No --end-of-options here by design — raw's whole purpose
	// is to pass flags through, and the gate is the per-subcommand allow list plus
	// ValidateUserArgs, not a positional terminator.
	sub := RawSubcommandOf(args)

	pol := policyFor(c, repo, RepoSlug(remoteURL(c, repo, "origin")))
	v := pol.Evaluate(Request{Op: "raw", RawSubcommand: sub})
	if !v.Allow {
		return deniedEnvelope(v, "git "+strings.Join(args, " ")), nil
	}
	if err := ValidateUserArgs(args); err != nil {
		return nil, err
	}

	o := runOpts(c, true) // a raw subcommand may reach the network; use the longer budget
	if c.InputBool("dry_run") {
		return envelope(Result{
			OK:      true,
			Command: mask("git "+strings.Join(args, " "), o.Masks),
			Stdout:  "dry run: the policy allows this command; nothing was executed",
		}, v, nil), nil
	}
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "raw"),
		UserArgs:     args,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, nil), nil
}

// Operations returns the connector's operation tree. Read operations are safe by
// construction; mutating and destructive ones are added in later categories.
func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat("Read", "Inspect a repository without changing it.",
			connector.Op("status", "Status",
				"Report the working tree state of the repository at {repo_path} in porcelain v2 format. Returns staged, unstaged and untracked entries plus the current branch. Never modifies anything.",
				StatusInput{}, doStatus, wickdocs.Docs{}),
			connector.Op("log", "Log",
				"Read commit history at {repo_path}. Returns one tab-separated line per commit: hash, author, ISO date, subject. Defaults to 20 commits — raise {limit} only when a wider window is needed.",
				LogInput{}, doLog, wickdocs.Docs{}),
			connector.Op("diff", "Diff",
				"Compare {ref_a} against {ref_b} (or the working tree when {ref_b} is empty) at {repo_path}. Set {stat_only} for a file summary instead of a full patch, which is much smaller. Patches longer than {max_lines} are cut and reported as truncated.",
				DiffInput{}, doDiff, wickdocs.Docs{}),
			connector.Op("branch_list", "List Branches",
				"List branches at {repo_path} with each branch's short commit and last commit date, tab-separated. Set {remote} to list remote-tracking branches instead of local ones.",
				BranchListInput{}, doBranchList, wickdocs.Docs{}),
			connector.Op("show", "Show Commit",
				"Show commit {ref} at {repo_path} with its changed-file summary. Returns the commit message plus per-file statistics, not the full patch.",
				ShowInput{}, doShow, wickdocs.Docs{}),
			connector.Op("remote_list", "List Remotes",
				"List the remotes of {repo_path}. For each one returns the configured URL with credentials stripped and the URL network operations would actually use, so an SSH-to-HTTPS conversion is visible before anything is pushed.",
				RemoteListInput{}, doRemoteList, wickdocs.Docs{}),
			connector.Op("ls_remote", "Probe Remote",
				"List the branches remote {remote} advertises for {repo_path}, without fetching or changing anything. The cheapest way to verify that the credential and the remote URL both work.",
				LsRemoteInput{}, doLsRemote, wickdocs.Docs{}),
		),
		connector.Cat("Branches and Commits", "Create branches, stage and record changes.",
			connector.Op("branch_create", "Create Branch",
				"Create branch {name} at {repo_path}, optionally from {from_ref}. The name must satisfy the connector's branch pattern and must not be a protected branch — both are enforced before git runs.",
				BranchCreateInput{}, doBranchCreate, wickdocs.Docs{}),
			connector.Op("checkout", "Checkout",
				"Switch {repo_path} to {ref}. With {create} set, the branch is created first and the branch pattern applies. Fails if the working tree has conflicting changes.",
				CheckoutInput{}, doCheckout, wickdocs.Docs{}),
			connector.Op("add", "Stage Paths",
				"Stage {paths} in {repo_path} for the next commit. Accepts a comma-separated list; use . to stage everything.",
				AddInput{}, doAdd, wickdocs.Docs{}),
			connector.Op("commit", "Commit",
				"Record staged changes at {repo_path} with {message}. Blocked when the current branch is protected. Set {dry_run} to see the command and the policy verdict without committing.",
				CommitInput{}, doCommit, wickdocs.Docs{}),
			connector.Op("stash", "Stash",
				"Save, restore or list work in progress at {repo_path}. push saves the working tree, pop restores the most recent entry, list shows entries. Deleting an entry is the separate stash_drop operation.",
				StashInput{}, doStash, wickdocs.Docs{}),
			connector.Op("tag", "Create Tag",
				"Create tag {name} at {repo_path}, annotated when {message} is supplied. Local only — pushing tags is a push operation.",
				TagInput{}, doTag, wickdocs.Docs{}),
		),
		connector.Cat("Network", "Exchange commits with a remote.",
			connector.Op("fetch", "Fetch",
				"Update remote-tracking refs for {remote} at {repo_path} without touching the working tree. Uses the connector's credential and the remote's HTTPS URL, ignoring any credentials stored in .git/config.",
				FetchInput{}, doFetch, wickdocs.Docs{}),
			connector.Op("pull", "Pull",
				"Fetch {remote} and integrate it into the current branch at {repo_path}, rebasing instead of merging when {rebase} is set. Blocked when the current branch is protected.",
				PullInput{}, doPull, wickdocs.Docs{}),
		),
		connector.Cat("Destructive", "Operations that publish or discard work. Each is off by default on a new instance.",
			connector.OpDestructive("push", "Push",
				"Publish commits from {repo_path} to {branch} on {remote}. Blocked when the target branch is protected; {force} additionally requires allow_force_push and always uses --force-with-lease. Set {dry_run} to see the command and verdict without pushing.",
				PushInput{}, doPush, wickdocs.Docs{}),
			connector.OpDestructive("merge", "Merge",
				"Merge {ref} into the current branch at {repo_path}, or abort a conflicted merge with {abort}. Blocked when the current branch is protected. Never opens an editor.",
				MergeInput{}, doMerge, wickdocs.Docs{}),
			connector.OpDestructive("reset", "Reset",
				"Move HEAD at {repo_path} to {ref}. soft keeps the index and working tree, mixed resets the index, hard discards every local change and requires allow_force_push.",
				ResetInput{}, doReset, wickdocs.Docs{}),
			connector.OpDestructive("rebase", "Rebase",
				"Replay the current branch's commits onto {onto} at {repo_path}, or abort or continue an in-progress rebase. Never interactive — an interactive rebase would block on an editor until the timeout.",
				RebaseInput{}, doRebase, wickdocs.Docs{}),
			connector.OpDestructive("clone", "Clone",
				"Clone {url} into {dest}. An SSH URL is converted to HTTPS when the connector allows it; a self-hosted host needs a remote_host_map row. Fails when {dest} already exists.",
				CloneInput{}, doClone, wickdocs.Docs{}),
			connector.OpDestructive("stash_drop", "Drop Stash",
				"Delete stash entry {ref} at {repo_path}. A dropped stash cannot be recovered, which is why this is separate from the stash operation.",
				StashDropInput{}, doStashDrop, wickdocs.Docs{}),
			connector.OpDestructive("tag_delete", "Delete Tag",
				"Delete tag {name} at {repo_path}, and on {remote} as well when it is supplied. Deleting a remote tag is a network mutation others will see.",
				TagDeleteInput{}, doTagDelete, wickdocs.Docs{}),
			connector.OpDestructive("raw", "Raw Git Command",
				"Run an arbitrary git subcommand at {repo_path} with {args}. Requires raw_enabled and an explicit allow rule for that subcommand; an unlisted subcommand is denied. Arguments that could execute shell commands are rejected. Set {dry_run} to check the verdict first.",
				RawInput{}, doRaw, wickdocs.Docs{}),
		),
		connector.Cat("Configuration", "Widgets that back the config form. Not available to agents.",
			connector.OpConfigOnly("setup_guide", "Credential Setup Guide",
				"Render the per-host credential guide shown in the Authentication section: which username and token type to use, which permissions to grant, and where to click to create the token. Backs the config form only; never called by an agent.",
				setupGuideInput{}, doSetupGuide, wickdocs.Docs{}),
			connector.OpConfigOnly("test_panel", "Test Panel",
				"Render the diagnostics form in the config page. Backs the config form only; never called by an agent.",
				testGuideInput{}, doTestPanel, wickdocs.Docs{}),
			connector.OpConfigOnly("test_run", "Run Connector Check",
				"Run the read-only diagnostics against a repository: resolve the remote, authenticate with ls-remote, and evaluate the policy. Backs the Run check button in the config page; never called by an agent.",
				testGuideInput{}, doTestRun, wickdocs.Docs{}),
			connector.OpConfigOnly("test_write", "Run Write Check",
				"Clone into a throwaway sandbox, commit, push the named branch, then delete the sandbox. Verifies the token really has write permission, which no read-only check can prove. The push is still policy-gated. Backs the Run write check button in the config page; never called by an agent.",
				testGuideInput{}, doTestWrite, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_manager", "Policy Manager",
				"Render the per-repo policy editor and simulator. Backs the Policy Rules widget in the config form; never called by an agent.",
				policyManagerInput{}, doPolicyManager, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_simulate", "Simulate Policy",
				"Evaluate a hypothetical operation against the current policy and report the verdict, the matched rule and the command that would run. Backs the simulator button.",
				policyManagerInput{}, doPolicySimulate, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_save", "Save Policy Rules",
				"Replace the per-repo rule set from the editor textarea and report per-rule warnings. Backs the Save rules button.",
				policyManagerInput{}, doPolicyRuleSave, wickdocs.Docs{}),
		),
	}
}

// Module assembles the definition served by main.go.
func Module() connector.Module {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.Development}
	return connector.Module{
		Meta:       m,
		Configs:    entity.StructToConfigs(Config{}),
		Operations: Operations(),
	}
}
