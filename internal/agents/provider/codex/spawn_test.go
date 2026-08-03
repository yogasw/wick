package codex

import (
	"os"
	"path/filepath"
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
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
				"-c", "features.multi_agent=false",
				"--sandbox", "danger-full-access",
				"--ask-for-approval", "never",
			},
		},
		{
			name:    "opt.ExtraArgs (instance config) forwarded",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ExtraArgs: []string{"--model", "o4-mini"},
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"-c", "features.multi_agent=false",
				"--sandbox", "danger-full-access",
				"--model", "o4-mini",
			},
		},
		{
			name:    "spawner ExtraArgs + opt.ExtraArgs both forwarded",
			spawner: Spawner{ExtraArgs: []string{"--model", "o3"}},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ExtraArgs: []string{"--timeout", "60"},
			},
			wantArgs: []string{
				"exec",
				"--json",
				"--skip-git-repo-check",
				"-c", "features.multi_agent=false",
				"--sandbox", "danger-full-access",
				"--model", "o3",
				"--timeout", "60",
			},
		},
	}

	// Pin home to an empty temp dir so ~/.codex/skills doesn't exist and
	// the skill --add-dir stays out of these exact-argv assertions
	// regardless of the dev/CI machine's real home. The --add-dir path is
	// covered separately by TestSpawnerArgv_SkillsDir.
	restore := homeDir
	homeDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { homeDir = restore })

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

// TestSpawnerArgv_SkillsDir verifies codex is granted --add-dir
// ~/.codex/skills when that dir exists, so a sandboxed agent can read
// skill bundle files (the claude-vs-codex gap the retest surfaced).
func TestSpawnerArgv_SkillsDir(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	restore := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = restore })

	sp := Spawner{Binary: "codex-nonexistent-for-test"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := sp.Spawn(ctx, provider.SpawnOptions{Workspace: t.TempDir()})
	if err == nil {
		_ = p.Kill()
	}
	if p == nil {
		t.Skip("could not inspect argv — Spawn failed before exec.Cmd was wired")
	}

	got := p.Argv()
	var sawAddDir bool
	for i, a := range got {
		if a == "--add-dir" && i+1 < len(got) && got[i+1] == skills {
			sawAddDir = true
		}
	}
	if !sawAddDir {
		t.Errorf("argv missing --add-dir %s\n  got: %v", skills, got)
	}
}
