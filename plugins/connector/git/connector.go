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

	// The whole policy is edited through one widget, so every field it owns is
	// hidden. Two reasons this beats a grid of inputs:
	//
	//   - A regex written blind is a guess. The widget compiles it as you save,
	//     shows which repositories a glob actually matches, and lets you try a
	//     repo/op/branch/message against the compiled result before anyone relies
	//     on it.
	//   - Global rules and per-repo overrides are one decision at two scopes. Laid
	//     out as separate inputs, nothing on screen says which wins; stacked inside
	//     one panel, precedence reads top-down.
	//
	// Hidden rows are still seeded and still readable via c.Cfg, so the runtime is
	// unchanged — only the default Settings rendering is skipped. The widget carries
	// a raw-JSON escape hatch for the case where its own buttons ever fail.
	BranchNamePattern    string `wick:"hidden;desc=Regex a new branch name must match. Managed by the Policy widget."`
	ProtectedBranches    string `wick:"kvlist=branch;hidden;desc=Protected branches. Managed by the Policy widget."`
	AllowForcePush       bool   `wick:"bool;hidden;desc=Allow --force and --force-with-lease. Managed by the Policy widget."`
	CommitMessagePattern string `wick:"hidden;desc=Regex a commit message must match. Managed by the Policy widget."`
	RepoPolicies         string `wick:"hidden;desc=Per-repo policy rows. Managed by the Policy widget."`

	PolicyManager string `wick:"html=policy_manager;group=Policy|Everything this connector refuses to do: the fallback that applies everywhere, per-repo overrides that win over it, and a simulator to check a rule before trusting it.|collapsed;desc=The only place policy is edited. Nothing outside this panel changes it."`

	RawEnabled bool   `wick:"bool;group=Raw Operation||collapsed;desc=Enable the raw operation, which runs an arbitrary git subcommand. Off by default."`
	RawRules   string `wick:"kvlist=subcommand|mode;group=Raw Operation;desc=Per-subcommand rules for raw. mode is allow or deny. A subcommand that is not listed is denied."`

	// Opt-in, and empty means unrestricted, because that is the behaviour this
	// connector shipped with and the one that makes it useful: it manages repositories
	// that already exist, wherever they are, and a mandatory sandbox would put every
	// existing checkout out of reach. Configuring roots is how an operator narrows that
	// per instance.
	AllowedRepoRoots string `wick:"kvlist=root;group=Runtime||collapsed;desc=Directories the connector may touch. Leave EMPTY to allow any repository the process can reach. When set, every repo_path and clone destination must resolve inside one of these roots — symlinks and .. are resolved first, so neither escapes. Example: d:/code/work"`

	AllowHooks            bool `wick:"bool;group=Runtime;desc=Let repository hooks in .git/hooks run. Off by default, because a hook is arbitrary code from the repository."`
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
	TestRepo    string `wick:"hidden;desc=Last repository tested, kept so the diagnostics form survives a re-render."`
	TestRemote  string `wick:"hidden;desc=Last remote tested."`
	TestBranch  string `wick:"hidden;desc=Last branch tested."`
	TestMessage string `wick:"hidden;desc=Last commit message tested."`
}

