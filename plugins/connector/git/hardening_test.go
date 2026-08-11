package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoIdentityIsNormalised is the regression suite for a policy bypass.
//
// One character disabled every guard: "https://bitbucket.org./owner/repo" — a trailing
// dot, which is the fully qualified form of the hostname, accepted by DNS and by TLS and
// routed to the same server. The glob "bitbucket.org/owner/repo" did not match it, so
// the repository resolved to the global fallback, and on an instance whose protection
// lived in per-repo rules that meant no branch pattern, no commit pattern and nothing
// protected. Any caller allowed to clone could pick its own policy.
//
// Case, ports, double slashes and ".git" had each been handled with a targeted fix, and
// each fix was correct — the point of this table is that a list of remembered spellings
// is the wrong shape for the problem. Both sides now go through one parser, so the
// question is whether the parser agrees rather than whether a variant was anticipated.
func TestRepoIdentityIsNormalised(t *testing.T) {
	const rule = "bitbucket.org/yogasetiawan/test"

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"the plain form", "https://bitbucket.org/yogasetiawan/test.git"},
		// The reported bypass.
		{"a trailing dot on the host", "https://bitbucket.org./yogasetiawan/test.git"},
		{"several trailing dots", "https://bitbucket.org.../yogasetiawan/test.git"},
		{"a trailing dot and a port", "https://bitbucket.org.:443/yogasetiawan/test.git"},
		{"mixed case", "https://BitBucket.ORG/YogaSetiawan/Test.git"},
		{"mixed case and a dot", "https://BITBUCKET.ORG./YOGASETIAWAN/TEST.GIT"},
		{"an explicit port", "https://bitbucket.org:443/yogasetiawan/test.git"},
		{"doubled slashes", "https://bitbucket.org//yogasetiawan//test"},
		{"a trailing slash", "https://bitbucket.org/yogasetiawan/test/"},
		{"no .git suffix", "https://bitbucket.org/yogasetiawan/test"},
		{"an embedded credential", "https://user:pw@bitbucket.org/yogasetiawan/test.git"},
		{"scp-style ssh", "git@bitbucket.org:yogasetiawan/test.git"},
		{"scp-style with a dot", "git@bitbucket.org.:yogasetiawan/test.git"},
		{"ssh:// with a port", "ssh://git@bitbucket.org:22/yogasetiawan/test.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := RepoSlug(tc.raw)
			if slug != rule {
				t.Errorf("RepoSlug(%q) = %q, want %q", tc.raw, slug, rule)
			}
			if !MatchRepo(rule, "", slug) {
				t.Errorf("%q escaped the rule %q — every spelling of one repository must "+
					"resolve to one identity, or a caller can pick its own policy", tc.raw, rule)
			}
		})
	}
}

// TestNormalisationDoesNotOverMatch is the other half, and the more important one: a
// normaliser that collapses too much would silently apply one repository's rules to
// another, which is a worse failure than the one being fixed.
func TestNormalisationDoesNotOverMatch(t *testing.T) {
	const rule = "bitbucket.org/yogasetiawan/test"
	for _, raw := range []string{
		// A different host that merely ends in the rule's host.
		"https://evil-bitbucket.org/yogasetiawan/test.git",
		// A subdomain is a different host.
		"https://x.bitbucket.org/yogasetiawan/test.git",
		// The rule's host as a path segment somewhere else.
		"https://evil.com/bitbucket.org/yogasetiawan/test.git",
		// Different owner, different repository.
		"https://bitbucket.org/someone-else/test.git",
		"https://bitbucket.org/yogasetiawan/other.git",
		// A prefix of the repository name is not the repository.
		"https://bitbucket.org/yogasetiawan/test2.git",
		// Userinfo must not be read as the host.
		"https://bitbucket.org@evil.com/yogasetiawan/test.git",
	} {
		if MatchRepo(rule, "", RepoSlug(raw)) {
			t.Errorf("%q must NOT match the rule %q (slug %q)", raw, rule, RepoSlug(raw))
		}
	}
}

