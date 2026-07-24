package wick

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/yogasw/wick/pkg/safeexec"
)

// shell_resolve.go finds a real Unix-like bash for the shell tool.
//
// The shell tool MUST run through bash — never cmd.exe / PowerShell as the
// primary interpreter. Wrapping commands in `cmd /c "..."` forces a second
// layer of shell quoting that mangles the command before bash (or whatever
// the agent intended) ever sees it: double quotes become `\"`, multi-line
// heredocs get corrupted, and Windows-style paths leak into a Unix
// context. See internal/planning/in-progress/wick-provider/plan.md
// "Shell provider spec" for the full bug report and required test suite —
// every regression there traces back to a cmd.exe wrapper.
//
// On Linux/macOS, "bash" on PATH is bash. On Windows there is no system
// bash, so we search the common install locations for a real one (Git for
// Windows' bash.exe, MSYS2, WSL's bash shim) in addition to PATH.

var (
	bashOnce sync.Once
	bashPath string
	bashErr  error
)

// resolveBash returns the absolute path to a bash executable, caching the
// result for the process lifetime (the environment doesn't change mid-run).
func resolveBash() (string, error) {
	bashOnce.Do(func() {
		bashPath, bashErr = findBash()
	})
	return bashPath, bashErr
}

func findBash() (string, error) {
	if p, err := safeexec.LookPath("bash"); err == nil {
		return p, nil
	}
	if runtime.GOOS != "windows" {
		return "", errors.New("bash not found on PATH")
	}
	for _, c := range windowsBashCandidates() {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("bash not found — install Git for Windows, MSYS2, or WSL (cmd.exe/PowerShell are not used as shell interpreters)")
}

// windowsBashCandidates lists the well-known bash locations on Windows,
// checked when PATH lookup misses. Order = most to least common install.
func windowsBashCandidates() []string {
	var out []string
	progFiles := os.Getenv("ProgramFiles")
	progFilesX86 := os.Getenv("ProgramFiles(x86)")
	sysRoot := os.Getenv("SystemRoot")
	userProfile := os.Getenv("USERPROFILE")

	add := func(base, rel string) {
		if base == "" {
			return
		}
		out = append(out, filepath.Join(base, rel))
	}
	// Git for Windows.
	add(progFiles, filepath.Join("Git", "bin", "bash.exe"))
	add(progFiles, filepath.Join("Git", "usr", "bin", "bash.exe"))
	add(progFilesX86, filepath.Join("Git", "bin", "bash.exe"))
	add(userProfile, filepath.Join("scoop", "apps", "git", "current", "bin", "bash.exe"))
	// MSYS2 default install.
	out = append(out, `C:\msys64\usr\bin\bash.exe`, `C:\msys32\usr\bin\bash.exe`)
	// WSL's bash shim (bridges to a real Linux bash inside the WSL VM).
	add(sysRoot, filepath.Join("System32", "bash.exe"))
	return out
}
