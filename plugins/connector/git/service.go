// service.go bridges connector config into the pure types from policy.go and
// git.go. It is the only place that reads c.Cfg — everything downstream takes
// typed values, which keeps the policy engine and runner testable without a Ctx.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
		MessagePattern: strings.TrimSpace(c.Cfg("commit_message_pattern")),
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
//
// The refusal also points at policy_show. Evaluate stops at the first rule that
// fires, so a reason answers "why did this one fail" but never "what are the rules"
// — a caller left with only the reason fixes one violation, hits the next, and pays
// a round trip per rule. Naming the operation that answers in full turns that chain
// into one call.
func deniedEnvelope(v Verdict, command, op string) any {
	out := envelope(Result{OK: false, Command: command, ExitCode: -1}, v, nil)
	if m, ok := out.(map[string]any); ok {
		if pol, ok := m["policy"].(map[string]any); ok {
			pol["next_step"] = policySummary(op)
		}
	}
	return out
}

// execute is the shared path for every operation: validate the repo, compile the
// policy, evaluate, then run. build assembles the git arguments and runs after
// the policy passes, so a denied operation never even builds a command line.
func execute(c *connector.Ctx, op string, repoPath string, req Request,
	build func(EffectivePolicy) ([]string, error), network bool) (any, error) {

	if err := validateRepo(c, repoPath); err != nil {
		return nil, err
	}

	slug, warn, err := repoScope(c, repoPath, req.Remote)
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repoPath, slug)
	req.Op = op

	v := pol.Evaluate(req)
	if !v.Allow {
		return deniedEnvelope(v, "git "+op, op), nil
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
	return withScopeWarning(envelope(res, v, nil), warn), nil
}

// withScopeWarning attaches an unresolved-scope note to a response.
//
// Placed inside the policy block rather than at the top level: it is a caveat about
// the verdict, and a reader who trusts the verdict has to encounter it in the same
// place. It is absent entirely when there is nothing to report, so its presence is
// itself the signal.
func withScopeWarning(out any, warn string) any {
	if warn == "" {
		return out
	}
	if m, ok := out.(map[string]any); ok {
		if pol, ok := m["policy"].(map[string]any); ok {
			pol["unresolved_scope"] = warn
		}
	}
	return out
}

// injectedArgs are the plugin's own git options. They are never passed through
// ValidateUserArgs — that deny-list exists to filter agent input, and blocking
// our own injections would break credential handling and hook suppression.
// rewrite carries an optional SSH→HTTPS substitution. Variadic so the many callers
// that need no rewrite (commit, checkout, raw…) stay unchanged, and only the network
// operations pass one.
func injectedArgs(c *connector.Ctx, auth AuthSpec, op string, rewrite ...[]string) []string {
	args := AuthInjectedArgs(auth)
	for _, r := range rewrite {
		args = append(args, r...)
	}

	// SSH→HTTPS conversion, expressed as config rather than by substituting the URL
	// for the remote name on the command line.
	//
	// Substituting the URL is what the connector used to do, and it broke three
	// things at once, all downstream of git no longer knowing which remote it was
	// talking to:
	//
	//   - "fetch <url>" writes FETCH_HEAD and nothing else. refs/remotes/origin/* is
	//     never updated, so a branch pushed through the connector never appeared in
	//     branch_list remote=true.
	//   - "push --set-upstream <url>" sets branch.<b>.remote to the URL STRING, so
	//     status loses branch.upstream and branch.ab — ahead/behind stops working for
	//     every branch the connector creates.
	//   - "pull" with no branch has no upstream to resolve.
	//
	// insteadOf hands the rewrite to git instead: the command still says "origin", and
	// git substitutes the URL when it dials. Refspecs, upstream bookkeeping and
	// remote-tracking refs then work by construction rather than being reimplemented
	// here — the alternative was writing update-ref and branch.<b>.merge by hand,
	// which is re-doing what git already does correctly.
	//
	// The rewrite itself is computed by the caller, which is the only place that knows
	// the remote, and reaches here through RemoteRewrite.

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

// buildPushArgs assembles a push that names the REMOTE, not its URL.
//
// It used to pass the URL, to keep a credential embedded in .git/config from being
// used instead of the connector's own — a real risk, verified against git 2.52. But
// the cost was hidden and worse: with a URL here, "--set-upstream" records the URL
// STRING as branch.<b>.remote, so status loses branch.upstream and branch.ab and
// ahead/behind stops working for every branch the connector creates. The credential
// is now neutralised by RemoteInfo.RewriteArgs (a url.<clean>.insteadOf=<dirty> pair)
// so both properties hold at once.
//
// Force always means --force-with-lease: it refuses to overwrite commits the
// local clone has not seen, which is the difference between recovering from a
// bad rebase and destroying a colleague's work. Bare --force is never emitted.
//
// The remote and the refspec are positional, so --end-of-options precedes them.
// Verified against git 2.52: without it a remote of "--receive-pack=evil" is parsed
// as a flag and runs an arbitrary binary on the far end; with it, git reports
// "strange pathname blocked" instead.
func buildPushArgs(remote, branch string, force, setUpstream bool) []string {
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "--end-of-options", remote, "HEAD:refs/heads/"+branch)
	return args
}

