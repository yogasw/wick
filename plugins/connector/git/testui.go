// testui.go implements the "Test against a repository" widget: point the
// connector at a real repository, run a read-only check, and see what it actually
// did — policy verdict, effective remote, and the exact git command.
//
// Why this exists next to the unit tests: those prove the pieces in isolation
// against a temp repo. This proves the assembled thing against YOUR repository
// with THIS instance's credential and policy. The failures it catches are the ones
// that only exist in a real setup — a token missing a scope, a Bitbucket username
// that is actually an email, an SSH remote whose host is not in the map, a branch
// pattern nobody can satisfy.
//
// Two checks, and the split is deliberate. The READ check only resolves and
// probes: nothing is fetched, committed or pushed. The WRITE check does the one
// thing reading cannot prove — that the token may actually publish — but it does it
// inside the plugin's own throwaway sandbox and then deletes it, so no repository
// on disk is ever touched. Neither button is wired to a real operation: those stay
// behind the agent surface, where each destructive one is opted into per instance,
// because a config page is exactly where someone clicks to find out what a button
// does. The write check's push is still policy-gated for the same reason.
//
// The target may be a clone URL or a local checkout path. A URL matters most: when
// a token is new there is no checkout yet, so demanding one would block the check
// precisely when it is most wanted.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// sandboxDir is the throwaway working directory for the write test.
//
// It lives under the plugin's own data dir — <appdata>/plugins/git/test — and
// deliberately NOT inside <appdata>/plugins/connectors/git/, because installing
// the plugin does `rm -rf` on that folder: anything kept there is destroyed on
// every rebuild, and worse, a half-finished unzip can leave the install itself
// broken. DataDir sits alongside connectors/ for exactly this reason.
func sandboxDir() string {
	return filepath.Join(wickplugin.DataDir(Key), "test")
}

// resetSandbox removes any leftovers and returns a fresh empty directory.
//
// Cleaning on the way IN as well as out matters: a run that is killed halfway
// (timeout, process restart) leaves a directory behind, and the next run must not
// inherit it or a stale clone would silently be tested instead of a new one.
func resetSandbox() (string, error) {
	dir := sandboxDir()
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear the test sandbox at %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create the test sandbox at %s: %w", dir, err)
	}
	return dir, nil
}

// resolveTestTarget works out what the operator typed and produces the URL the
// checks should use.
//
// Two shapes are accepted in one field, because both are natural starting points
// and demanding the wrong one blocks the commonest case:
//
//   - A clone URL. This is what you have when a token is new and the question is
//     simply "can this credential reach that repository?" — there is no checkout
//     yet, so requiring one would make the check impossible exactly when it is
//     most wanted.
//   - A local checkout path. Handy when the repository is already on disk and the
//     question is about ITS remote, whatever that happens to be.
//
// localPath is empty for the URL form; callers that need a working tree (reading
// the current branch) skip that step rather than inventing one.
func resolveTestTarget(c *connector.Ctx, target, remote string) (info RemoteInfo, localPath string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return RemoteInfo{}, "", fmt.Errorf(
			"enter a clone URL (https://host/org/repo.git) or the path of a checkout on this machine")
	}

	hostMap := ParseHostMap(c.Cfg("remote_host_map"))
	convert := c.CfgBool("convert_ssh_remote_to_https")

	// A URL is anything git would treat as a remote rather than a directory. The
	// same helpers the operations use decide, so the answer cannot drift from what
	// a real clone would do.
	if isHTTPURL(target) || (!isLocalPathRemote(target) && strings.Contains(target, ":")) {
		info, err = ConvertRemote(target, hostMap, convert)
		return info, "", err
	}

	// Otherwise treat it as a checkout and read its remote.
	if verr := validateRepo(c, target); verr != nil {
		return RemoteInfo{}, "", verr
	}
	raw := remoteURL(c, target, remote)
	if raw == "" {
		return RemoteInfo{}, target, fmt.Errorf(
			"the checkout at %s has no remote called %q — name a different one, or paste a clone URL instead",
			target, remote)
	}
	info, err = ConvertRemote(raw, hostMap, convert)
	return info, target, err
}

