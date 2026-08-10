// service.go bridges connector config into the pure types from policy.go and
// git.go. It is the only place that reads c.Cfg — everything downstream takes
// typed values, which keeps the policy engine and runner testable without a Ctx.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// parseRawRules turns the raw_rules kvlist into subcommand → mode. Keys and modes
// are lowercased so a row typed as "Push"/"DENY" still matches.
func parseRawRules(s string) map[string]string {
	rows, err := ParseKVList(s)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		sub := strings.ToLower(strings.TrimSpace(r["subcommand"]))
		mode := strings.ToLower(strings.TrimSpace(r["mode"]))
		if sub == "" || (mode != "allow" && mode != "deny") {
			continue
		}
		out[sub] = mode
	}
	return out
}

// loadGlobal reads layer 1 of the policy from config.
func loadGlobal(c *connector.Ctx) GlobalPolicy {
	protected := make([]string, 0, 4)
	if rows, err := ParseKVList(c.Cfg("protected_branches")); err == nil {
		for _, r := range rows {
			if b := strings.TrimSpace(r["branch"]); b != "" {
				protected = append(protected, b)
			}
		}
	}
	return GlobalPolicy{
		BranchPattern:  strings.TrimSpace(c.Cfg("branch_name_pattern")),
		Protected:      protected,
		AllowForcePush: c.CfgBool("allow_force_push"),
		RawEnabled:     c.CfgBool("raw_enabled"),
		RawRules:       parseRawRules(c.Cfg("raw_rules")),
	}
}

// policyFor compiles the effective policy for one repo. Invalid repo_policies
// JSON is recorded as PolicyErr rather than ignored, so mutations fail closed.
func policyFor(c *connector.Ctx, repoPath, repoSlug string) EffectivePolicy {
	global := loadGlobal(c)
	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	if err != nil {
		p := Resolve(global, nil, repoPath, repoSlug)
		p.PolicyErr = "repo_policies is not valid JSON: " + err.Error()
		return p
	}
	return Resolve(global, rules, repoPath, repoSlug)
}

// loadAuth reads the HTTPS credential. An empty token is valid — the repo may be
// public, or the operation may not touch the network.
func loadAuth(c *connector.Ctx) AuthSpec {
	method := strings.TrimSpace(c.Cfg("auth_method"))
	if method == "" {
		method = "askpass"
	}
	return AuthSpec{
		Method:   method,
		Username: strings.TrimSpace(c.Cfg("username")),
		Token:    c.Cfg("token"),
	}
}

// runOpts assembles runner options, applying the network timeout for operations
// that contact a remote.
func runOpts(c *connector.Ctx, network bool) RunOpts {
	timeout := c.CfgInt("timeout_seconds")
	if network {
		if nt := c.CfgInt("network_timeout_seconds"); nt > 0 {
			timeout = nt
		} else {
			timeout = 180
		}
	}
	if timeout <= 0 {
		timeout = 60
	}
	maxOut := c.CfgInt("max_output_bytes")
	if maxOut <= 0 {
		maxOut = 262144
	}
	auth := loadAuth(c)

	// Every encoding the credential can reach argv or output in must be masked,
	// not just the raw token. With auth_method=extraheader the token travels as
	// "Authorization: Basic base64(user:token)", and base64 is one decode step
	// away from the plaintext — masking only the raw token would leave a fully
	// usable credential in Result.Command and therefore in the run history.
	masks := make([]string, 0, 2)
	if auth.Token != "" {
		masks = append(masks, auth.Token, basicAuthValue(auth.Username, auth.Token))
	}
	return RunOpts{
		Auth:      auth,
		SelfPath:  selfPath(),
		Timeout:   time.Duration(timeout) * time.Second,
		MaxOutput: maxOut,
		Masks:     masks,
	}
}

// selfPath returns this binary's path, used as the GIT_ASKPASS helper.
func selfPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// firstNonEmpty returns the first value that is not blank, so a caller can spell
// "input, else default" without a temporary variable per site.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// envelope is the single response shape every operation returns. Reporting the
// policy verdict on success as well as failure means "why did this run" is always
// answerable, and reporting the effective remote makes a push to an unexpected
// host visible instead of mysterious.
func envelope(res Result, v Verdict, info *RemoteInfo) any {
	policy := map[string]any{
		"evaluated":    true,
		"verdict":      verdictWord(v.Allow),
		"matched_rule": v.MatchedRule,
	}
	if v.Reason != "" {
		policy["reason"] = v.Reason
	}
	out := map[string]any{
		"ok":          res.OK,
		"command":     res.Command,
		"exit_code":   res.ExitCode,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"truncated":   res.Truncated,
		"duration_ms": res.DurationMS,
		"policy":      policy,
	}
	if info != nil {
		out["remote"] = map[string]any{
			"original":  StripCredentials(info.Original),
			"effective": info.Effective,
			"converted": info.Converted,
		}
	}
	return out
}

func verdictWord(allow bool) string {
	if allow {
		return "allow"
	}
	return "deny"
}

// deniedEnvelope is returned when the policy blocks an operation. No process is
// spawned, so there is no Result to report. ExitCode -1 marks "never ran", which
// a caller must not confuse with git's own exit code 1.
func deniedEnvelope(v Verdict, command string) any {
	return envelope(Result{OK: false, Command: command, ExitCode: -1}, v, nil)
}

