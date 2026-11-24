package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/safeexec"
)

// cappedOutput bounds captured stdout and stderr independently, while
// continuing to consume output so a noisy child cannot deadlock.
type cappedOutput struct {
	mu sync.Mutex
	buf bytes.Buffer
	truncated bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	left := 256*1024 - w.buf.Len()
	if n > left {
		w.truncated = true
		p = p[:left]
	}
	_, _ = w.buf.Write(p)
	return n, nil
}

func (w *cappedOutput) snapshot() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String(), w.truncated
}

func parseArgs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var args []string
	if len(raw) > 64*1024 || json.Unmarshal([]byte(raw), &args) != nil || args == nil || len(args) > 256 {
		return nil, errors.New("args_json must be an array of at most 256 strings")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("invalid NUL in argument")
		}
	}
	return args, nil
}

// Do not inherit plugin authentication tokens or arbitrary host environment
// variables. This reduces accidental disclosure; it is not a sandbox.
func cleanEnv() []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "PREFIX", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func resolveCommand(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", errors.New("command required")
	}
	if strings.ContainsAny(name, "/\\") && !filepath.IsAbs(name) {
		return "", errors.New("command must be a bare name or absolute executable")
	}
	// Resolve explicitly before constructing Cmd: a missing executable
	// must not fall back to Go's faccessat2-based PATH lookup on Termux.
	bin, err := safeexec.LookPath(name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(bin) {
		return "", errors.New("non-absolute executable refused")
	}
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(bin))
		if ext == ".bat" || ext == ".cmd" {
			return "", errors.New("batch files are not supported by direct execution")
		}
	}
	return bin, nil
}

func executeCommand(c *connector.Ctx, shell bool) (any, error) {
	p, err := policyFor(c)
	if err != nil {
		return nil, err
	}
	name := c.Input("command")
	args, err := parseArgs(c.Input("args_json"))
	if err != nil {
		return nil, err
	}
	if shell {
		// Command-name filtering cannot constrain an arbitrary script.
		// Require explicit unrestricted shell trust, with no command
		// exclusions that would imply a guarantee we cannot enforce.
		if p.Terminal.Mode != "all" || len(p.Terminal.Deny) != 0 || len(p.Terminal.Approval) != 0 {
			return nil, errors.New("shell requires unrestricted terminal policy without command exclusions")
		}
		name, args = "bash", []string{"-c", c.Input("script")}
		if runtime.GOOS == "windows" {
			name, args = "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", c.Input("script")}
		}
		if c.Input("script") == "" || len(c.Input("script")) > 64*1024 {
			return nil, errors.New("script required, maximum 64 KiB")
		}
	}
	if verdict, reason := p.commandVerdict(name, args, shell); verdict != "allow" {
		return nil, fmt.Errorf("%s: %s", verdict, reason)
	}
	bin, err := resolveCommand(name)
	if err != nil {
		return nil, err
	}
	// Re-evaluate the resolved path, so absolute-path exclusions also
	// apply when the caller supplied a bare executable name.
	if verdict, reason := p.commandVerdict(bin, args, shell); verdict != "allow" {
		return nil, fmt.Errorf("%s: %s", verdict, reason)
	}
	dir, err := p.authorize("list", c.Input("cwd"))
	if err != nil {
		return nil, err
	}
	info, err := statFile(dir)
	if err != nil || info["type"] != "directory" {
		return nil, errors.New("cwd must be an accessible directory")
	}
	timeout := p.Terminal.TimeoutSeconds
	if timeout == 0 {
		timeout = 60
	}
	if requested := c.InputInt("timeout_seconds"); requested > 0 && requested < timeout {
		timeout = requested
	}
	ctx, cancel := context.WithTimeout(c.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := safeexec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr cappedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err = cmd.Run()
	out, outTruncated := stdout.snapshot()
	errText, errTruncated := stderr.snapshot()
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	result := map[string]any{
		"ok": err == nil, "exit_code": exit, "stdout": out, "stderr": errText,
		"truncated": outTruncated || errTruncated, "duration_ms": time.Since(start).Milliseconds(),
		"timed_out": errors.Is(ctx.Err(), context.DeadlineExceeded), "sandboxed": false,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result, nil
}