// testGuideInput drives the test widget. Values come from the named inputs in the
// markup it returns; browser carries the field's own value by the html= convention.
type testGuideInput struct {
	Browser  string `wick:"desc=Current field value, supplied by the config UI."`
	RepoPath string `wick:"desc=Repository to test: either a clone URL (https://host/org/repo.git) or the path of a checkout on the machine running wick."`
	Remote   string `wick:"desc=Remote name to resolve and probe. Default: origin."`
	Branch   string `wick:"desc=Branch name to evaluate the policy against."`
	Message  string `wick:"desc=Commit message to evaluate. Optional — only meaningful once a commit rule exists."`
}

// doTestPanel renders the form, restoring whatever was last entered.
func doTestPanel(c *connector.Ctx) (any, error) {
	// Render from the stored values rather than blank. The manager re-runs this op
	// whenever it re-mounts the widget, and returning an empty form at that moment
	// is what silently erased the operator's input.
	return map[string]any{"html": renderTestPanel(storedTestForm(c))}, nil
}

// storedTestForm rebuilds the form state from the hidden config rows, or nil when
// nothing has been entered yet.
func storedTestForm(c *connector.Ctx) *testReport {
	repo := strings.TrimSpace(c.Cfg("test_repo"))
	remote := strings.TrimSpace(c.Cfg("test_remote"))
	branch := strings.TrimSpace(c.Cfg("test_branch"))
	message := strings.TrimSpace(c.Cfg("test_message"))
	if repo == "" && remote == "" && branch == "" && message == "" {
		return nil
	}
	return &testReport{
		RepoPath: repo,
		Remote:   firstNonEmpty(remote, "origin"),
		Branch:   branch,
		Message:  message,
	}
}

// formFields is the {fields} map that persists what was just entered. Returned
// alongside the HTML on every run so the inputs outlive a re-mount.
func formFields(rep *testReport) map[string]string {
	return map[string]string{
		"test_repo":    rep.RepoPath,
		"test_remote":  rep.Remote,
		"test_branch":  rep.Branch,
		"test_message": rep.Message,
	}
}

// testReport is one run's findings. Each check is a named step with a verdict, so
// a failure says which stage broke rather than returning one opaque error.
type testReport struct {
	RepoPath string
	Remote   string
	Branch   string

	// Message is the commit message to judge. Optional: only a commit rule gives it
	// meaning, and inventing one would report a verdict on something the operator
	// never wrote.
	Message string
	Checks  []testCheck

	// Write marks a report from the write test, so the panel can say which of the
	// two ran — the check lists differ and a reader should not have to infer it.
	Write bool
}

// testCheck is one step. Status is "ok", "warn" or "fail"; Detail is what the
// operator needs to act on.
type testCheck struct {
	Name   string
	Status string
	Detail string
	Mono   string // command, URL, or raw output — rendered in a monospace block
}

func (r *testReport) add(name, status, detail, mono string) {
	r.Checks = append(r.Checks, testCheck{Name: name, Status: status, Detail: detail, Mono: mono})
}

