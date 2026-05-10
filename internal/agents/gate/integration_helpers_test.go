package gate_test

import (
	"io"
	"os"
)

// pathEnv returns the current PATH so subprocess tests can find go,
// claude, etc. without inheriting the parent's full env.
func pathEnv() string { return os.Getenv("PATH") }

// pipePair is os.Pipe wrapped to match io.PipeReader / Writer
// expectations the subprocess needs (stdin must be an *os.File for
// exec.Cmd to wire it without an intermediate goroutine).
func pipePair() (*os.File, *os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	return r, w, nil
}

// ensure io is referenced for tests that may add more readers later
var _ = io.EOF
