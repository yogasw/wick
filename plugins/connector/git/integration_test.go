package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Integration tests drive the real operation handlers against a real git binary.
//
// The key move is that a git remote can be faked locally: a bare repository on
// disk is a fully functional remote, so push / fetch / pull / ls-remote can be
// exercised end to end with no network and no credentials. That is unusual — an
// HTTP connector has to choose between mocking the API and needing a live token —
// and it means the whole mutating surface is covered by default rather than
// skipped in CI.
//
// Two things a local remote CANNOT prove, because they only exist over HTTPS:
// credential injection (askpass) and SSH-to-HTTPS conversion against a real host.
// Those live in TestLiveRemote below, gated on WICK_GIT_TEST_REMOTE.

// gitInTest runs git directly, bypassing the connector. Used only to arrange
// fixtures and to assert on the remote's real state; never to exercise the code
// under test.
func gitInTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := runGitInTest(t, dir, args...); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// gitOutputInTest is gitInTest but returns stdout for assertions.
func gitOutputInTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitInTest(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return out
}

func runGitInTest(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	cmd := safeexec.Command(gitPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
		"GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// envelopeOf returns a checker bound to t. It has to be a closure rather than
// func(t, any, error) because Go cannot spread a two-value call into a function
// that already takes a leading argument — env(doPush(ctx)) does not compile.
//
// The envelope is asserted as a map so tests read the same shape an agent
// receives, not Go internals.
func envelopeOf(t *testing.T) func(any, error) map[string]any {
	t.Helper()
	return func(out any, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatalf("operation returned error: %v", err)
		}
		m, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("operation returned %T, want the map envelope", out)
		}
		return m
	}
}

func policyOf(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	p, ok := e["policy"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no policy block: %v", e)
	}
	return p
}

// bareRemote creates a bare repo that acts as a real remote, and wires it as
// "origin" of the given work tree.
func bareRemote(t *testing.T, workdir string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	// -b main matters: "git init --bare" points HEAD at refs/heads/master, so a
	// remote whose only branch is "main" would have a dangling HEAD and any fetch
	// without an explicit refspec fails with "couldn't find remote ref HEAD".
	// That is a fixture artefact, not connector behaviour.
	gitInTest(t, filepath.Dir(remote), "init", "--quiet", "--bare", "-b", "main", remote)
	gitInTest(t, workdir, "remote", "add", "origin", remote)
	return remote
}

// baseCfg is a permissive-but-realistic policy: feature branches required,
// master/main protected, force push denied, raw off.
func baseCfg() map[string]string {
	return map[string]string{
		"author_name":                 "Test Bot",
		"author_email":                "bot@example.com",
		"branch_name_pattern":         `^(fix|feat|chore)/[a-z0-9._/-]+$`,
		"protected_branches":          `[{"branch":"master"},{"branch":"main"}]`,
		"allow_force_push":            "false",
		"raw_enabled":                 "false",
		"convert_ssh_remote_to_https": "true",
		"timeout_seconds":             "60",
		"network_timeout_seconds":     "60",
		"max_output_bytes":            "262144",
	}
}

func TestIntegrationBranchCommitRoundTrip(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)
	cfg := baseCfg()

	// A branch whose name satisfies the pattern is created and checked out.
	e := env(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": repo, "name": "fix/login-timeout", "checkout": "true",
	})))
	if e["ok"] != true {
		t.Fatalf("branch_create failed: %v", e)
	}
	if got := policyOf(t, e)["verdict"]; got != "allow" {
		t.Errorf("verdict = %v, want allow", got)
	}

	// The commit lands on that branch, which is not protected.
	if err := os.WriteFile(filepath.Join(repo, "fix.txt"), []byte("patched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e = env(doAdd(opCtx(cfg, map[string]string{"repo_path": repo, "paths": "fix.txt"})))
	if e["ok"] != true {
		t.Fatalf("add failed: %v", e)
	}
	e = env(doCommit(opCtx(cfg, map[string]string{
		"repo_path": repo, "message": "fix: stop the login timeout",
	})))
	if e["ok"] != true {
		t.Fatalf("commit failed: %v", e)
	}

	// The commit is really there, and carries the configured identity rather than
	// whatever the machine's git config says.
	e = env(doLog(opCtx(cfg, map[string]string{"repo_path": repo, "limit": "1"})))
	out, _ := e["stdout"].(string)
	if !strings.Contains(out, "stop the login timeout") {
		t.Errorf("log does not show the new commit:\n%s", out)
	}
	if !strings.Contains(out, "Test Bot") {
		t.Errorf("commit author is not the configured identity:\n%s", out)
	}
}

func TestIntegrationBranchNameViolationIsBlocked(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)

	e := env(doBranchCreate(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "name": "temp-hack",
	})))
	if e["ok"] != false {
		t.Fatalf("branch_create succeeded for a name violating the pattern: %v", e)
	}
	// exit_code -1 is the marker that no process ran at all.
	if e["exit_code"] != float64(-1) && e["exit_code"] != -1 {
		t.Errorf("exit_code = %v, want -1 to show nothing was executed", e["exit_code"])
	}
	p := policyOf(t, e)
	if p["verdict"] != "deny" {
		t.Errorf("verdict = %v, want deny", p["verdict"])
	}
	reason, _ := p["reason"].(string)
	if !strings.Contains(reason, "pattern") {
		t.Errorf("reason = %q, want it to name the pattern", reason)
	}

	// The branch must not exist — a denied op changes nothing.
	e = env(doBranchList(opCtx(baseCfg(), map[string]string{"repo_path": repo})))
	if out, _ := e["stdout"].(string); strings.Contains(out, "temp-hack") {
		t.Errorf("denied branch_create still created the branch:\n%s", out)
	}
}

