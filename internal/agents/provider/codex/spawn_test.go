package codex

import (
	"reflect"
	"testing"

	"context"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestSpawnerArgv verifies the argv-construction logic for codex exec.
// Uses a non-existent binary so Start errors fast; we read Argv() before
// the process layer can reject the binary (on Windows, exec.Cmd.Args is
// populated before Start is called).
func TestSpawnerArgv(t *testing.T) {
	cases := []struct {
		name       string
		spawner    Spawner
		opt        provider.SpawnOptions
		wantArgs   []string
		wantNoFlag string
	}{
		{
			name:    "default headless — danger-full-access sandbox",
			spawner: Spawner{},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
			},
		},
		{
			name:    "sandbox workspace-write via CodexConfig",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				Instance:  &provider.Instance{CodexConfig: &provider.CodexConfig{SandboxMode: provider.CodexSandboxWorkspaceWrite}},
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "workspace-write",
			},
		},
		{
			name:    "sandbox read-only via CodexConfig",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				Instance:  &provider.Instance{CodexConfig: &provider.CodexConfig{SandboxMode: provider.CodexSandboxReadOnly}},
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "read-only",
			},
		},
		{
			name:    "with initial message as positional arg",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace:      t.TempDir(),
				InitialMessage: "hello codex",
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
				"hello codex",
			},
		},
		{
			name:    "resume id uses exec resume subcommand",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ResumeID:  "abc-123",
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
				"resume", "abc-123",
			},
		},
		{
			name:    "resume with message",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace:      t.TempDir(),
				ResumeID:       "abc-123",
				InitialMessage: "follow up",
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
				"resume", "abc-123",
				"follow up",
			},
		},
		{
			name:    "extra args before resume",
			spawner: Spawner{ExtraArgs: []string{"--model", "o3"}},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ResumeID:  "xyz",
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
				"--model", "o3",
				"resume", "xyz",
			},
		},
		{
			name:    "ask-for-approval when no gate",
			spawner: Spawner{AskForApproval: "never"},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"--sandbox", "danger-full-access",
				"--ask-for-approval", "never",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spawner.Binary = "codex-nonexistent-for-test"
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
						t.Errorf("argv should NOT contain %q, got %v", tc.wantNoFlag, got)
					}
				}
			}
		})
	}
}