// networkOp is the shared shape for fetch and pull: resolve the remote, convert
// it, evaluate the policy, then run against the explicit URL. build receives the
// effective URL and must place it after its own --end-of-options.
// networkOp is the shared path for fetch and pull.
//
// build receives the REMOTE NAME, not the URL. That is the whole of the P1 fix: git
// only updates refs/remotes/<remote>/* and only records upstream when it knows which
// remote it is talking to, and it cannot know that from a URL. The SSH→HTTPS rewrite
// still happens, as injected config (RemoteInfo.RewriteArgs), so git dials the
// converted URL while the command line keeps naming the remote.
func networkOp(c *connector.Ctx, op string, build func(remote string) []string) (any, error) {
	repo := c.Input("repo_path")
	if err := validateRepo(c, repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	// info.Slug comes from the remote URL that was just read, so it is only empty when
	// the URL itself carried no host — a local path remote. repoScope is not consulted
	// here because ConvertRemote already failed loudly if the remote was unreadable.
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: op, Branch: currentBranch(c, repo), Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git "+op+" "+remote, op), nil
	}
	userArgs := build(remote)
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, op, info.RewriteArgs),
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
	if err := validateRepo(c, repoPath); err != nil {
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
		return deniedEnvelope(v, shown, req.Op), nil
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

// repoScope resolves the two identifiers a policy is matched against, distinguishing
// the two reasons a slug can be missing.
//
// They are not the same thing and must not be treated the same:
//
//   - The repository has NO remote. A legitimate state for a local-only checkout, and
//     rules written against paths still match it. Resolution proceeds under the global
//     fallback, and warn explains which stricter rules could not be evaluated so a
//     permissive verdict never arrives silently.
//   - The named remote does NOT EXIST, or its URL could not be read. That is a broken
//     request, not a configuration: the operator asked about a remote that is not
//     there. Erroring here is what keeps a typo from being answered with the global
//     fallback — and the error names the real remotes, which the caller has no other
//     way to discover.
//
// Returning an error for the second case costs nothing in usability, because every
// operation that talks to that remote was going to fail anyway. What it buys is that
// the failure says why.
func repoScope(c *connector.Ctx, repoPath, remote string) (slug, warn string, err error) {
	named := firstNonEmpty(remote, "origin")
	if url := remoteURL(c, repoPath, named); url != "" {
		return RepoSlug(url), "", nil
	}

	existing := listRemotes(c, repoPath)
	if len(existing) > 0 {
		return "", "", fmt.Errorf(
			"remote %q does not exist in this repository, which has: %s",
			named, strings.Join(existing, ", "))
	}

	// No remotes at all: proceed, but say what that cost.
	rules, _ := ParseRepoRules(c.Cfg("repo_policies"))
	return "", scopeWarning(loadGlobal(c), rules, repoPath, ""), nil
}

// intInput reads a numeric input, applying def when it was not supplied and REFUSING a
// value that cannot mean anything.
//
// Two distinctions the old code lost, both by going through InputInt, which returns 0
// for "absent", for "0" and for "abc" alike:
//
//   - Absent is not nonsensical. A missing limit legitimately means "use the default",
//     but "limit: -5" is a mistake, and quietly substituting 20 hid it: the caller saw
//     20 commits with no sign its argument had been discarded, so it would keep passing
//     -5 forever.
//   - Unparseable is not zero. "limit: abc" silently became the default too.
//
// max bounds values that drive a resource limit, so a caller cannot ask for output large
// enough to be its own problem. A max of 0 means unbounded.
func intInput(c *connector.Ctx, name string, def, max int) (int, error) {
	raw := strings.TrimSpace(c.Input(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}
	if n == 0 {
		// Explicit zero reads as "no opinion" for every one of these, and refusing it
		// would break a caller that fills every field in from a template.
		return def, nil
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be a positive number, got %d", name, n)
	}
	if max > 0 && n > max {
		return 0, fmt.Errorf("%s must be at most %d, got %d", name, max, n)
	}
	return n, nil
}

// validateRepo is ValidateRepoPath plus the configured root check.
//
// One function so the check cannot be forgotten at a call site: every operation that
// takes a repo_path already goes through the shape validation, so attaching the scope
// check to it means adding an operation cannot accidentally skip it.
func validateRepo(c *connector.Ctx, p string) error {
	if err := ValidateRepoPath(p); err != nil {
		return err
	}
	return CheckPathRoots(p, allowedRoots(c))
}

// allowedRoots reads the configured roots. Empty means unrestricted — see the config
// field's comment for why that is the default rather than a sandbox.
func allowedRoots(c *connector.Ctx) []string {
	rows, err := ParseKVList(c.Cfg("allowed_repo_roots"))
	if err != nil {
		// A malformed list is not a reason to allow everything: the operator meant to
		// restrict something. Returning one unresolvable root refuses every path, and the
		// message from CheckPathRoots names it.
		return []string{"<allowed_repo_roots is not valid JSON>"}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v := strings.TrimSpace(r["root"]); v != "" {
			out = append(out, v)
		}
	}
	return out
}
