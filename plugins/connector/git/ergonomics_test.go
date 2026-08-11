package main

import (
	"strings"
	"testing"
)

// This file covers four findings that share a shape: the connector was CORRECT but
// described itself wrongly, or gated at the wrong granularity. None of them let
// anything unsafe through; each one made a caller draw a false conclusion.

// TestStashListIsNotGatedOnAProtectedBranch covers a read refused for writing nothing.
//
// One operation covers three git commands with opposite effects — push and pop move
// work around, list only reads — and the gate was per OPERATION. On a protected branch
// "stash list" came back "branch \"main\" is protected; direct stash is blocked", which
// is both wrong and unactionable: there is no way to list stashes without writing,
// because listing never wrote in the first place.
func TestStashListIsNotGatedOnAProtectedBranch(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	cfg := baseCfg() // protects main and master; initTestRepo checks out main

	m := envelopeOf(t)(doStash(opCtx(cfg, map[string]string{
		"repo_path": dir, "action": "list",
	})))
	pol := policyOf(t, m)
	if pol["verdict"] != "allow" {
		t.Errorf("stash list is a read and must not be gated on a protected branch, got %v", pol)
	}
	if m["ok"] != true {
		t.Errorf("stash list failed: %+v", m)
	}

	// The writing actions must still be refused, or this fix would have opened a hole
	// while closing an annoyance.
	for _, action := range []string{"push", "pop"} {
		w := envelopeOf(t)(doStash(opCtx(cfg, map[string]string{
			"repo_path": dir, "action": action,
		})))
		if policyOf(t, w)["verdict"] != "deny" {
			t.Errorf("stash %s writes and must still be refused on a protected branch, got %v",
				action, policyOf(t, w))
		}
	}
}

// TestForceDenyNamesTheOperationBeingRefused covers a message that described a
// different operation than the one that was refused.
//
// allow_force_push gates two things — a force push and a hard reset — and the message
// was written from the config's name. Answering "reset mode=hard" with "force push is
// not allowed" mentions an operation that is not happening, and the natural reading is
// that the connector misunderstood the request rather than that a policy applied.
func TestForceDenyNamesTheOperationBeingRefused(t *testing.T) {
	pol := EffectivePolicy{AllowForcePush: false, MatchedRule: "global"}

	reset := pol.Evaluate(Request{Op: "reset", Force: true, Branch: "feature/x"})
	if reset.Allow {
		t.Fatal("a hard reset must be refused when force push is off")
	}
	if !strings.Contains(reset.Reason, "hard reset") {
		t.Errorf("a refused reset must say so, got %q", reset.Reason)
	}
	if strings.Contains(reset.Reason, "force push is not allowed") {
		t.Errorf("a refused reset must not be described as a push, got %q", reset.Reason)
	}

	push := pol.Evaluate(Request{Op: "push", Force: true, Branch: "feature/x"})
	if push.Allow {
		t.Fatal("a force push must be refused when force push is off")
	}
	if !strings.Contains(push.Reason, "force push") {
		t.Errorf("a refused push must say force push, got %q", push.Reason)
	}

	// Both cite the config key, because that is what an operator has to change, and both
	// say it gates the pair — otherwise fixing a refused reset means finding a setting
	// whose name mentions only pushes.
	for _, v := range []Verdict{reset, push} {
		if !strings.Contains(v.Reason, "allow_force_push") {
			t.Errorf("the reason must name the config to change, got %q", v.Reason)
		}
		if !strings.Contains(v.Reason, "hard reset") {
			t.Errorf("the reason must say the setting gates both, got %q", v.Reason)
		}
	}
}

