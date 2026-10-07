package omp

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// A `-p` turn has no RPC get_state to read the model's context window
// from, so the meter had no scale there. The spawn looks the --model it
// passes up in the profile's models.db and puts wick's context line in
// front of omp's stream — the same line the RPC path emits. No --model
// (omp runs its own default) or a model models.db does not list: no line,
// the meter shows tokens without a percentage, as before.

// modelArg is the value of the last --model in args ("" when none).
func modelArg(args []string) string {
	m := ""
	for i, a := range args {
		switch {
		case (a == "--model" || a == "-m") && i+1 < len(args):
			m = args[i+1]
		case strings.HasPrefix(a, "--model="):
			m = strings.TrimPrefix(a, "--model=")
		}
	}
	return m
}

// spawnWindow is the context window of the model args pin, from ins's
// models.db; 0 when unknown.
func spawnWindow(ctx context.Context, ins provider.Instance, args []string) int {
	sel := modelArg(args)
	if sel == "" {
		return 0
	}
	agent := instanceAgentDir(ins)
	if agent == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db := filepath.Join(agent, "models.db")
	if w := modelsDBWindow(ctx, db, sel); w > 0 {
		return w
	}
	// "provider/model:high" — a thinking level after the id.
	if i := strings.LastIndexByte(sel, ':'); i > strings.IndexByte(sel, '/') {
		return modelsDBWindow(ctx, db, sel[:i])
	}
	return 0
}

// withContextLine puts wick's context line for window ahead of r.
func withContextLine(r io.ReadCloser, window int) io.ReadCloser {
	if window <= 0 {
		return r
	}
	line := event.ContextLine(window)
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(line), r), r}
}
