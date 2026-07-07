// Package startupscript runs an admin-defined shell script when the
// server boots. Use case: spawn a tunnel (ngrok / cloudflared) so the
// local HTTP port stays unexposed to the LAN — the admin configures
// the command via /admin/variables (startup_script + startup_script_enabled)
// and the server fires it once Bootstrap finishes.
//
// Lifetime is tied to the caller's context: when the context cancels
// (server stop / tray quit) the subprocess and every child it spawned
// receive a kill signal via the platform's process-group mechanism
// (Setpgid + kill -pgid on Unix, Job Object kill-on-close on Windows).
// Without this, a backgrounded `ngrok &` survives wick shutdown as an
// orphan re-parented to init — leaking tunnels and accumulating zombies
// on every restart.
//
// Output (stdout + stderr) is appended to
// ~/.<appName>/logs/startup-script-YYYY-MM-DD.log.
//
// Multi-line scripts run sequentially in one shell process, exactly
// like a `.sh` / `.ps1` file. Long-running foreground commands block
// subsequent lines — use `&` (Unix) or `Start-Process` (Windows) for
// parallel daemons. Edits to the script row only take effect on next
// server boot.
package startupscript

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yogasw/wick/pkg/safeexec"
	"github.com/yogasw/wick/internal/userconfig"
)

// Run spawns the script in a fresh shell, waits for context cancel,
// then kills the subprocess plus every descendant via the platform's
// process-group mechanism. Blocking call — callers spawn it in a
// goroutine. Returns immediately with nil when script is empty or only
// whitespace.
//
// appName scopes the log file location (~/.<appName>/logs/...). Pass
// the same string used elsewhere (APP_NAME env / userconfig.Dir input)
// so logs land next to server/worker/app logs.
func Run(ctx context.Context, appName, script string) error {
	l := zerolog.Ctx(ctx).With().Str("component", "startupscript").Logger()

	// CRLF -> LF: browser textareas submit \r\n on Windows, and sh
	// chokes on the stray \r with "$'\r': command not found" on every
	// non-trailing line. PowerShell is tolerant either way.
	script = strings.ReplaceAll(script, "\r\n", "\n")
	script = strings.TrimSpace(script)
	if script == "" {
		l.Debug().Msg("empty script, nothing to do")
		return nil
	}

	logFile, err := openLog(appName)
	if err != nil {
		l.Warn().Err(err).Msg("open log file failed, falling back to discard")
		logFile = nopCloser{io.Discard}
	}
	defer logFile.Close()

	shell, args := shellInvocation()
	// Plain exec.Command (not CommandContext): ctx cancel must trigger
	// our process-group kill, not exec's per-PID kill which would leave
	// `ngrok &` orphans behind.
	cmd := safeexec.Command(shell, args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	applyProcessGroup(cmd)

	header := fmt.Sprintf("\n=== %s startup-script begin (shell=%s) ===\n", time.Now().Format(time.RFC3339), shell)
	_, _ = logFile.Write([]byte(header))

	if err := cmd.Start(); err != nil {
		l.Error().Err(err).Str("shell", shell).Msg("start failed")
		_, _ = logFile.Write([]byte(fmt.Sprintf("start failed: %s\n", err)))
		return fmt.Errorf("start startup script: %w", err)
	}
	pid := cmd.Process.Pid
	assignToJob(cmd)
	l.Info().Int("pid", pid).Str("shell", shell).Msg("startup script running")

	// Track + clean up the OS-level group container (Job Object handle
	// on Windows; no-op on Unix). Must outlive cmd.Wait so deferred
	// cleanup fires even on normal exit.
	defer releaseProcessGroup(cmd)

	// Watch ctx in parallel with the shell. First trigger wins:
	//   - ctx done  -> kill the whole process group, then Wait returns
	//   - cmd exits -> done channel closes, watcher returns cleanly
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			l.Info().Int("pid", pid).Msg("ctx cancelled, killing process group")
			if err := killProcessGroup(cmd); err != nil {
				l.Warn().Err(err).Msg("kill process group")
			}
		case <-done:
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	footer := fmt.Sprintf("=== %s startup-script ended ===\n", time.Now().Format(time.RFC3339))
	_, _ = logFile.Write([]byte(footer))

	if waitErr != nil && ctx.Err() == nil {
		l.Warn().Err(waitErr).Msg("startup script exited with error")
		return waitErr
	}
	l.Info().Msg("startup script stopped")
	return nil
}

// shellInvocation returns the interpreter + args used to feed the
// script through stdin. PowerShell's "-Command -" reads the pipeline
// from stdin; sh's "-s" does the same on POSIX shells.
func shellInvocation() (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell.exe", []string{"-NoProfile", "-Command", "-"}
	}
	return "sh", []string{"-s"}
}

func openLog(appName string) (io.WriteCloser, error) {
	dir, err := userconfig.Dir(appName)
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "startup-script-"+time.Now().Format("2006-01-02")+".log")
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// nopCloser wraps an io.Writer with a no-op Close so the openLog
// fallback path (writing to io.Discard) satisfies io.WriteCloser.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