func TestIntegrationPushToLocalRemote(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)
	remote := bareRemote(t, repo)
	cfg := baseCfg()

	// Work on an allowed branch, then publish it.
	env(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": repo, "name": "feat/publish-me", "checkout": "true",
	})))
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env(doAdd(opCtx(cfg, map[string]string{"repo_path": repo, "paths": "feature.txt"})))
	env(doCommit(opCtx(cfg, map[string]string{"repo_path": repo, "message": "feat: publish me"})))

	e := env(doPush(opCtx(cfg, map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "feat/publish-me", "set_upstream": "true",
	})))
	if e["ok"] != true {
		t.Fatalf("push to a local bare remote failed: %v", e)
	}

	// The branch really arrived on the remote.
	got := gitOutputInTest(t, remote, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if !strings.Contains(got, "feat/publish-me") {
		t.Errorf("remote refs = %q, want the pushed branch", got)
	}
}

func TestIntegrationPushToProtectedBranchIsBlocked(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)
	remote := bareRemote(t, repo)

	// initTestRepo checks out "main", which baseCfg protects.
	e := env(doPush(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "main",
	})))
	if e["ok"] != false {
		t.Fatalf("push to a protected branch succeeded: %v", e)
	}
	if reason, _ := policyOf(t, e)["reason"].(string); !strings.Contains(reason, "protected") {
		t.Errorf("reason = %q, want it to name the protected branch", reason)
	}

	// Nothing reached the remote.
	if got := gitOutputInTest(t, remote, "for-each-ref", "refs/heads"); strings.TrimSpace(got) != "" {
		t.Errorf("remote received refs despite the denial: %q", got)
	}
}

func TestIntegrationForcePushNeedsOptIn(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)
	bareRemote(t, repo)

	env(doBranchCreate(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "name": "fix/rewrite", "checkout": "true",
	})))

	// Denied while allow_force_push is off.
	e := env(doPush(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/rewrite", "force": "true",
	})))
	if e["ok"] != false {
		t.Fatalf("force push allowed while allow_force_push is off: %v", e)
	}

	// Allowed once the policy opts in — and it must use --force-with-lease, never
	// a bare --force, so it cannot silently overwrite unseen work.
	cfg := baseCfg()
	cfg["allow_force_push"] = "true"
	e = env(doPush(opCtx(cfg, map[string]string{
		"repo_path": repo, "remote": "origin", "branch": "fix/rewrite",
		"force": "true", "dry_run": "true",
	})))
	cmd, _ := e["command"].(string)
	if !strings.Contains(cmd, "--force-with-lease") {
		t.Errorf("command = %q, want --force-with-lease", cmd)
	}
	for _, bare := range []string{" --force ", " -f "} {
		if strings.Contains(cmd+" ", bare) {
			t.Errorf("command uses a bare force flag: %q", cmd)
		}
	}
}

