package claude

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestClaudeSpawnThinkingTokensInjectsEnv asserts the claude spawn env
// gains MAX_THINKING_TOKENS=<value> when ThinkingTokens is set: "0" is the
// workflow agent node's thinking:off path; a positive value is a budget cap.
func TestClaudeSpawnThinkingTokensInjectsEnv(t *testing.T) {
	cases := []struct {
		name   string
		tokens string
		want   string
	}{
		{name: "off disables thinking", tokens: "0", want: "MAX_THINKING_TOKENS=0"},
		{name: "budget caps thinking", tokens: "2048", want: "MAX_THINKING_TOKENS=2048"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := spawnEnv(nil, provider.SpawnOptions{ThinkingTokens: tc.tokens})
			if !slices.Contains(env, tc.want) {
				t.Fatalf("expected %q in env, got %v", tc.want, env)
			}
		})
	}
}

// TestClaudeSpawnThinkingOnByDefault is the chat-isolation regression
// guard: a spawn with an empty ThinkingTokens (the chat / default path)
// must NOT carry any MAX_THINKING_TOKENS env so chat behaviour stays
// byte-identical.
func TestClaudeSpawnThinkingOnByDefault(t *testing.T) {
	env := spawnEnv(nil, provider.SpawnOptions{})
	for _, e := range env {
		if strings.HasPrefix(e, "MAX_THINKING_TOKENS=") {
			t.Fatalf("chat/default spawn must NOT set MAX_THINKING_TOKENS, got %q", e)
		}
	}
}

// TestClaudeSpawnEnvPreservesExtraEnv ensures the thinking entry is
// additive: existing ExtraEnv entries are preserved and not mutated.
func TestClaudeSpawnEnvPreservesExtraEnv(t *testing.T) {
	base := []string{"BASE=1"}
	extra := []string{"FOO=bar"}
	env := spawnEnv(base, provider.SpawnOptions{ExtraEnv: extra, ThinkingTokens: "0"})
	for _, want := range []string{"BASE=1", "FOO=bar", "MAX_THINKING_TOKENS=0"} {
		if !slices.Contains(env, want) {
			t.Fatalf("expected %q in env, got %v", want, env)
		}
	}
	if len(extra) != 1 || extra[0] != "FOO=bar" {
		t.Fatalf("ExtraEnv was mutated: %v", extra)
	}
}

// TestSpawnerArgv verifies argv-construction for the claude spawner.
// Uses a non-existent binary so Start errors fast; Argv() is populated
// before Start, so we can inspect it without a real claude binary.
func TestSpawnerArgv(t *testing.T) {
	cases := []struct {
		name       string
		spawner    Spawner
		opt        provider.SpawnOptions
		wantArgs   []string
		wantNoFlag string
	}{
		{
			name:    "default headless",
			spawner: Spawner{},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
			},
		},
		{
			name:    "spawner ExtraArgs forwarded",
			spawner: Spawner{ExtraArgs: []string{"--debug"}},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--debug",
			},
		},
		{
			name:    "opt.ExtraArgs (instance config) forwarded",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ExtraArgs: []string{"--verbose-extra"},
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose-extra",
			},
		},
		{
			name:    "spawner ExtraArgs + opt.ExtraArgs both forwarded",
			spawner: Spawner{ExtraArgs: []string{"--debug"}},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ExtraArgs: []string{"--verbose-extra"},
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--debug",
				"--verbose-extra",
			},
		},
		{
			name:    "with resume",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ResumeID:  "sess-abc",
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--resume", "sess-abc",
			},
		},
		{
			name:    "opt.ExtraArgs before resume",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ResumeID:  "sess-abc",
				ExtraArgs: []string{"--model", "claude-opus-4-5"},
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--model", "claude-opus-4-5",
				"--resume", "sess-abc",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spawner.Binary = "claude-nonexistent-for-test"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			p, err := tc.spawner.Spawn(ctx, tc.opt)
			if err == nil {
				_ = p.Kill()
			}
			if p == nil {
				t.Skip("could not inspect argv — Spawn failed before exec.Cmd was wired")
			}
			got := p.Argv()
			if !reflect.DeepEqual(got, tc.wantArgs) {
				t.Errorf("Argv\n  got:  %v\n  want: %v", got, tc.wantArgs)
			}
			if tc.wantNoFlag != "" {
				for _, a := range got {
					if a == tc.wantNoFlag {
						t.Errorf("argv must NOT contain %q, got %v", tc.wantNoFlag, got)
					}
				}
			}
		})
	}
}
