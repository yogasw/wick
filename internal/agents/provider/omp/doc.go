// Package omp implements provider.Spawner for oh-my-pi (`omp`).
//
// One process per turn (SendRespawnQueue), like codex exec:
//
//	omp --profile <p> -p --mode json --cwd <ws> --no-title --auto-approve
//	    [--append-system-prompt <file>] [--model <m>] [--resume <sid>]
//	    <prompt on stdin>
//
// Every flag is checked against the oh-my-pi source (commit fc671eb):
// packages/coding-agent/src/cli/args.ts (--profile, -p, --no-title,
// --yolo), cli/flag-tables.ts (--mode, --cwd, --model, --append-system-prompt,
// --resume), main.ts readPipedInput (non-TTY stdin = prompt). The stream is
// parsed by event.OMPParser.
//
// Account isolation: the instance's profile (provider.OMPProfile) is the
// FIRST argv pair on every spawn, and the same helper feeds login/usage.
//
// Gate/hook capability is OFF: omp hooks are TypeScript extensions under
// .omp/hooks, not a command contract wick's gate can speak.
package omp