func TestIntegrationResetHardCannotBeSmuggledViaRef(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)

	// The attack: a soft reset needs no force opt-in, so a ref of "--hard" would
	// let it destroy the working tree while the policy says allow. Verified
	// against git 2.52: "reset --soft --hard" exits 0 and wipes uncommitted work.
	if err := os.WriteFile(filepath.Join(repo, "precious.txt"), []byte("do not lose me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env(doAdd(opCtx(baseCfg(), map[string]string{"repo_path": repo, "paths": "precious.txt"})))

	e := env(doReset(opCtx(baseCfg(), map[string]string{
		"repo_path": repo, "mode": "soft", "ref": "--hard",
	})))
	if e["ok"] == true {
		t.Errorf("git accepted an injected --hard: %v", e)
	}

	// The file survived, which is the assertion that actually matters.
	if _, err := os.Stat(filepath.Join(repo, "precious.txt")); err != nil {
		t.Fatalf("working tree was destroyed by an injected --hard: %v", err)
	}
}

func TestIntegrationRawIsDeniedUntilAllowListed(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)

	// Off by default.
	e := env(doRaw(opCtx(baseCfg(), map[string]string{"repo_path": repo, "args": "describe --tags"})))
	if e["ok"] != false {
		t.Fatalf("raw ran while raw_enabled is false: %v", e)
	}

	// Enabled but the subcommand is not listed → still denied (fail closed).
	cfg := baseCfg()
	cfg["raw_enabled"] = "true"
	cfg["raw_rules"] = `[{"subcommand":"blame","mode":"allow"}]`
	e = env(doRaw(opCtx(cfg, map[string]string{"repo_path": repo, "args": "describe --tags"})))
	if e["ok"] != false {
		t.Fatalf("unlisted subcommand ran: %v", e)
	}

	// Explicitly allow-listed → runs.
	cfg["raw_rules"] = `[{"subcommand":"describe","mode":"allow"}]`
	e = env(doRaw(opCtx(cfg, map[string]string{"repo_path": repo, "args": "describe --tags --always"})))
	if got := policyOf(t, e)["verdict"]; got != "allow" {
		t.Errorf("verdict = %v, want allow for an allow-listed subcommand (%v)", got, e)
	}
}

func TestIntegrationFetchAndPullFromLocalRemote(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)

	// Producer publishes main; consumer fetches and pulls it.
	producer := initTestRepo(t)
	remote := bareRemote(t, producer)
	cfg := baseCfg()
	// main is protected by baseCfg, so publish it with plain git — protection is
	// covered by its own test above.
	gitInTest(t, producer, "push", "--quiet", remote, "HEAD:refs/heads/main")

	consumer := t.TempDir()
	gitInTest(t, filepath.Dir(consumer), "clone", "--quiet", remote, consumer)

	// fetch only moves remote-tracking refs, so it is allowed even on a protected
	// branch — there is no local branch for the rule to protect.
	e := env(doFetch(opCtx(cfg, map[string]string{"repo_path": consumer, "remote": "origin"})))
	if e["ok"] != true {
		t.Fatalf("fetch failed: %v", e)
	}
	// The effective remote is reported so a wrong host is visible immediately.
	if r, ok := e["remote"].(map[string]any); !ok || r["effective"] == "" {
		t.Errorf("fetch envelope does not report the effective remote: %v", e["remote"])
	}

	// pull DOES integrate into the current branch, so on a protected branch it is
	// correctly refused. This is the asymmetry between fetch and pull, and it is
	// worth pinning: it would be easy to "fix" pull into allowing this.
	e = env(doPull(opCtx(cfg, map[string]string{"repo_path": consumer, "remote": "origin"})))
	if e["ok"] != false {
		t.Fatalf("pull onto protected branch main succeeded: %v", e)
	}
	if reason, _ := policyOf(t, e)["reason"].(string); !strings.Contains(reason, "protected") {
		t.Errorf("reason = %q, want it to name the protected branch", reason)
	}

	// On an unprotected branch the same pull goes through, proving the refusal
	// above came from the policy and not from a broken remote.
	gitInTest(t, consumer, "checkout", "--quiet", "-b", "fix/local-work")
	e = env(doPull(opCtx(cfg, map[string]string{
		"repo_path": consumer, "remote": "origin", "branch": "main",
	})))
	if e["ok"] != true {
		t.Fatalf("pull onto an unprotected branch failed: %v", e)
	}
}