// execute is the shared path for every operation: validate the repo, compile the
// policy, evaluate, then run. build assembles the git arguments and runs after
// the policy passes, so a denied operation never even builds a command line.
func execute(c *connector.Ctx, op string, repoPath string, req Request,
	build func(EffectivePolicy) ([]string, error), network bool) (any, error) {

	if err := ValidateRepoPath(repoPath); err != nil {
		return nil, err
	}

	slug := RepoSlug(remoteURL(c, repoPath, firstNonEmpty(req.Remote, "origin")))
	pol := policyFor(c, repoPath, slug)
	req.Op = op

	v := pol.Evaluate(req)
	if !v.Allow {
		return deniedEnvelope(v, "git "+op), nil
	}

	userArgs, err := build(pol)
	if err != nil {
		return nil, err
	}
	// Only what the builder produced from agent input is filtered. Validating
	// cmd.Argv() instead would reject the plugin's own injected "-c …" and break
	// every authenticated operation.
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}

	o := runOpts(c, network)
	cmd := Cmd{
		RepoPath:     repoPath,
		InjectedArgs: injectedArgs(c, o.Auth, op),
		UserArgs:     userArgs,
		Network:      network,
	}
	res, err := Run(c.Context(), cmd, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, nil), nil
}

// injectedArgs are the plugin's own git options. They are never passed through
// ValidateUserArgs — that deny-list exists to filter agent input, and blocking
// our own injections would break credential handling and hook suppression.
func injectedArgs(c *connector.Ctx, auth AuthSpec, op string) []string {
	args := AuthInjectedArgs(auth)

	// Repository hooks are arbitrary code from the repository. Suppress them for
	// the operations that would run one, unless the admin opted in.
	if !c.CfgBool("allow_hooks") && hookRunningOps[op] {
		args = append(args, "-c", "core.hooksPath="+emptyHooksDir())
	}
	if name := strings.TrimSpace(c.Cfg("author_name")); name != "" {
		args = append(args, "-c", "user.name="+name)
	}
	if email := strings.TrimSpace(c.Cfg("author_email")); email != "" {
		args = append(args, "-c", "user.email="+email)
	}
	return args
}

// hookRunningOps are the operations git would run a hook for.
var hookRunningOps = map[string]bool{
	"commit": true, "merge": true, "push": true, "checkout": true, "rebase": true,
}

// emptyHooksDir returns a directory guaranteed to contain no hooks. It is created
// once under the OS temp dir; an empty directory is not a secret and holds no
// state, so leaving it behind is harmless.
func emptyHooksDir() string {
	dir := filepath.Join(os.TempDir(), "wick-git-nohooks")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// currentBranch reads the checked-out branch. A detached HEAD yields "", which
// the policy treats as an unprotected, non-matching branch — mutations on a
// detached HEAD are still gated by the protected-branch check on the target.
func currentBranch(c *connector.Ctx, repoPath string) string {
	res, err := Run(c.Context(),
		Cmd{RepoPath: repoPath, UserArgs: []string{"rev-parse", "--abbrev-ref", "HEAD"}},
		runOpts(c, false))
	if err != nil || !res.OK {
		return ""
	}
	branch := strings.TrimSpace(res.Stdout)
	if branch == "HEAD" {
		return "" // detached
	}
	return branch
}

// buildPushArgs assembles a push that names the remote URL explicitly. Passing
// the URL rather than "origin" is what makes credentials embedded in
// .git/config irrelevant — git uses the URL given here and our askpass helper.
//
// Force always means --force-with-lease: it refuses to overwrite commits the
// local clone has not seen, which is the difference between recovering from a
// bad rebase and destroying a colleague's work. Bare --force is never emitted.
//
// The URL and the refspec are positional, so --end-of-options precedes them.
// Verified against git 2.52: without it a remote URL of "--receive-pack=evil"
// is parsed as a flag and runs an arbitrary binary on the far end; with it, git
// reports "strange pathname blocked" instead.
func buildPushArgs(url, branch string, force, setUpstream bool) []string {
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "--end-of-options", url, "HEAD:refs/heads/"+branch)
	return args
}

// networkOp is the shared shape for fetch and pull: resolve the remote, convert
// it, evaluate the policy, then run against the explicit URL. build receives the
// effective URL and must place it after its own --end-of-options.
func networkOp(c *connector.Ctx, op string, build func(url string) []string) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: op, Branch: currentBranch(c, repo), Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git "+op+" "+remote), nil
	}
	userArgs := build(info.Effective)
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, op),
		UserArgs:     userArgs,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

// dryRun evaluates the policy and reports the command that would run, without
// spawning anything. It exists so an agent can ask "would this be allowed" for a
// mutation whose real execution is not recoverable.
//
// The command is masked before it is returned: a dry run of an authenticated
// operation must not hand back a credential the real run would have hidden.
func dryRun(c *connector.Ctx, op, repoPath string, req Request, userArgs []string) (any, error) {
	if err := ValidateRepoPath(repoPath); err != nil {
		return nil, err
	}
	// The same deny-list the real path applies. A dry run that reported "allowed"
	// for arguments the runner would reject would be worse than no dry run.
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	req.Op = op
	pol := policyFor(c, repoPath, RepoSlug(remoteURL(c, repoPath, firstNonEmpty(req.Remote, "origin"))))
	v := pol.Evaluate(req)
	o := runOpts(c, false)
	shown := mask("git "+strings.Join(userArgs, " "), o.Masks)
	if !v.Allow {
		return deniedEnvelope(v, shown), nil
	}
	return envelope(Result{
		OK:      true,
		Command: shown,
		Stdout:  "dry run: the policy allows this command; nothing was executed",
	}, v, nil), nil
}

// remoteURL reads a remote's configured URL. Failure yields "", which makes the
// repo slug empty so only path-based policy rules can match — a safe default.
func remoteURL(c *connector.Ctx, repoPath, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	res, err := Run(c.Context(),
		Cmd{RepoPath: repoPath, UserArgs: []string{"remote", "get-url", "--", remote}},
		runOpts(c, false))
	if err != nil || !res.OK {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}