// doTestRun executes the checks and re-renders the widget with the report.
//
// It runs the same functions the operations use — ValidateRepoPath, remoteURL,
// ConvertRemote, policyFor, Evaluate, Run — so a pass here means the real path
// works, not that a parallel test path works.
func doTestRun(c *connector.Ctx) (any, error) {
	rep := &testReport{
		RepoPath: strings.TrimSpace(c.Input("repo_path")),
		Remote:   firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin"),
		Branch:   strings.TrimSpace(c.Input("branch")),
		Message:  strings.TrimSpace(c.Input("message")),
	}

	// 1. git present at all. Everything else is moot without it.
	gitPath, err := ResolveGit()
	if err != nil {
		rep.add("git binary", "fail", err.Error(), "")
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	rep.add("git binary", "ok", "Found on PATH.", gitPath)

	// 2. Work out what was typed — a clone URL or a checkout path — and resolve it
	// to the URL the operations would really use.
	info, local, rerr := resolveTestTarget(c, rep.RepoPath, rep.Remote)
	if rerr != nil {
		rep.add("Repository", "fail", rerr.Error(), "")

		// A policy problem has nothing to do with the remote, so report it even when
		// resolution failed. Returning outright hid a broken branch pattern from
		// exactly the operator most likely to have one: someone setting up a
		// repository that has no remote configured yet.
		if local != "" {
			runTestPolicy(c, rep, info, local)
		}
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}

	switch local {
	case "":
		rep.add("Repository", "ok", "Testing a clone URL directly — no checkout needed.", info.Original)
	default:
		rep.add("Repository", "ok",
			fmt.Sprintf("Local checkout; its %q remote is what gets tested.", rep.Remote), local)

		// 3. Current branch. Only meaningful for a checkout, and it tells the
		// operator what a commit there would target.
		switch current := currentBranch(c, local); current {
		case "":
			rep.add("Current branch", "warn",
				"Detached HEAD — there is no current branch, so commit and push need one named explicitly.", "")
		default:
			rep.add("Current branch", "ok", "", current)
			if rep.Branch == "" {
				rep.Branch = current
			}
		}
	}

	// 4. What the network operations will actually talk to. An SSH host with no map
	// entry, or a credential baked into .git/config, shows up here.
	detail := "Used as-is."
	if info.Converted {
		detail = "Converted from SSH to HTTPS for network operations. Nothing on disk is modified."
	}
	if isHTTPURL(info.Original) && strings.Contains(info.Original, "@") {
		detail += " The credential embedded in this URL is ignored; the connector's own token is used instead."
	}
	rep.add("Effective URL", "ok", detail, info.Effective)

	// 5. Live credential check. ls-remote is the cheapest real probe: it asks the
	// remote what branches exist and changes nothing.
	runTestLsRemote(c, rep, info, local)

	// 6. Policy, evaluated for this repository. Runs even when the network part
	// failed, because a policy problem is worth seeing either way.
	runTestPolicy(c, rep, info, local)

	return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
}

// runTestLsRemote probes the remote with the configured credential.
//
// ls-remote against an explicit URL needs no repository, but Run still sets the
// process working directory — so when the operator gave a URL rather than a
// checkout, run it from the sandbox root instead of an empty path.
func runTestLsRemote(c *connector.Ctx, rep *testReport, info RemoteInfo, local string) {
	o := runOpts(c, true)
	if o.Auth.Token == "" {
		rep.add("Credential", "warn",
			"No token set. This works for a public repository over HTTPS; a private one will fail to authenticate.", "")
	}

	dir := local
	if dir == "" {
		d, err := resetSandbox()
		if err != nil {
			rep.add("Credential", "fail", err.Error(), "")
			return
		}
		defer func() { _ = os.RemoveAll(d) }()
		dir = d
	}

	// The URL, deliberately, unlike the real operations which name the remote: this
	// check has to work when the operator supplied only a URL and there is no checkout
	// to hold a remote. Nothing here reads or writes tracking refs, so the reason the
	// operations need a name does not apply.
	res, err := Run(c.Context(), Cmd{
		RepoPath:     dir,
		InjectedArgs: injectedArgs(c, o.Auth, "ls_remote", info.RewriteArgs),
		UserArgs:     []string{"ls-remote", "--heads", "--end-of-options", info.Effective},
		Network:      true,
	}, o)

	switch {
	case err != nil:
		rep.add("Credential", "fail", err.Error(), "")
	case !res.OK:
		// git's own message is the useful part here: it distinguishes a bad token
		// from a missing repository from a network block.
		rep.add("Credential", "fail",
			"The remote refused the request. git said:", strings.TrimSpace(res.Stderr))
	default:
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		rep.add("Credential", "ok",
			fmt.Sprintf("Authenticated. The remote advertises %d branch(es). Nothing was fetched or changed.", n),
			firstLines(res.Stdout, 5))
	}
}

// runTestPolicy reports the compiled policy and what it would decide, without
// running anything.
func runTestPolicy(c *connector.Ctx, rep *testReport, info RemoteInfo, local string) {
	// Policy rules match on the repo slug AND the local path, so pass whichever the
	// operator actually gave. With a URL there is no path, and only slug rules can
	// match — which is the truthful answer for a repository not on this machine.
	pol := policyFor(c, local, info.Slug)

	if pol.PolicyErr != "" {
		rep.add("Policy config", "fail",
			"Mutating operations are blocked until this is fixed. Read operations still work.", pol.PolicyErr)
		return
	}

	eff := "branch pattern:  " + orNone(pol.BranchPattern) +
		"\ncommit message:  " + orNone(pol.MessagePattern) +
		"\nprotected:       " + orNone(strings.Join(pol.Protected, ", ")) +
		"\nforce push:      " + map[bool]string{true: "allowed", false: "denied"}[pol.AllowForcePush] +
		"\nmatched rule:    " + pol.MatchedRule
	rep.add("Policy in effect", "ok", "", eff)

	if rep.Branch == "" {
		rep.add("Policy verdict", "warn",
			"No branch to judge. Enter one, or check out a branch in the repository.", "")
		return
	}

	// A push is the operation people most want to predict, so that is what the
	// verdict reports. Same Evaluate the real op calls.
	v := pol.Evaluate(Request{Op: "push", Branch: rep.Branch})
	if v.Allow {
		rep.add("Policy verdict", "ok",
			fmt.Sprintf("A push to %q would be allowed by %s.", rep.Branch, v.MatchedRule), "")
	} else {
		rep.add("Policy verdict", "fail", v.Reason, "matched rule: "+v.MatchedRule)
	}

	/* Commit verdict, when there is something to judge.
	   Reported separately from the push verdict because the two are gated by
	   different rules — a message can be rejected on a branch that pushes fine, and
	   the operator needs to know which one refused. Silent unless a message was
	   entered, so an unused field does not add a line to every report. */
	switch {
	case rep.Message == "":
		if pol.MessagePattern != "" {
			rep.add("Commit message", "warn",
				"A commit message rule is configured but no message was entered, so it was not checked.",
				pol.MessagePattern)
		}
	default:
		v := pol.Evaluate(Request{Op: "commit", Branch: rep.Branch, Message: rep.Message})
		if v.Allow {
			detail := "Accepted."
			if pol.MessagePattern == "" {
				detail = "Accepted — no commit message rule is configured, so any message passes."
			}
			rep.add("Commit message", "ok", detail, rep.Message)
		} else {
			rep.add("Commit message", "fail", v.Reason, rep.Message)
		}
	}

	// And the command it would run, assembled but not executed. The remote NAME, to
	// match what doPush actually emits — previewing a URL here would advertise the very
	// shape that broke upstream tracking.
	args := buildPushArgs(firstNonEmpty(rep.Remote, "origin"), rep.Branch, false, false)
	rep.add("Push command (not run)", "ok", "",
		mask("git "+strings.Join(args, " "), runOpts(c, true).Masks))
}

// renderTestPanel renders the form, plus the report when there is one.
//
// Unlike the setup guide this widget legitimately needs the backend — it has to
// actually run git — so the button carries data-op and a round-trip is expected.
// The form values are preserved across it so a failed run can be corrected and
// retried without retyping.
func renderTestPanel(rep *testReport) string {
	const p = "wgtt"

	repoVal, remoteVal, branchVal, messageVal := "", "origin", "", ""
	if rep != nil {
		repoVal, remoteVal, branchVal, messageVal = rep.RepoPath, rep.Remote, rep.Branch, rep.Message
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<style>
.%[1]s-w{color:%[2]s;font-size:13px}
.%[1]s-form{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:7px;margin-bottom:8px}
.%[1]s-l{display:block;font-size:11px;opacity:.6;margin-bottom:2px}
.%[1]s-in{width:100%%;font-family:monospace;font-size:12px;padding:5px 6px;border:1px solid %[3]s;border-radius:5px;background:%[4]s;color:%[2]s}
.%[1]s-btns{display:flex;gap:6px;flex-wrap:wrap}
.%[1]s-btn{cursor:pointer;font-size:12px;padding:6px 12px;border-radius:6px;border:1px solid %[5]s;background:%[5]s;color:#fff;font-weight:600}
.%[1]s-btn2{background:transparent;color:%[2]s;border-color:%[3]s}
.%[1]s-note{font-size:11px;opacity:.6;margin-top:6px}
.%[1]s-row{display:flex;gap:8px;padding:8px 0;border-top:1px solid %[3]s;align-items:flex-start}
.%[1]s-badge{flex:0 0 auto;width:16px;height:16px;border-radius:50%%;display:flex;align-items:center;justify-content:center;font-size:10px;font-family:monospace;color:#fff}
.%[1]s-ok{background:%[5]s}
.%[1]s-warn{background:#D9A03C}
.%[1]s-fail{background:%[6]s}
.%[1]s-body{flex:1 1 auto;min-width:0}
.%[1]s-name{font-weight:600;font-size:12px}
.%[1]s-detail{font-size:12px;opacity:.75;margin-top:1px}
.%[1]s-mono{margin-top:4px;font-family:monospace;font-size:11px;background:%[7]s;border:1px solid %[3]s;border-radius:4px;padding:5px 7px;overflow-x:auto;white-space:pre}
.%[1]s-sum{font-weight:600;font-size:12px;margin:10px 0 2px}
</style><div class="%[1]s-w">`, p, uiText, uiBorder, uiPanel, uiOK, uiBad, uiSunken)

	// Form. Named inputs are collected and sent as input.<name> on the click.
	fmt.Fprintf(&b, `<div class="%s-form">`, p)
	field := func(name, label, ph, val string) {
		fmt.Fprintf(&b, `<div><label class="%[1]s-l" for="%[1]s-%[2]s">%[3]s</label>`+
			`<input class="%[1]s-in" id="%[1]s-%[2]s" name="%[2]s" placeholder="%[4]s" value="%[5]s"/></div>`,
			p, esc(name), esc(label), esc(ph), esc(val))
	}
	field("repo_path", "Clone URL or checkout path", "https://github.com/org/repo.git", repoVal)
	field("remote", "Remote", "origin", remoteVal)
	field("branch", "Branch to check", "fix/my-change", branchVal)
	// Optional, and only meaningful once a commit rule exists — leaving it blank
	// keeps the report from claiming a verdict on a message nobody supplied.
	field("message", "Commit message (optional)", "fix: something", messageVal)
	fmt.Fprintf(&b, `</div>`)

	// type="button" is required, not cosmetic. A <button> with no type defaults to
	// type="submit", and this markup renders inside the manager's config form — so
	// the click submitted the form, reloaded it, wiped every field, and the op never
	// ran. Exactly the "nothing happens and my input disappears" symptom.
	fmt.Fprintf(&b, `<div class="%[1]s-btns">`+
		`<button type="button" class="%[1]s-btn" data-op="test_run">Run read check</button>`+
		`<button type="button" class="%[1]s-btn %[1]s-btn2" data-op="test_write">Run write check</button></div>`, p)

	fmt.Fprintf(&b, `<div class="%[1]s-note"><b>Read check</b> resolves the remote, authenticates `+
		`with ls-remote and evaluates the policy. Nothing is fetched, committed or pushed.<br/>`+
		`<b>Write check</b> clones into a throwaway folder, commits, pushes the branch you named, `+
		`then deletes the folder. It never touches the repository on disk, and the push still has to `+
		`pass the policy. One branch is left on the remote for you to delete.</div>`, p)

	if rep == nil {
		fmt.Fprintf(&b, `</div>`)
		return b.String()
	}

	// Report. Worst status first in the summary so a failure is not buried.
	fails, warns := 0, 0
	for _, c := range rep.Checks {
		switch c.Status {
		case "fail":
			fails++
		case "warn":
			warns++
		}
	}
	summary := "All checks passed."
	switch {
	case fails > 0:
		summary = fmt.Sprintf("%d check(s) failed.", fails)
	case warns > 0:
		summary = fmt.Sprintf("%d warning(s).", warns)
	}
	which := "Read check"
	if rep.Write {
		which = "Write check"
	}
	fmt.Fprintf(&b, `<div class="%s-sum">%s — %s</div>`, p, esc(which), esc(summary))

	for _, c := range rep.Checks {
		mark := "✓"
		switch c.Status {
		case "warn":
			mark = "!"
		case "fail":
			mark = "✕"
		}
		fmt.Fprintf(&b, `<div class="%[1]s-row"><span class="%[1]s-badge %[1]s-%[2]s">%[3]s</span>`+
			`<div class="%[1]s-body"><div class="%[1]s-name">%[4]s</div>`,
			p, esc(c.Status), mark, esc(c.Name))
		if c.Detail != "" {
			fmt.Fprintf(&b, `<div class="%[1]s-detail">%[2]s</div>`, p, esc(c.Detail))
		}
		if c.Mono != "" {
			fmt.Fprintf(&b, `<div class="%[1]s-mono">%[2]s</div>`, p, esc(c.Mono))
		}
		fmt.Fprintf(&b, `</div></div>`)
	}

	fmt.Fprintf(&b, `</div>`)
	return b.String()
}

// doTestWrite runs the full write path in a throwaway clone: clone, branch,
// commit, push, then delete the whole directory.
//
// This is the check the read-only one cannot make. A token with `repo` selected
// but no write permission, a branch pattern nobody can satisfy, a protected-branch
// rule that blocks the only branch anyone uses — all of those pass every read
// check and fail the first time someone actually tries to publish.
//
// Two rules make it safe to put behind a button on a config page:
//
//   - It never touches a repository on disk. It clones into the plugin's own
//     sandbox, works there, and removes it — so the worst outcome is a wasted
//     clone, never a modified checkout.
//   - The push goes through the SAME policy evaluation as the real operation. A
//     test button that could push to a protected branch would be a hole straight
//     through the policy engine, so a denial here is reported as a denial, not
//     bypassed.
//
// What it does leave behind, deliberately, is one branch on the remote. Deleting
// it would need another destructive push, and a leftover test branch is easy to
// see and remove; a test that silently deletes remote refs is not.
func doTestWrite(c *connector.Ctx) (any, error) {
	rep := &testReport{
		RepoPath: strings.TrimSpace(c.Input("repo_path")),
		Remote:   firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin"),
		Branch:   strings.TrimSpace(c.Input("branch")),
		Message:  strings.TrimSpace(c.Input("message")),
		Write:    true,
	}

	if _, err := ResolveGit(); err != nil {
		rep.add("git binary", "fail", err.Error(), "")
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	// Resolve up front: without a URL there is nothing to clone from or push to, and
	// the error reads better here than mid-way through.
	info, local, rerr := resolveTestTarget(c, rep.RepoPath, rep.Remote)
	if rerr != nil {
		rep.add("Repository", "fail", rerr.Error(), "")
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	source := "clone URL"
	if local != "" {
		source = fmt.Sprintf("the %q remote of %s", rep.Remote, local)
	}
	rep.add("Clone source", "ok", "Resolved from "+source+".", info.Effective)

	// The branch to publish. A policy usually demands a pattern, so let the
	// operator supply a name that satisfies theirs rather than inventing one that
	// probably will not.
	if rep.Branch == "" {
		rep.add("Branch", "fail",
			"Enter a branch name to create. It must satisfy this connector's branch pattern.", "")
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}

	// Policy first, before anything is written. A denial must stop the test, not be
	// worked around by it.
	pol := policyFor(c, local, info.Slug)
	if v := pol.Evaluate(Request{Op: "branch_create", Branch: rep.Branch, NewBranch: true}); !v.Allow {
		rep.add("Policy: create branch", "fail", v.Reason, "matched rule: "+v.MatchedRule)
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	if v := pol.Evaluate(Request{Op: "push", Branch: rep.Branch}); !v.Allow {
		rep.add("Policy: push", "fail", v.Reason, "matched rule: "+v.MatchedRule)
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	rep.add("Policy", "ok",
		fmt.Sprintf("Creating and pushing %q is allowed by %s.", rep.Branch, pol.MatchedRule), "")

	dir, err := resetSandbox()
	if err != nil {
		rep.add("Sandbox", "fail", err.Error(), "")
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}
	// Removal has to happen before the report is rendered, so the operator can see
	// that it worked — a deferred cleanup runs after the return value is already
	// built, which removed the directory but left "Cleanup" out of the HTML.
	//
	// finish is therefore called on every exit path below instead of deferred. The
	// belt-and-braces defer stays for the paths a panic could take: it repeats the
	// removal (harmless — RemoveAll on a missing path succeeds) without touching
	// the report.
	defer func() { _ = os.RemoveAll(dir) }()

	finish := func() (any, error) {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			rep.add("Cleanup", "warn", "Could not remove the sandbox. Delete it by hand.", dir)
		} else {
			rep.add("Cleanup", "ok", "Sandbox removed.", dir)
		}
		return map[string]any{"html": renderTestPanel(rep), "fields": formFields(rep)}, nil
	}

	work := filepath.Join(dir, "clone")
	o := runOpts(c, true)

	// 1. Clone. Shallow and single-branch: the test needs a working tree, not
	// history, and a big repository should not mean a big wait.
	if !runTestStep(c, rep, o, "Clone", dir, true,
		"clone", "--quiet", "--depth", "1", "--single-branch", "--end-of-options", info.Effective, work) {
		return finish()
	}

	// 2. Branch. No --end-of-options: -b binds its own value, and the slot after
	// the name is checkout's start-point, where git reads the terminator as a
	// commit-ish and refuses.
	if !runTestStep(c, rep, o, "Create branch", work, false,
		"checkout", "-b", rep.Branch) {
		return finish()
	}

	// 3. A commit with a file that says what it is, so anyone who finds the branch
	// on the remote knows where it came from.
	stamp := filepath.Join(work, "wick-connector-test.txt")
	body := "Written by the wick git connector's write test.\n" +
		"Safe to delete, along with the branch it was pushed on.\n"
	if err := os.WriteFile(stamp, []byte(body), 0o644); err != nil {
		rep.add("Commit", "fail", err.Error(), "")
		return finish()
	}
	if !runTestStep(c, rep, o, "Stage", work, false, "add", "--", "wick-connector-test.txt") {
		return finish()
	}
	if !runTestStep(c, rep, o, "Commit", work, false,
		"commit", "-m", "test: wick git connector write check") {
		return finish()
	}

	// 4. Push — the step everything else exists to reach. "origin" is the remote the
	// clone in step 1 created, so this exercises the same named-remote path the real
	// push takes rather than a URL shortcut the operations no longer use.
	args := buildPushArgs("origin", rep.Branch, false, false)
	if !runTestStep(c, rep, o, "Push", work, true, args...) {
		return finish()
	}
	rep.add("Result", "ok",
		fmt.Sprintf("Branch %q was published to %s. Delete it when you are done — this test does not, "+
			"because removing a remote ref is itself a destructive push.", rep.Branch, rep.Remote), "")

	return finish()
}

// runTestStep runs one git command and records it. Returns false when the step
// failed, so the caller can stop rather than pile errors on a broken state.
func runTestStep(c *connector.Ctx, rep *testReport, o RunOpts, name, dir string,
	network bool, args ...string) bool {

	res, err := Run(c.Context(), Cmd{
		RepoPath: dir,
		// No rewrite: the sandbox was cloned from the already-converted, already-stripped
		// URL in step 1, so its origin carries no credential and needs no substitution.
		InjectedArgs: injectedArgs(c, o.Auth, "test_write"),
		UserArgs:     args,
		Network:      network,
	}, o)

	switch {
	case err != nil:
		rep.add(name, "fail", err.Error(), mask("git "+strings.Join(args, " "), o.Masks))
		return false
	case !res.OK:
		rep.add(name, "fail", "git said:", strings.TrimSpace(res.Stderr)+"\n\n"+res.Command)
		return false
	default:
		rep.add(name, "ok", "", res.Command)
		return true
	}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

// firstLines keeps output short — the widget reports a result, not a log.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %d more", len(lines)-n)
}
