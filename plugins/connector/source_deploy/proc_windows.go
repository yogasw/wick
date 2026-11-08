//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/yogasw/wick/pkg/safeexec"
	"golang.org/x/sys/windows"
)

// launch starts the .ps1 detached from wick's console and process group: the script
// stops wick to swap the .exe (Windows has no handover), and must outlive that. A small
// wrapper next to the log writes the exit code for the wick that starts afterwards.
func launch(script string, args []string, exitFile, runID string, logf *os.File) (int, error) {
	if !strings.HasSuffix(strings.ToLower(script), ".ps1") {
		return 0, errors.New("on Windows the deploy script must be a .ps1")
	}
	ps, err := safeexec.LookPath("pwsh")
	if err != nil {
		if ps, err = safeexec.LookPath("powershell"); err != nil {
			return 0, errors.New("no PowerShell on PATH")
		}
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, psQuote(a))
	}
	body := "& " + psQuote(script) + " " + strings.Join(quoted, " ") + "\r\n" +
		"$code = if ($?) { if ($LASTEXITCODE) { $LASTEXITCODE } else { 0 } } else { if ($LASTEXITCODE) { $LASTEXITCODE } else { 1 } }\r\n" +
		"Set-Content -NoNewline -Path " + psQuote(exitFile) + " -Value $code\r\n" +
		"& $env:WICK_DEPLOY_NOTIFY_EXE --notify-run\r\n" +
		"exit $code\r\n"
	wrap := filepath.Join(filepath.Dir(exitFile), strings.TrimSuffix(filepath.Base(exitFile), ".exit")+".wrap.ps1")
	if err := os.WriteFile(wrap, []byte(body), 0o600); err != nil {
		return 0, err
	}
	cmd := safeexec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", wrap)
	cmd.Env = childEnv(exitFile, runID)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
	if dir, err := os.UserHomeDir(); err == nil {
		cmd.Dir = dir
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func killTree(pid int) error {
	if pid <= 0 {
		return errors.New("no pid")
	}
	return safeexec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
