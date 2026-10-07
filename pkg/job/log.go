package job

import (
	"context"
	"fmt"
)

type logKey struct{}

// WithLogger attaches a progress-line sink to ctx. The plugin runtime sets it
// so lines a job logs stream back to the host's run history; in-process jobs
// run without one and Logf is a no-op.
func WithLogger(parent context.Context, fn func(line string)) context.Context {
	return context.WithValue(parent, logKey{}, fn)
}

// Logf records one progress line for the current run.
func Logf(ctx context.Context, format string, args ...any) {
	if fn, ok := ctx.Value(logKey{}).(func(string)); ok && fn != nil {
		fn(fmt.Sprintf(format, args...))
	}
}
