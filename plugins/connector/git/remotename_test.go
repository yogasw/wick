package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// This file covers one defect: the network operations passed the remote's URL where
// git expects a remote NAME.
//
// It looked harmless — the push reached the right server — but three separate pieces
// of git bookkeeping only happen when git knows which remote it is talking to, and
// all three were silently lost:
//
//   - fetch wrote FETCH_HEAD and never updated refs/remotes/<remote>/*, so a branch
//     that existed upstream never showed up in branch_list remote=true.
//   - push --set-upstream recorded the URL STRING as branch.<b>.remote, so status
//     lost branch.upstream and branch.ab — ahead/behind died for every branch the
//     connector created.
//   - pull with no {branch} had no upstream to resolve, contradicting its own docs.
//
// The tests below are integration tests against a real git binary and a real (local,
// bare) remote, because none of this bookkeeping is observable without an actual
// fetch or push.

// bareUpstream creates a bare repository to act as a real remote, plus a clone whose
// main branch is already pushed to it.
func bareUpstream(t *testing.T) (bare, clone string) {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	bare = filepath.Join(root, "up.git")
	clone = filepath.Join(root, "work")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := safeexec.Command(gitPath, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	run(root, "init", "-q", "--bare", "-b", "main", "up.git")
	run(root, "clone", "-q", bare, "work")
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(clone, "add", "README.md")
	run(clone, "commit", "-q", "-m", "initial")
	run(clone, "push", "-q", "origin", "main")
	return bare, clone
}

// cloneOf makes a second working clone of bare, so a change can be made "elsewhere"
// and only be learnable by fetching.
func cloneOf(t *testing.T, bare, name string) string {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	cmd := safeexec.Command(gitPath, "clone", "-q", bare, name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	dir := filepath.Join(root, name)
	for _, kv := range [][2]string{{"user.email", "test@example.com"}, {"user.name", "Test"}} {
		c := safeexec.Command(gitPath, "config", kv[0], kv[1])
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("config: %v\n%s", err, out)
		}
	}
	return dir
}

// gitOut runs git and returns trimmed stdout. A missing ref exits non-zero and the
// empty string is the answer the caller wants, so the error is deliberately ignored.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	cmd := safeexec.Command(gitPath, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// TestFetchUpdatesRemoteTrackingRefs is symptom one. With a URL as the remote
// argument, fetch wrote FETCH_HEAD and nothing else.
func TestFetchUpdatesRemoteTrackingRefs(t *testing.T) {
	requireGit(t)
	bare, clone := bareUpstream(t)

	// The branch is created in a different clone, so this one can only learn about it
	// by fetching — which is the whole point.
	other := cloneOf(t, bare, "other")
	runInRepo(t, other, "checkout", "-q", "-b", "feature/from-elsewhere")
	runInRepo(t, other, "commit", "-q", "--allow-empty", "-m", "remote work")
	runInRepo(t, other, "push", "-q", "origin", "feature/from-elsewhere")

	const ref = "refs/remotes/origin/feature/from-elsewhere"
	if got := gitOut(t, clone, "rev-parse", "--verify", "-q", ref); got != "" {
		t.Fatalf("precondition failed: the clone already knows the branch (%s)", got)
	}

	m := envOf(t)(doFetch(opCtx(nil, map[string]string{"repo_path": clone})))
	if m["ok"] != true {
		t.Fatalf("fetch failed: %+v", m)
	}

	if got := gitOut(t, clone, "rev-parse", "--verify", "-q", ref); got == "" {
		t.Error("fetch must update refs/remotes/origin/* — passing a URL only wrote FETCH_HEAD, " +
			"so a pushed branch never appeared in branch_list remote=true")
	}
	if cmd := m["command"].(string); !strings.Contains(cmd, "origin") {
		t.Errorf("fetch must name the remote, got %q", cmd)
	}
}

// TestPushSetUpstreamRecordsTheRemoteName is symptom two, and the one with the longest
// tail: every branch the connector created lost ahead/behind reporting.
func TestPushSetUpstreamRecordsTheRemoteName(t *testing.T) {
	requireGit(t)
	_, clone := bareUpstream(t)
	runInRepo(t, clone, "checkout", "-q", "-b", "feature/upstream-check")
	runInRepo(t, clone, "commit", "-q", "--allow-empty", "-m", "local work")

	m := envOf(t)(doPush(opCtx(nil, map[string]string{
		"repo_path": clone, "branch": "feature/upstream-check", "set_upstream": "true",
	})))
	if m["ok"] != true {
		t.Fatalf("push failed: %+v", m)
	}

	if got := gitOut(t, clone, "config", "--get", "branch.feature/upstream-check.remote"); got != "origin" {
		t.Errorf("branch.<b>.remote = %q, want origin — a URL here is what killed ahead/behind", got)
	}
	if got := gitOut(t, clone, "config", "--get", "branch.feature/upstream-check.merge"); got != "refs/heads/feature/upstream-check" {
		t.Errorf("branch.<b>.merge = %q, want the upstream ref", got)
	}
	// The symptom as a caller actually sees it.
	status := gitOut(t, clone, "status", "--porcelain=v2", "--branch")
	if !strings.Contains(status, "# branch.upstream origin/feature/upstream-check") {
		t.Errorf("status must report branch.upstream:\n%s", status)
	}
	if !strings.Contains(status, "# branch.ab ") {
		t.Errorf("status must report ahead/behind:\n%s", status)
	}
}

// TestPullResolvesTheUpstreamWithNoBranch is symptom three. An omitted {branch} is
// documented to fall back to the current branch's upstream, which cannot resolve
// against a URL.
func TestPullResolvesTheUpstreamWithNoBranch(t *testing.T) {
	requireGit(t)
	bare, clone := bareUpstream(t)

	other := cloneOf(t, bare, "other")
	runInRepo(t, other, "commit", "-q", "--allow-empty", "-m", "upstream commit")
	runInRepo(t, other, "push", "-q", "origin", "main")

	before := gitOut(t, clone, "rev-parse", "HEAD")
	m := envOf(t)(doPull(opCtx(nil, map[string]string{"repo_path": clone})))
	if m["ok"] != true {
		t.Fatalf("pull with no branch failed: %+v\nstderr: %v", m, m["stderr"])
	}
	if after := gitOut(t, clone, "rev-parse", "HEAD"); after == before {
		t.Error("pull with no branch must fast-forward from the upstream")
	}
}

// TestRewriteNeutralisesACredentialInTheRemoteURL guards the property that made the
// URL substitution defensible in the first place.
//
// Verified against git 2.52: with https://user:pass@host/repo configured, git dials
// with THAT credential and never consults GIT_ASKPASS. Without a rewrite, naming the
// remote would authenticate as whoever's password is in .git/config, so the credential
// in the audit trail would not be the one used — the exact failure the connector's
// unconditional credential.helper reset exists to prevent.
func TestRewriteNeutralisesACredentialInTheRemoteURL(t *testing.T) {
	info, err := ConvertRemote("https://baduser:badpass@abc.com/org/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if len(info.RewriteArgs) == 0 {
		t.Fatal("a remote URL carrying a credential must produce an insteadOf rewrite")
	}
	joined := strings.Join(info.RewriteArgs, " ")
	if !strings.Contains(joined, "insteadOf") {
		t.Errorf("rewrite must use insteadOf, got %q", joined)
	}
	// It has to point AT the clean URL and FROM the dirty one, not the reverse.
	if !strings.Contains(joined, "url.https://abc.com/org/repo.git.insteadOf=") {
		t.Errorf("rewrite must target the credential-free URL, got %q", joined)
	}
	if !strings.HasSuffix(joined, "https://baduser:badpass@abc.com/org/repo.git") {
		t.Errorf("rewrite must match the configured URL, got %q", joined)
	}
}

// TestPushDoesNotLeakAnEmbeddedCredential is the end-to-end half of the test above:
// the reported command is stored in run history, so it must not carry the password
// that was sitting in .git/config.
func TestPushDoesNotLeakAnEmbeddedCredential(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin",
		"https://baduser:badpass@invalid.invalid/org/repo.git")

	m := envOf(t)(doPush(opCtx(nil, map[string]string{
		"repo_path": dir, "dry_run": "true",
	})))
	if m["ok"] != true {
		t.Fatalf("push dry run failed: %+v", m)
	}
	cmd := m["command"].(string)
	if strings.Contains(cmd, "badpass") {
		t.Errorf("the reported command leaks the embedded credential: %q", cmd)
	}
	if !strings.Contains(cmd, "--end-of-options origin ") {
		t.Errorf("push must name the remote, got %q", cmd)
	}
}

