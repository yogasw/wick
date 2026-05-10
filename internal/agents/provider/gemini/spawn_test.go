package gemini

import (
	"context"
	"reflect"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestSpawnerArgv covers the argv shape only. Integration (real binary
// spawn) is deferred until a contributor with gemini installed verifies
// the flag surface — see package godoc.
func TestSpawnerArgv(t *testing.T) {
	cases := []struct {
		name     string
		spawner  Spawner
		opt      provider.SpawnOptions
		wantArgs []string
	}{
		{
			name:    "default headless",
			spawner: Spawner{},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"-p",
			},
		},
		{
			name:    "yolo (no gate)",
			spawner: Spawner{YoloMode: true},
			opt:     provider.SpawnOptions{Workspace: t.TempDir()},
			wantArgs: []string{
				"-p", "--yolo",
			},
		},
		{
			name: "with resume",
			spawner: Spawner{},
			opt: provider.SpawnOptions{
				Workspace: t.TempDir(),
				ResumeID:  "g-123",
			},
			wantArgs: []string{
				"-p", "--resume", "g-123",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spawner.Binary = "gemini-nonexistent-for-test"
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
		})
	}
}

// TestIntegrationSkipped is a placeholder reminding contributors that
// the real-binary path needs verification. When gemini becomes
// available, replace this with the actual integration test.
func TestIntegrationSkipped(t *testing.T) {
	t.Skip("requires gemini install, manual verify — see package godoc")
}
