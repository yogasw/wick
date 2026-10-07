package envscrub

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// reset clears the once-cached rules so a test can choose its own.
// loadRules deliberately caches, so every test that changes the deny
// list has to go through here.
func reset(t *testing.T, deny string) {
	t.Helper()
	rulesOnce = sync.Once{}
	rules = nil
	seenMu.Lock()
	seen = map[string]bool{}
	seenMu.Unlock()
	if deny == "" {
		t.Setenv(DenyEnvVar, "")
	} else {
		t.Setenv(DenyEnvVar, deny)
	}
}

func TestScrubDropsDatabaseURLByDefault(t *testing.T) {
	reset(t, "")
	in := []string{
		"PATH=/usr/bin",
		"DATABASE_URL=postgresql://u:p@host/db",
		"HOME=/home/ubuntu",
	}
	got := Scrub(in)
	if slices.ContainsFunc(got, func(s string) bool {
		return strings.HasPrefix(s, "DATABASE_URL=")
	}) {
		t.Fatalf("DATABASE_URL survived the scrub: %v", got)
	}
	// Everything else must be preserved verbatim — the point is to remove
	// one entry, not to normalise the environment.
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/ubuntu"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q from %v", want, got)
		}
	}
}

func TestScrubKeepsProviderCredentials(t *testing.T) {
	reset(t, "")
	// These are how the AI CLIs authenticate. Dropping them would break
	// every spawn, so the default list must never reach them.
	in := []string{
		"ANTHROPIC_API_KEY=sk-ant-xxx",
		"OPENAI_API_KEY=sk-xxx",
		"CLAUDE_CODE_MESSAGING_TOKEN=abc",
	}
	got := Scrub(in)
	if len(got) != len(in) {
		t.Fatalf("provider credentials were dropped: in=%v out=%v", in, got)
	}
}

func TestScrubGlobs(t *testing.T) {
	reset(t, "")
	in := []string{
		"REPORTING_DSN=postgres://x",  // *_DSN
		"REDIS_PASSWORD=hunter2",      // *_PASSWORD
		"MYSQL_PASSWD=hunter2",        // *_PASSWD
		"DSN_NOTES=keep-me",           // not a suffix match
		"PASSWORD_POLICY_URL=keep-me", // not a suffix match
	}
	got := Scrub(in)
	for _, gone := range []string{"REPORTING_DSN=", "REDIS_PASSWORD=", "MYSQL_PASSWD="} {
		if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, gone) }) {
			t.Errorf("%s should have been dropped: %v", gone, got)
		}
	}
	for _, kept := range []string{"DSN_NOTES=keep-me", "PASSWORD_POLICY_URL=keep-me"} {
		if !slices.Contains(got, kept) {
			t.Errorf("%q was dropped but only matches mid-name: %v", kept, got)
		}
	}
}

func TestScrubCustomDenyList(t *testing.T) {
	reset(t, "APP_URL,INTERNAL_*")
	in := []string{
		"APP_URL=https://x",
		"INTERNAL_QUEUE=y",
		"DATABASE_URL=postgres://z", // custom list REPLACES the default
		"PATH=/usr/bin",
	}
	got := Scrub(in)
	if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "APP_URL=") }) {
		t.Error("APP_URL should have been dropped")
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "INTERNAL_QUEUE=") }) {
		t.Error("INTERNAL_QUEUE should have been dropped by the prefix glob")
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "DATABASE_URL=") }) {
		t.Error("an explicit deny list replaces the default, so DATABASE_URL should survive")
	}
}

func TestScrubDisabled(t *testing.T) {
	reset(t, "-")
	in := []string{"DATABASE_URL=postgres://x", "PATH=/usr/bin"}
	got := Scrub(in)
	if len(got) != 2 {
		t.Fatalf("scrubbing should be off with %s=-, got %v", DenyEnvVar, got)
	}
}

func TestScrubPassesThroughMalformedEntries(t *testing.T) {
	reset(t, "")
	// os/exec accepts whatever it is handed; reshaping entries is not
	// this function's job, and dropping them would change behaviour.
	in := []string{"NOEQUALS", "=leading-equals", "PATH=/usr/bin"}
	got := Scrub(in)
	for _, want := range in {
		if !slices.Contains(got, want) {
			t.Errorf("malformed entry %q was not passed through: %v", want, got)
		}
	}
}