// TestSSHRemoteConvertsThroughConfigNotTheArgument covers the case the URL
// substitution originally existed for: an SSH remote has to reach an HTTPS host. It
// still converts, now as injected config, so the command line keeps naming the remote.
func TestSSHRemoteConvertsThroughConfigNotTheArgument(t *testing.T) {
	info, err := ConvertRemote("git@github.com:org/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if !info.Converted || info.Effective != "https://github.com/org/repo.git" {
		t.Fatalf("conversion wrong: %+v", info)
	}
	// Keyed on the host PREFIX, so one rewrite covers every repository on that host —
	// the rewrite is injected per command and the remote URL is read fresh each time.
	joined := strings.Join(info.RewriteArgs, " ")
	if !strings.Contains(joined, "url.https://github.com/.insteadOf=git@github.com:") {
		t.Errorf("rewrite = %q, want a host-prefix insteadOf pair", joined)
	}

	// The common case must inject nothing: a clean HTTPS remote needs no rewrite, and
	// an unnecessary -c would be one more thing in every command line.
	plain, err := ConvertRemote("https://github.com/org/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if len(plain.RewriteArgs) != 0 {
		t.Errorf("a clean HTTPS remote must need no rewrite, got %q", plain.RewriteArgs)
	}

	// A local path remote likewise: no host, no credential, nothing to rewrite.
	local, err := ConvertRemote("/srv/mirrors/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if len(local.RewriteArgs) != 0 {
		t.Errorf("a local path remote must need no rewrite, got %q", local.RewriteArgs)
	}
}

// TestRewriteReachesTheCommandLine closes the loop: a rewrite that is computed but
// never injected would leave every symptom in place while the unit tests passed.
func TestRewriteReachesTheCommandLine(t *testing.T) {
	info, err := ConvertRemote("git@github.com:org/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	args := injectedArgs(opCtx(nil, nil), AuthSpec{}, "push", info.RewriteArgs)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "insteadOf") {
		t.Errorf("injectedArgs must carry the rewrite, got %q", joined)
	}
	// And it must not appear when there is nothing to rewrite.
	plain := injectedArgs(opCtx(nil, nil), AuthSpec{}, "push", nil)
	if strings.Contains(strings.Join(plain, " "), "insteadOf") {
		t.Errorf("no rewrite means no insteadOf, got %q", plain)
	}
}
