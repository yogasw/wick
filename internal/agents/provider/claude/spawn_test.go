package claude

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/pkg/safeexec"
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
			// First spawn of a uuid session: wick names the conversation, so
			// claude's transcript is keyed by the same id wick uses.
			name:    "session-id names a fresh uuid session",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				SessionID: "39141fdf-48df-4fd1-93d3-76256f29dad5",
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--session-id", "39141fdf-48df-4fd1-93d3-76256f29dad5",
			},
		},
		{
			// A recorded resume id wins — claude refuses to re-claim a name
			// it has already seen, so naming only happens once.
			name:    "resume beats session-id",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				SessionID: "39141fdf-48df-4fd1-93d3-76256f29dad5",
				ResumeID:  "39141fdf-48df-4fd1-93d3-76256f29dad5",
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--resume", "39141fdf-48df-4fd1-93d3-76256f29dad5",
			},
			wantNoFlag: "--session-id",
		},
		{
			// Sub-agent ids ("<parent>--sub-<seg>") are not uuids; claude
			// demands one, so it mints its own instead of being handed a
			// flag it would exit on.
			name:    "non-uuid session id gets neither flag",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				SessionID: "parent--sub-a1b2c3d4e5f6",
			},
			wantArgs: []string{
				"-p", "--verbose",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
			},
			wantNoFlag: "--session-id",
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

// TestSpawnerSessionIDTranscriptOnDisk pins the guard against re-naming a
// conversation claude already holds: instances sharing a config dir all
// name it with the wick session id, so a spawn that lost its ResumeID must
// resume the transcript on disk instead of passing --session-id (which
// claude rejects as "already in use").
func TestSpawnerSessionIDTranscriptOnDisk(t *testing.T) {
	const sid = "39141fdf-48df-4fd1-93d3-76256f29dad5"
	cases := []struct {
		name       string
		transcript bool
		resumeID   string
		wantTail   []string
		wantNoFlag string
	}{
		{"transcript on disk resumes it", true, "", []string{"--resume", sid}, "--session-id"},
		{"no transcript names the session", false, "", []string{"--session-id", sid}, "--resume"},
		{"recorded resume id is kept", true, "sess-abc", []string{"--resume", "sess-abc"}, "--session-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			ws := t.TempDir()
			if tc.transcript {
				dir := filepath.Join(cfg, "projects", EncodeWorkspace(ws))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// A real binary, so Spawn gets as far as exec and argv can be
			// read; `true` ignores the flags and exits.
			bin, lerr := safeexec.LookPath("true")
			if lerr != nil {
				t.Skip("no `true` binary on PATH")
			}
			p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{
				Workspace: ws,
				SessionID: sid,
				ResumeID:  tc.resumeID,
				ExtraEnv:  []string{"CLAUDE_CONFIG_DIR=" + cfg},
			})
			if err == nil {
				_ = p.Kill()
			}
			if p == nil {
				t.Skip("could not inspect argv — Spawn failed before exec.Cmd was wired")
			}
			// The session flag is appended last; everything before it is
			// covered by TestSpawnerArgv.
			got := p.Argv()
			if len(got) < 2 || !reflect.DeepEqual(got[len(got)-2:], tc.wantTail) {
				t.Errorf("Argv tail\n  got:  %v\n  want: %v", got, tc.wantTail)
			}
			if slices.Contains(got, tc.wantNoFlag) {
				t.Errorf("argv must NOT contain %q, got %v", tc.wantNoFlag, got)
			}
		})
	}
}

func TestClaudeConfigDirFromEnv(t *testing.T) {
	home := filepath.Join("/", "home", "u")
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"unset falls back to home", []string{"PATH=/bin"}, filepath.Join(home, ".claude")},
		{"set wins", []string{"CLAUDE_CONFIG_DIR=/cfg/a"}, "/cfg/a"},
		{"last entry wins", []string{"CLAUDE_CONFIG_DIR=/cfg/a", "CLAUDE_CONFIG_DIR=/cfg/b"}, "/cfg/b"},
		{"empty value counts as unset", []string{"CLAUDE_CONFIG_DIR=/cfg/a", "CLAUDE_CONFIG_DIR="}, filepath.Join(home, ".claude")},
	}
	for _, tc := range cases {
		if got := claudeConfigDirFromEnv(tc.env, home); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