// TestRulesAreNormalisedToo covers the side an operator controls. A rule pasted from a
// browser or a clone URL carries the same variations as a remote does, and having to
// know which spelling the engine wants would be its own trap.
func TestRulesAreNormalisedToo(t *testing.T) {
	const slug = "bitbucket.org/yogasetiawan/test"
	for _, glob := range []string{
		"bitbucket.org/yogasetiawan/test",
		"BitBucket.ORG/YogaSetiawan/Test",
		"bitbucket.org./yogasetiawan/test",
		"bitbucket.org.:443/yogasetiawan/test.git",
		"bitbucket.org/yogasetiawan/test.git",
		"/bitbucket.org/yogasetiawan/test/",
		"*/yogasetiawan/test",
		"bitbucket.org/yogasetiawan/*",
		"bitbucket.org/*/test",
		"bitbucket.org/yogasetiawan", // owner-level: covers everything under it
		"*/*/*",
	} {
		if !MatchRepo(glob, "", slug) {
			t.Errorf("rule %q should match %q", glob, slug)
		}
	}
	for _, glob := range []string{
		"bitbucket.org/other/test",
		"gitlab.com/yogasetiawan/test",
		"bitbucket.org/yogasetiawan/tes", // not a prefix match
		"bitbucket.org",                  // a host alone is not a repository rule
	} {
		if MatchRepo(glob, "", slug) {
			t.Errorf("rule %q must not match %q", glob, slug)
		}
	}
}

// TestSubgroupPathsSurvive covers GitLab, where a repository can sit several levels
// deep. Matching field by field must not have flattened that.
func TestSubgroupPathsSurvive(t *testing.T) {
	slug := RepoSlug("https://gitlab.com/org/team/sub/repo.git")
	if slug != "gitlab.com/org/team/sub/repo" {
		t.Fatalf("slug = %q, want every segment kept", slug)
	}
	for _, glob := range []string{
		"gitlab.com/org/team/sub/repo",
		"gitlab.com/org/*",
		"*/org/*",
		"gitlab.com/org/team/*",
	} {
		if !MatchRepo(glob, "", slug) {
			t.Errorf("rule %q should match a subgroup path %q", glob, slug)
		}
	}
	if MatchRepo("gitlab.com/org/repo", "", slug) {
		t.Error("a two-level rule must not match a deeper path by accident")
	}
}

// TestLocalRemotesHaveNoSlug covers the junk identities the string-based parser
// produced. A drive colon was eaten by the port stripper, giving `C/\Users\x\repo`, and
// "file://" was read as a hostname, giving "file/srv/mirror". Neither is a
// host/owner/repo, and both could be matched by a careless glob.
func TestLocalRemotesHaveNoSlug(t *testing.T) {
	for _, raw := range []string{
		`C:\Users\x\gittest4`,
		"C:/Users/x/gittest4",
		"file:///srv/git/mirror.git",
		"/srv/git/mirror.git",
		"../sibling.git",
	} {
		id := ParseRepoID(raw)
		if !id.Local {
			t.Errorf("ParseRepoID(%q).Local = false, want a filesystem remote", raw)
		}
		if got := id.Slug(); got != "" {
			t.Errorf("ParseRepoID(%q).Slug() = %q, want empty — a local path has no host", raw, got)
		}
		// The specific junk that used to appear.
		if strings.HasPrefix(id.Slug(), "C/") || strings.HasPrefix(id.Slug(), "file/") {
			t.Errorf("%q produced a fabricated host: %q", raw, id.Slug())
		}
	}
}