// Meta identifies the connector. Key must equal the folder name.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Git CLI",
		// The description leads with policy_show because the alternative is discovery by
		// refusal: Evaluate stops at the first rule that fires, so a caller that starts
		// writing learns one rule per rejected call and never hears about a rule it has
		// not yet broken. Rules also differ per repository, so nothing here can be
		// stated once and assumed — it has to be asked for, per repo.
		Description: "Run git against local repositories, with a policy that can refuse a branch name, a commit message, a push to a protected branch, or a force push. Rules DIFFER PER REPOSITORY. Call policy_show for the repository first and comply with what it returns — every other operation reports a refusal only after the fact, one rule at a time. Wraps the git binary, so it works with any host.",
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
		// Bounded at 5000: a log of every commit in a large repository is not a useful
		// answer, and the output cap would truncate it anyway without saying why.
		limit, err := intInput(c, "limit", 20, 5000)
		if err != nil {
			return nil, err
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
	maxLines, err := intInput(c, "max_lines", 500, 100000)
	if err != nil {
		return nil, err
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
	out, err := execute(c, "branch_list", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		// %(if)%(symref) drops origin/HEAD. Listed plainly it appeared as a bare
		// "origin" among origin/main and origin/dev, which reads as a branch called
		// "origin" — it is the remote's default-branch pointer, not a branch, and it
		// duplicates whichever branch it points at. git can test for it but not omit the
		// line, so the empty rows it leaves are stripped below.
		args := []string{"branch",
			"--format=%(if)%(symref)%(then)%(else)%(refname:short)%09%(objectname:short)%09%(committerdate:iso8601)%(end)"}
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
	if err != nil {
		return nil, err
	}
	return dropBlankLines(out), nil
}

// dropBlankLines removes empty stdout rows from a response.
//
// Needed because a git --format can decide to print nothing for a ref but still emits
// the newline, so filtering inside the format leaves holes rather than removing rows.
func dropBlankLines(out any) any {
	m, ok := out.(map[string]any)
	if !ok {
		return out
	}
	s, ok := m["stdout"].(string)
	if !ok || s == "" {
		return out
	}
	kept := make([]string, 0, 16)
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	m["stdout"] = strings.Join(kept, "\n")
	return m
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
	if err := validateRepo(c, repo); err != nil {
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
	if err := validateRepo(c, repo); err != nil {
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
		InjectedArgs: injectedArgs(c, o.Auth, "ls_remote", info.RewriteArgs),
		UserArgs:     []string{"ls-remote", "--heads", "--end-of-options", remote},
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
// Three subcommands reject the terminator in the obvious position and are handled
// differently:
//
//	checkout -b   "checkout -b --end-of-options NAME" consumes the terminator as
//	              the branch name. The guard is unnecessary there anyway (-b binds
//	              its own value), so it is omitted — or, when a start-point is
//	              supplied, placed immediately before it, which is the only
//	              positional in that argv that needs one.
//	checkout ref  "checkout --end-of-options REF" is not portable: git 2.43 does
//	              not recognise the terminator there and falls through to pathspec
//	              parsing, so nothing is checked out and the error names the
//	              terminator as a missing pathspec. 2.44+ accept it, which is why
//	              this survived testing. The portable disambiguator is "--", and
//	              since that does not stop option parsing, doCheckout runs the ref
//	              through ValidateRefName instead — a leading "-" is refused before
//	              an argv exists.
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
			if err := ValidateRefName("name", name); err != nil {
				return nil, err
			}
			if name == "" {
				return nil, errors.New("name is required")
			}
			if c.InputBool("checkout") {
				// -b binds NAME as a value, so a flag-shaped name is rejected as an
				// invalid branch name and needs no terminator. The terminator cannot
				// go after NAME either — that slot is checkout's start-point, and git
				// reads --end-of-options there as a commit-ish ("is not a commit").
				// It goes before from_ref, the only positional here that needs it.
				args := []string{"checkout", "-b", name}
				if from := strings.TrimSpace(c.Input("from_ref")); from != "" {
					args = append(args, "--end-of-options", from)
				}
				return args, nil
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
			// checkout lost its --end-of-options guard below (not portable to 2.43),
			// so the value check is now the defence that stops a flag-shaped ref
			// rather than a second line behind the terminator. branch_create has
			// always called this; checkout never did.
			if err := ValidateRefName("ref", ref); err != nil {
				return nil, err
			}
			if create {
				// See doBranchCreate: -b guards its own value, and no terminator can
				// follow the name — that slot is checkout's start-point.
				return []string{"checkout", "-b", ref}, nil
			}
			// "checkout --end-of-options <ref>" is NOT portable: git 2.43 does not
			// recognise the terminator here and falls through to pathspec parsing,
			// so the ref never gets checked out —
			//
			//   error: pathspec '--end-of-options' did not match any file(s) known to git
			//
			// The disambiguator checkout documents is "--", which every supported
			// version understands and which still refuses a flag-shaped ref (git
			// parses options before it). Verified on 2.43 and 2.52.
			return []string{"checkout", ref, "--"}, nil
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
		return dryRun(c, "commit", repo, Request{Branch: branch, Message: msg}, args)
	}
	return execute(c, "commit", repo, Request{Branch: branch, Message: msg}, build, false)
}

func doStash(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")

	// The gate is per ACTION, not per operation.
	//
	// One op covers three git commands with opposite effects: push and pop move work
	// around, list only reads. Judged as one op, "stash list" on a protected branch was
	// refused with "direct stash is blocked" — a read denied for writing nothing. The
	// op name is what the policy sees, so the read action has to be routed as the read
	// it is rather than relabelled.
	if strings.TrimSpace(c.Input("action")) == "list" {
		return execute(c, "stash_list", repo, Request{},
			func(EffectivePolicy) ([]string, error) {
				return []string{"stash", "list"}, nil
			}, false)
	}

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
	return networkOp(c, "fetch", func(remote string) []string {
		args := []string{"fetch"}
		if c.InputBool("prune") {
			args = append(args, "--prune")
		}
		// The remote NAME, so git applies the remote's configured refspec and updates
		// refs/remotes/<remote>/*. With a URL here it wrote FETCH_HEAD and nothing else,
		// and a branch pushed through the connector never appeared in branch_list.
		return append(args, "--end-of-options", remote)
	})
}

func doPull(c *connector.Ctx) (any, error) {
	return networkOp(c, "pull", func(remote string) []string {
		args := []string{"pull"}
		if c.InputBool("rebase") {
			args = append(args, "--rebase")
		}
		// The remote NAME: an omitted {branch} is meant to fall back to the current
		// branch's upstream, and an upstream can only resolve against a named remote.
		args = append(args, "--end-of-options", remote)
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
			if err := ValidateRefName("name", name); err != nil {
				return nil, err
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
	if err := validateRepo(c, repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	branch := firstNonEmpty(strings.TrimSpace(c.Input("branch")), currentBranch(c, repo))
	if branch == "" {
		return nil, errors.New("branch is required (HEAD is detached, so there is no current branch)")
	}
	// Validated as a VALUE, not by argv position. The branch is embedded in
	// "HEAD:refs/heads/<branch>", so it arrives as one token starting with "HEAD:" and
	// ValidateUserArgs — a deny-list over tokens — never saw it. The same string as
	// show's {ref} was refused, which made the protection depend on where a value
	// happened to land rather than on the value itself.
	if err := ValidateRefName("branch", branch); err != nil {
		return nil, err
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
		return deniedEnvelope(v, "git push "+remote+" "+branch, "push"), nil
	}

	userArgs := buildPushArgs(remote, branch, force, c.InputBool("set_upstream"))
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
		InjectedArgs: injectedArgs(c, o.Auth, "push", info.RewriteArgs),
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
	// Scope-checked like every repo_path, but through CheckPathRoots directly:
	// ValidateRepoPath requires an existing directory containing .git, and a clone
	// destination is neither yet. resolvePath handles that — it resolves the deepest
	// existing ancestor, which is what catches a symlinked parent.
	if err := CheckPathRoots(dest, allowedRoots(c)); err != nil {
		return nil, err
	}
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
		return deniedEnvelope(v, "git clone "+info.Effective, "clone"), nil
	}

	args := []string{"clone"}
	if b := strings.TrimSpace(c.Input("branch")); b != "" {
		// --branch binds its value, so a flag-shaped branch is already inert here. The
		// name is still validated, because "already safe in this position" is exactly the
		// reasoning that left push open.
		if err := ValidateRefName("branch", b); err != nil {
			return nil, err
		}
		args = append(args, "--branch", b)
	}
	depth, err := intInput(c, "depth", 0, 0)
	if err != nil {
		return nil, err
	}
	if d := depth; d > 0 {
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
	if err := validateRepo(c, repo); err != nil {
		return nil, err
	}
	if err := ValidateRefName("name", name); err != nil {
		return nil, err
	}
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: "tag_delete", Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git push "+remote+" :refs/tags/"+name, "tag_delete"), nil
	}
	userArgs := []string{"push", "--end-of-options", remote, ":refs/tags/" + name}
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "tag_delete", info.RewriteArgs),
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
	if err := validateRepo(c, repo); err != nil {
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
		return deniedEnvelope(v, "git "+strings.Join(args, " "), "raw"), nil
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
			connector.Op("policy_show", "Show Policy",
				"Report every policy rule in force for {repo_path}: the branch name pattern, the commit message pattern, the protected branches, whether force push is allowed, and which rules gate which operation. Rules differ PER REPOSITORY, so call this with the repo you are about to work on. Each rule names the syntax it is written in — regex or glob — and a pattern that is set comes with an example value it accepts. Call this BEFORE creating a branch or committing: otherwise a rule is only discovered by violating it, one refusal at a time, and a rule nobody has violated yet is invisible. Spawns no git process and changes nothing.",
				PolicyShowInput{}, doPolicyShow, wickdocs.Docs{}),
		),
		connector.Cat("Branches and Commits", "Create branches, stage and record changes.",
			connector.Op("branch_create", "Create Branch",
				"Create branch {name} at {repo_path}, optionally from {from_ref}. The name must satisfy the connector's branch pattern and must not be a protected branch — both are enforced before git runs, and the pattern differs per repository. Call policy_show first to read the pattern and an example name it accepts, rather than guessing a name and being refused.",
				BranchCreateInput{}, doBranchCreate, wickdocs.Docs{}),
			connector.Op("checkout", "Checkout",
				"Switch {repo_path} to {ref}. With {create} set, the branch is created first and the branch pattern applies. Fails if the working tree has conflicting changes.",
				CheckoutInput{}, doCheckout, wickdocs.Docs{}),
			connector.Op("add", "Stage Paths",
				"Stage {paths} in {repo_path} for the next commit. Accepts a comma-separated list; use . to stage everything.",
				AddInput{}, doAdd, wickdocs.Docs{}),
			connector.Op("commit", "Commit",
				"Record staged changes at {repo_path} with {message}. Blocked when the current branch is protected, and when {message} does not match the connector's commit message pattern — call policy_show first for that pattern and an example message it accepts, since a length floor or a required scope is not obvious from the regex. Set {dry_run} to see the command and the policy verdict without committing.",
				CommitInput{}, doCommit, wickdocs.Docs{}),
			connector.Op("stash", "Stash",
				"Save, restore or list work in progress at {repo_path}. push saves the working tree, pop restores the most recent entry, list shows entries. Policy is judged per ACTION: push and pop are refused on a protected branch, list is a read and is never refused. Deleting an entry is the separate stash_drop operation.",
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
				"Publish commits from {repo_path} to {branch} on {remote}. Blocked when the target branch is protected; {force} additionally requires allow_force_push and always uses --force-with-lease. policy_show lists the protected branches for this repository. Set {dry_run} to see the command and verdict without pushing.",
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
			connector.OpConfigOnly("policy_global_save", "Save Policy Fallback",
				"Write the fallback policy — branch pattern, commit message pattern, protected branches, force push — after checking that every regex compiles. Backs the Save fallback button in the config form; never called by an agent.",
				policyManagerInput{}, doPolicyGlobalSave, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_add", "Add Policy Override",
				"Append a per-repo override for a repository glob, with every column inheriting the fallback until it is changed. Backs the Add repository button; never called by an agent.",
				policyManagerInput{}, doPolicyRuleAdd, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_update", "Update Policy Override",
				"Write one per-repo override from its panel, after checking that its branch pattern compiles. Backs the Save button on an override; never called by an agent.",
				policyManagerInput{}, doPolicyRuleUpdate, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_clear", "Clear Inherited Rules",
				"Mark one override's inheritable columns as cleared, so the fallback's branch pattern and protected list do not apply to it. Writes the clear marker so nobody has to type it. Backs the Clear inherited button; never called by an agent.",
				policyManagerInput{}, doPolicyRuleClear, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_delete", "Delete Policy Override",
				"Remove one per-repo override, after which its repositories fall back to the global rules. Backs the Delete button; never called by an agent.",
				policyManagerInput{}, doPolicyRuleDelete, wickdocs.Docs{}),
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