func TestIntegrationCloneFromLocalRemote(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	source := initTestRepo(t)
	remote := bareRemote(t, source)
	gitInTest(t, source, "push", "--quiet", remote, "HEAD:refs/heads/main")

	dest := filepath.Join(t.TempDir(), "cloned")
	// A local path is not an SSH remote, so it is passed through untouched no
	// matter how convert_ssh_remote_to_https is set.
	cfg := baseCfg()

	e := env(doClone(opCtx(cfg, map[string]string{"url": remote, "dest": dest})))
	if e["ok"] != true {
		t.Fatalf("clone failed: %v", e)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("clone reported success but produced no repo: %v", err)
	}

	// A second clone into the same path must be refused rather than clobber it.
	if _, err := doClone(opCtx(cfg, map[string]string{"url": remote, "dest": dest})); err == nil {
		t.Error("clone into an existing directory was allowed")
	}
}

func TestIntegrationRemoteListReportsEffectiveURL(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)

	// A remote with credentials baked into the URL: the connector must ignore
	// them, never echo them back, and never rewrite .git/config.
	gitInTest(t, repo, "remote", "add", "origin",
		"https://olduser:oldsecret@abc.com/org/repo.git")

	out, err := doRemoteList(opCtx(baseCfg(), map[string]string{"repo_path": repo}))
	if err != nil {
		t.Fatalf("remote_list: %v", err)
	}
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), "oldsecret") {
		t.Errorf("remote_list echoed the embedded credential: %s", blob)
	}

	// .git/config still holds the original, untouched.
	cfgPath := filepath.Join(repo, ".git", "config")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "oldsecret") {
		t.Error(".git/config was rewritten — the connector must never modify it")
	}
}

func TestIntegrationPolicySimulatorAgreesWithRealOps(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	repo := initTestRepo(t)

	cfg := baseCfg()
	cfg["repo_policies"] = `[{"repo":"*/org/infra","branch_pattern":"^ops/.+$","protected":"master,main","force_push":"false"}]`

	// Whatever the simulator says for a case, the real op must do the same. This
	// is the property that makes the widget trustworthy.
	for _, tc := range []struct{ branch string }{
		{"fix/allowed"}, {"nope"}, {"main"},
	} {
		simOut, err := doPolicySimulate(opCtx(cfg, map[string]string{
			"sim_repo": repo, "sim_op": "branch_create", "sim_branch": tc.branch,
		}))
		if err != nil {
			t.Fatalf("policy_simulate(%q): %v", tc.branch, err)
		}
		html, _ := simOut.(map[string]any)["html"].(string)
		simAllowed := strings.Contains(html, "ALLOWED") && !strings.Contains(html, "DENIED")

		realOut := env(doBranchCreate(opCtx(cfg, map[string]string{
			"repo_path": repo, "name": tc.branch,
		})))
		realAllowed := policyOf(t, realOut)["verdict"] == "allow"

		if simAllowed != realAllowed {
			t.Errorf("branch %q: simulator says allowed=%v but the real op says allowed=%v",
				tc.branch, simAllowed, realAllowed)
		}
	}
}

// TestLiveRemote covers the two things a local bare remote cannot: credential
// injection over HTTPS and conversion of a real host's SSH URL. It is skipped
// unless a remote is supplied, so the default suite stays offline.
//
//	WICK_GIT_TEST_REMOTE=https://github.com/org/repo.git
//	WICK_GIT_TEST_USERNAME=x-access-token
//	WICK_GIT_TEST_TOKEN=ghp_...
//
// Read-only: it runs ls_remote and never pushes.
func TestLiveRemote(t *testing.T) {
	requireGit(t)
	env := envelopeOf(t)
	remote := os.Getenv("WICK_GIT_TEST_REMOTE")
	token := os.Getenv("WICK_GIT_TEST_TOKEN")
	if remote == "" || token == "" {
		t.Skip("WICK_GIT_TEST_REMOTE / WICK_GIT_TEST_TOKEN not set — skipping live remote test")
	}

	repo := initTestRepo(t)
	gitInTest(t, repo, "remote", "add", "origin", remote)

	cfg := baseCfg()
	cfg["username"] = os.Getenv("WICK_GIT_TEST_USERNAME")
	cfg["token"] = token
	cfg["network_timeout_seconds"] = "60"

	e := env(doLsRemote(opCtx(cfg, map[string]string{"repo_path": repo, "remote": "origin"})))
	if e["ok"] != true {
		t.Fatalf("ls_remote against the live remote failed: %v", e)
	}
	if out, _ := e["stdout"].(string); !strings.Contains(out, "refs/heads/") {
		t.Errorf("ls_remote returned no heads:\n%s", out)
	}

	// The token must never appear in the recorded command or output.
	blob, _ := json.Marshal(e)
	if strings.Contains(string(blob), token) {
		t.Error("the token leaked into the response envelope")
	}
}