// TestRefNamesAreValidatedAsValues covers a guard that depended on argv position.
//
// ValidateUserArgs is a deny-list over argv TOKENS, so a value embedded in a larger
// token never reached it: push builds "HEAD:refs/heads/<branch>", and a branch named
// "--receive-pack=x" arrived as one token starting with "HEAD:". The same string as
// show's {ref} was refused. Not exploitable as it stood — the value sits after
// --end-of-options and git reads it as a ref name — but the protection came from where
// the value happened to land rather than from the value, so a refactor that moved the
// position would have opened it.
func TestRefNamesAreValidatedAsValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		ok    bool
	}{
		// The reported case.
		{"a flag-shaped branch", "--receive-pack=touch-pwned", false},
		{"a short flag", "-d", false},
		{"a long flag", "--all", false},
		// git-check-ref-format rules that also keep a ref from being something else.
		{"a double dot", "fix/..%2fescape", false},
		{"a reflog selector", "fix/x@{1}", false},
		{"a space", "fix/my branch", false},
		{"a colon", "fix:x", false},
		{"a tilde", "fix~1", false},
		{"a caret", "fix^", false},
		{"a question mark", "fix/x?", false},
		{"an asterisk", "fix/*", false},
		{"a backslash", `fix\x`, false},
		{"a control character", "fix/\x01x", false},
		{"a trailing dot", "fix/x.", false},
		{"a .lock suffix", "fix/x.lock", false},
		{"a leading slash", "/fix/x", false},
		{"a trailing slash", "fix/x/", false},
		{"doubled slashes", "fix//x", false},
		{"bare at", "@", false},
		{"empty", "", false},
		{"whitespace only", "   ", false},

		// Names that must keep working: refusing these would break ordinary use.
		{"a conventional branch", "fix/login-timeout", true},
		{"a nested branch", "ai/fix/some-thing", true},
		{"a version tag", "v1.2.3", true},
		{"an underscore", "feat/new_thing", true},
		{"a dot inside", "release/1.2.x", true},
		{"a dash inside", "fix/a-b-c", true},
		{"a digit start", "2024-cleanup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRefName("branch", tc.value)
			if tc.ok && err != nil {
				t.Errorf("ValidateRefName(%q) = %v, want accepted", tc.value, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("ValidateRefName(%q) was accepted, want refused", tc.value)
			}
		})
	}
}

// TestPushBranchIsValidated is the end-to-end half: the validator has to be wired into
// the operation that was open, not only exist.
func TestPushBranchIsValidated(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin", "https://abc.com/org/repo.git")

	_, err := doPush(opCtx(nil, map[string]string{
		"repo_path": dir, "branch": "--receive-pack=touch-pwned", "dry_run": "true",
	}))
	if err == nil {
		t.Fatal("push accepted a flag-shaped branch name")
	}
	if !strings.Contains(err.Error(), "--receive-pack") {
		t.Errorf("the error must name the rejected value, got: %v", err)
	}
}

// TestNumericInputsRefuseNonsense covers input silently replaced by a default.
//
// "limit: -5" became 20, so the caller saw twenty commits with no sign its argument had
// been discarded — and would keep passing -5. Absent and nonsensical are different
// things and only one of them means "use the default".
func TestNumericInputsRefuseNonsense(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	for _, tc := range []struct {
		name  string
		input map[string]string
		ok    bool
	}{
		{"absent means default", map[string]string{"repo_path": dir}, true},
		{"explicit zero means default", map[string]string{"repo_path": dir, "limit": "0"}, true},
		{"a normal value", map[string]string{"repo_path": dir, "limit": "5"}, true},
		{"negative is refused", map[string]string{"repo_path": dir, "limit": "-5"}, false},
		{"not a number is refused", map[string]string{"repo_path": dir, "limit": "abc"}, false},
		{"beyond the cap is refused", map[string]string{"repo_path": dir, "limit": "999999"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := doLog(opCtx(nil, tc.input))
			if tc.ok && err != nil {
				t.Errorf("unexpected refusal: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("input %v was accepted; a discarded argument must be reported", tc.input)
			}
		})
	}
}

// TestTransportsAreAllowListed covers a refusal that was accidental rather than
// designed.
//
// "ext::false" WAS rejected — but only because the scp-style parser could not make sense
// of it and reported `remote host "ext" looks like an ~/.ssh/config alias`. The class is
// remote code execution (git's ext transport runs its argument as a command), so being
// stopped by a parser's confusion is not good enough: a parser change could let it
// through, and the message sent the operator to fix a host map.
func TestTransportsAreAllowListed(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string // substring the error must contain
	}{
		{"ext::false", "not allowed"},
		{"ext::sh -c 'touch pwned'", "not allowed"},
		{"ext::ssh://host/repo.git", "not allowed"},
		{"fd::7", "not allowed"},
		{"transport-helper::whatever", "not allowed"},
		// git:// carries no credential and is unencrypted. Never supported by
		// ConvertRemote, and now refused by name rather than by the parser mistaking
		// "git" for a hostname.
		{"git://abc.com/org/repo.git", "not allowed"},
	} {
		_, err := ConvertRemote(tc.raw, nil, true)
		if err == nil {
			t.Errorf("ConvertRemote(%q) was accepted; a transport helper can execute commands", tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ConvertRemote(%q) error = %v, want it to say the transport is not allowed",
				tc.raw, err)
		}
		// The old message blamed an ssh config alias, which sent the reader to the wrong fix.
		if strings.Contains(err.Error(), "ssh/config alias") {
			t.Errorf("ConvertRemote(%q) still explains itself as a host-alias problem: %v", tc.raw, err)
		}
	}

	// The transports that must keep working.
	for _, raw := range []string{
		"https://abc.com/org/repo.git",
		"http://abc.com/org/repo.git",
		"git@abc.com:org/repo.git",
		"ssh://git@abc.com/org/repo.git",
		"file:///srv/git/mirror.git",
		"/srv/git/mirror.git",
	} {
		if _, err := ConvertRemote(raw, nil, true); err != nil {
			t.Errorf("ConvertRemote(%q) = %v, want accepted", raw, err)
		}
	}
}

