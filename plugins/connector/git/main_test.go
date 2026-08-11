package main

import (
	"os"
	"strings"
	"testing"
)

func TestAskpassReply(t *testing.T) {
	t.Setenv(envAskpassUser, "x-access-token")
	t.Setenv(envAskpassToken, "secret")

	cases := map[string]string{
		"Username for 'https://abc.com': ": "x-access-token",
		"Password for 'https://abc.com': ": "secret",
		"password:":                        "secret",
		"USERNAME:":                        "x-access-token",
		// git asks other things too; an empty reply is better than leaking the
		// token in answer to a prompt we do not understand.
		"Are you sure you want to continue connecting?": "",
	}
	for prompt, want := range cases {
		if got := askpassReply(prompt); got != want {
			t.Errorf("askpassReply(%q) = %q, want %q", prompt, got, want)
		}
	}
}

// TestAskpassReplyUnknownPromptsNeverLeakCredential is the credential boundary
// test. git reuses GIT_ASKPASS for questions that are not credential prompts —
// host-key confirmations, passphrase-less yes/no gates, upstream tool prompts.
// Answering any of those with the token would hand it to whatever asked. Every
// prompt below must produce the empty string, not the username and not the token.
func TestAskpassReplyUnknownPromptsNeverLeakCredential(t *testing.T) {
	t.Setenv(envAskpassUser, "x-access-token")
	t.Setenv(envAskpassToken, "s3cr3t-token")

	unknown := []string{
		"",
		"Are you sure you want to continue connecting (yes/no/[fingerprint])?",
		"The authenticity of host 'abc.com (10.0.0.1)' can't be established.",
		"Enter passphrase for key '/home/user/.ssh/id_ed25519': ",
		"yes/no",
		"Overwrite (y/n)?",
		"Continue?",
		"PIN for token:",  // no "token" substring boundary confusion check below
		"Enter PIN: ",     // smartcard PIN is not our HTTPS credential
		"Vault unlock: ",  // arbitrary third-party prompt
		"Login: ",         // "login" is not "username"
		"user:",           // substring of "username" must not be enough
		"Repository URL:", // git can ask for non-secret values too
	}
	for _, prompt := range unknown {
		got := askpassReply(prompt)
		if got == "" {
			continue
		}
		// Report which secret leaked, not just that something was returned.
		switch {
		case strings.Contains(got, "s3cr3t-token"):
			t.Errorf("askpassReply(%q) leaked the TOKEN (%q); unrecognised prompts must reply empty", prompt, got)
		case strings.Contains(got, "x-access-token"):
			t.Errorf("askpassReply(%q) leaked the USERNAME (%q); unrecognised prompts must reply empty", prompt, got)
		default:
			t.Errorf("askpassReply(%q) = %q, want empty", prompt, got)
		}
	}
}

func TestAskpassReplyWithoutEnvReturnsEmpty(t *testing.T) {
	os.Unsetenv(envAskpassUser)
	os.Unsetenv(envAskpassToken)
	if got := askpassReply("Password: "); got != "" {
		t.Errorf("askpassReply = %q, want empty when no credential is configured", got)
	}
	if got := askpassReply("Username: "); got != "" {
		t.Errorf("askpassReply = %q, want empty when no credential is configured", got)
	}
}

// TestAskpassReplyPromptIsCaseInsensitive pins the matching contract: git varies
// the capitalisation of its prompts between versions and transports, so the
// helper must not depend on it.
func TestAskpassReplyPromptIsCaseInsensitive(t *testing.T) {
	t.Setenv(envAskpassUser, "bot")
	t.Setenv(envAskpassToken, "tok")

	for _, prompt := range []string{"PASSWORD:", "Password:", "password for 'https://abc.com':"} {
		if got := askpassReply(prompt); got != "tok" {
			t.Errorf("askpassReply(%q) = %q, want the token", prompt, got)
		}
	}
	for _, prompt := range []string{"USERNAME:", "Username:", "username for 'https://abc.com':"} {
		if got := askpassReply(prompt); got != "bot" {
			t.Errorf("askpassReply(%q) = %q, want the username", prompt, got)
		}
	}
}

// TestAskpassArgIsRecognised guards the flag dispatch in main(): --askpass must
// be the trigger, and --dump-manifest must NOT be treated as one (pkg/plugin's
// Serve owns that flag, so main must pass it through untouched).
func TestAskpassArgIsRecognised(t *testing.T) {
	if !isAskpassInvocation([]string{"--askpass", "Password: "}) {
		t.Error("--askpass must be recognised as askpass mode")
	}
	if isAskpassInvocation([]string{"--dump-manifest"}) {
		t.Error("--dump-manifest must not be treated as askpass mode; Serve handles it")
	}
	if isAskpassInvocation(nil) {
		t.Error("no arguments means serve mode, not askpass mode")
	}
	if isAskpassInvocation([]string{"--sign-key", "k"}) {
		t.Error("unrelated flags must not trigger askpass mode")
	}
}

// TestAskpassInvocationMatchesRealGit is the regression test for the shape git
// actually uses. BuildEnv sets GIT_ASKPASS=<selfPath> with no flag, because git
// execs that path directly and cannot carry one. So when git asks for a
// credential this binary is called as `<self> "<prompt>"`. If only --askpass
// counted, that call would fall through to wickplugin.Serve, print "This binary
// is a plugin..." on stderr, and hand git no credential — every authenticated
// operation would fail with no useful diagnostic.
func TestAskpassInvocationMatchesRealGit(t *testing.T) {
	for _, prompt := range []string{
		"Password for 'https://abc.com': ",
		"Username for 'https://abc.com': ",
		"Are you sure you want to continue connecting?",
	} {
		if !isAskpassInvocation([]string{prompt}) {
			t.Errorf("a lone prompt %q must be treated as askpass mode; git passes no flag", prompt)
		}
		if got := askpassPrompt([]string{prompt}); got != prompt {
			t.Errorf("askpassPrompt([%q]) = %q, want the prompt itself", prompt, got)
		}
	}
}

// TestServeFlagsNeverTreatedAsPrompt is the other half of the bare-prompt rule:
// widening the match must not capture anything pkg/plugin's Serve owns, or
// `--dump-manifest` would print an empty askpass reply instead of the manifest
// and the plugin build (which generates plugin.json from it) would break.
func TestServeFlagsNeverTreatedAsPrompt(t *testing.T) {
	for _, args := range [][]string{
		{"--dump-manifest"},
		{"--sign-key", "key.ed25519"},
		{"--sign-key=key.ed25519"},
		{"--dump-manifest", "--sign-key", "k"},
		{"-h"},
		{"--some-future-flag"},
	} {
		if isAskpassInvocation(args) {
			t.Errorf("%v must reach Serve, not askpass mode", args)
		}
	}
}

// TestAskpassPromptFromArgs covers the missing-prompt case: git always passes a
// prompt, but a bare --askpass must not panic on a short argv.
func TestAskpassPromptFromArgs(t *testing.T) {
	if got := askpassPrompt([]string{"--askpass", "Password: "}); got != "Password: " {
		t.Errorf("askpassPrompt = %q, want the prompt", got)
	}
	if got := askpassPrompt([]string{"--askpass"}); got != "" {
		t.Errorf("askpassPrompt with no prompt = %q, want empty", got)
	}
}