// TestBranchListOmitsTheSymbolicHead covers noise that read as data.
//
// refs/remotes/origin/HEAD is the remote's default-branch pointer, not a branch. Listed
// plainly it appeared as a bare "origin" among origin/main and origin/dev — which reads
// as a branch literally called "origin", and duplicates whichever branch it points at.
func TestBranchListOmitsTheSymbolicHead(t *testing.T) {
	requireGit(t)
	bare, clone := bareUpstream(t)

	// Give the remote a second branch and a resolved HEAD, which is what a real clone
	// has and what produced the stray row.
	other := cloneOf(t, bare, "other")
	runInRepo(t, other, "checkout", "-q", "-b", "dev")
	runInRepo(t, other, "commit", "-q", "--allow-empty", "-m", "dev work")
	runInRepo(t, other, "push", "-q", "origin", "dev")
	runInRepo(t, clone, "fetch", "-q", "origin")
	runInRepo(t, clone, "remote", "set-head", "origin", "main")

	m := envelopeOf(t)(doBranchList(opCtx(baseCfg(), map[string]string{
		"repo_path": clone, "remote": "true",
	})))
	if m["ok"] != true {
		t.Fatalf("branch_list failed: %+v", m)
	}

	var names []string
	for _, line := range strings.Split(m["stdout"].(string), "\n") {
		if line == "" {
			t.Error("no blank rows: filtering inside a git --format still emits the newline")
			continue
		}
		names = append(names, strings.Split(line, "\t")[0])
	}
	for _, n := range names {
		if n == "origin" {
			t.Errorf("origin/HEAD must not be listed as a branch called \"origin\": %v", names)
		}
	}
	// The real branches still have to be there — filtering must not have eaten them.
	for _, want := range []string{"origin/main", "origin/dev"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from %v", want, names)
		}
	}
}

// TestReportedCommandIsPasteable covers a string that could not be used for the one
// thing it exists for.
//
// Execution never goes through a shell, so an argument with a space is already safe to
// RUN. But the same string is reported and stored in run history, where it is read as a
// shell command: "-c user.name=yoga bot" looks like two arguments, so pasting it to
// reproduce a failure runs something different from what actually ran.
func TestReportedCommandIsPasteable(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{
			// The case from the report: a two-word author name.
			name: "a value containing a space",
			argv: []string{"-c", "user.name=yoga bot", "commit"},
			want: `git -c 'user.name=yoga bot' commit`,
		},
		{
			// Also from the report: a commit message with a colon and a space.
			name: "a commit message",
			argv: []string{"commit", "-m", "ai-test: side A"},
			want: `git commit -m 'ai-test: side A'`,
		},
		{
			// Nothing needing quotes stays unquoted: this string exists to be read, and a
			// fully quoted line is equally correct and much harder to scan.
			name: "a plain command",
			argv: []string{"status", "--porcelain=v2"},
			want: `git status --porcelain=v2`,
		},
		{
			// A single quote cannot be escaped inside single quotes, so the POSIX dance is
			// required. Getting this wrong produces a line that does not even parse.
			name: "an embedded single quote",
			argv: []string{"commit", "-m", "it's fine"},
			// Verified by round-tripping through a real shell: this parses back to the
			// single argument "it's fine".
			want: `git commit -m 'it'\''s fine'`,
		},
		{
			// Shell metacharacters must be quoted even though argv made them inert: pasted
			// unquoted, this would run rm.
			name: "shell metacharacters",
			argv: []string{"branch", "BAD;rm -rf /"},
			want: `git branch 'BAD;rm -rf /'`,
		},
		{
			name: "an empty argument",
			argv: []string{"-c", "credential.helper="},
			want: `git -c credential.helper=`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayCommand(tc.argv); got != tc.want {
				t.Errorf("displayCommand() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestReportedCommandQuotingReachesTheResponse closes the loop: quoting that never
// reaches Result.Command would leave the reported string exactly as unusable.
func TestReportedCommandQuotingReachesTheResponse(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	cfg := baseCfg()
	cfg["author_name"] = "yoga bot" // the two-word name from the report

	m := envelopeOf(t)(doCommit(opCtx(cfg, map[string]string{
		"repo_path": dir, "message": "fix/spaces: a message with spaces", "dry_run": "true",
	})))
	cmd, _ := m["command"].(string)
	if cmd == "" {
		t.Fatalf("no command reported: %+v", m)
	}
	if strings.Contains(cmd, "user.name=yoga bot") && !strings.Contains(cmd, `'user.name=yoga bot'`) {
		t.Errorf("a value with a space must be quoted in the reported command: %q", cmd)
	}
}