// TestPathRootsAreOptIn covers the scope check. Empty means unrestricted on purpose:
// this connector manages repositories that already exist, wherever they are, and a
// mandatory sandbox would put every existing checkout out of reach.
func TestPathRootsAreOptIn(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "work", "api")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	// No roots: everything passes, which is the shipped behaviour.
	if err := CheckPathRoots(outside, nil); err != nil {
		t.Errorf("with no roots configured every path must be allowed, got %v", err)
	}

	roots := []string{root}
	if err := CheckPathRoots(inside, roots); err != nil {
		t.Errorf("a path inside a root must be allowed, got %v", err)
	}
	if err := CheckPathRoots(root, roots); err != nil {
		t.Errorf("the root itself must be allowed, got %v", err)
	}
	if err := CheckPathRoots(outside, roots); err == nil {
		t.Error("a path outside every root must be refused")
	}
	// The error has to name the roots, or the caller cannot tell what to pass instead.
	if err := CheckPathRoots(outside, roots); err != nil && !strings.Contains(err.Error(), root) {
		t.Errorf("the error must name the allowed roots, got %v", err)
	}
}

// TestPathRootsResolveBeforeComparing is why the check is not a string prefix test. A
// path can leave a root by traversal or by symlink, and only asking the filesystem where
// it really lands catches both.
func TestPathRootsResolveBeforeComparing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "allowed")
	secret := filepath.Join(base, "secret")
	for _, d := range []string{root, secret} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Traversal: a path that starts with the root but climbs out of it.
	escape := filepath.Join(root, "..", "secret")
	if err := CheckPathRoots(escape, []string{root}); err == nil {
		t.Errorf("%q resolves outside the root and must be refused", escape)
	}

	// Symlink: a link inside the root pointing out of it. Skipped where the OS will not
	// create one without privileges, which is the default on Windows.
	link := filepath.Join(root, "out")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if err := CheckPathRoots(link, []string{root}); err == nil {
		t.Error("a symlink out of the root must be refused; a prefix test would have passed it")
	}
	// And a path THROUGH the link, which is the form an attacker would actually use.
	if err := CheckPathRoots(filepath.Join(link, "repo"), []string{root}); err == nil {
		t.Error("a path through a symlink out of the root must be refused")
	}
}

// TestPathRootsAllowANonExistentDestination covers the clone case. A destination does
// not exist yet, so a check that demanded an existing path would refuse every clone —
// while still needing to catch a symlinked parent.
func TestPathRootsAllowANonExistentDestination(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "not-yet", "cloned-here")
	if err := CheckPathRoots(dest, []string{root}); err != nil {
		t.Errorf("a destination that does not exist yet must be allowed inside a root, got %v", err)
	}
	if err := CheckPathRoots(filepath.Join(t.TempDir(), "elsewhere"), []string{root}); err == nil {
		t.Error("a non-existent destination outside every root must still be refused")
	}
}
