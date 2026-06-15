package claude

import (
	"context"
	"reflect"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

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
