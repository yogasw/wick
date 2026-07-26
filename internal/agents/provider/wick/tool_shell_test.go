package wick

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tool_shell_test.go covers the shell provider spec: the shell tool must
// always run through a real bash — never wrap the command through
// cmd.exe/PowerShell — because a second shell layer re-quotes the string
// and mangles it before bash sees it (double quotes become \", multi-line
// heredocs get corrupted, Windows paths leak into a Unix context). See
// internal/planning/in-progress/wick-provider/plan.md "Shell provider
// spec" for the full bug report this suite guards against regressing.
//
// Tests skip (not fail) when bash isn't resolvable on the host, so CI
// environments without git-bash/WSL don't false-fail — but on any
// developer machine with Git for Windows or on Linux/macOS these must
// all pass before the shell tool is considered "ready".

func runShell(t *testing.T, cmdline string) (string, bool) {
	t.Helper()
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available on this host: %v", err)
	}
	td := shellTool(toolContext{Workspace: t.TempDir()})
	return td.handler(context.Background(), map[string]any{"command": cmdline})
}

// ── A. Basic execution ────────────────────────────────────────────────

func TestShell_EchoHello(t *testing.T) {
	out, isErr := runShell(t, "echo hello")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("want output containing hello, got %q", out)
	}
}

func TestShell_PwdInWorktree(t *testing.T) {
	out, isErr := runShell(t, "pwd")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "/") {
		t.Errorf("pwd should be an absolute Unix path, got %q", out)
	}
}

func TestShell_FalseExitCode(t *testing.T) {
	out, isErr := runShell(t, "false")
	if !isErr {
		t.Error("`false` should report isErr=true")
	}
	if out == "" {
		t.Error("response body should not be empty on nonzero exit")
	}
}

func TestShell_StderrCaptured(t *testing.T) {
	out, _ := runShell(t, "echo err-msg 1>&2")
	if !strings.Contains(out, "err-msg") {
		t.Errorf("stderr should be captured in combined output, got %q", out)
	}
}

// ── B. Quoting — the most commonly broken area ─────────────────────────

func TestShell_DoubleQuotePreserved(t *testing.T) {
	out, isErr := runShell(t, `echo "hello world"`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "hello world" {
		t.Errorf(`double quotes were mangled (want "hello world"), got %q`, out)
	}
}

func TestShell_SingleQuote(t *testing.T) {
	out, isErr := runShell(t, `echo 'hi there'`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "hi there" {
		t.Errorf("want 'hi there', got %q", out)
	}
}

func TestShell_NestedQuotes(t *testing.T) {
	out, isErr := runShell(t, `bash -c 'echo "hello world"'`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "hello world" {
		t.Errorf("nested quotes broken, got %q", out)
	}
}

func TestShell_JSONInline(t *testing.T) {
	out, isErr := runShell(t, `echo "{\"a\":1}"`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, `"a":1`) {
		t.Errorf("JSON-shaped double quotes were mangled, got %q", out)
	}
}

func TestShell_Base64Roundtrip(t *testing.T) {
	out, isErr := runShell(t, "echo SGVsbG8= | base64 -d")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "Hello" {
		t.Errorf("want Hello, got %q", out)
	}
}

// ── C. Multi-line & heredoc ──────────────────────────────────────────

func TestShell_MultilinePrintf(t *testing.T) {
	out, isErr := runShell(t, "printf '%s\\n' 'line1' 'line2'")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "line1\nline2" {
		t.Errorf("want line1\\nline2, got %q", out)
	}
}

func TestShell_HeredocLiteral(t *testing.T) {
	cmd := "cat <<'EOF'\nalpha\nbeta\nEOF"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "alpha\nbeta" {
		t.Errorf("heredoc broken, want alpha\\nbeta, got %q", out)
	}
}

func TestShell_HeredocNoExpand(t *testing.T) {
	cmd := "X=secret\ncat <<'EOF'\n$X\nEOF"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "$X" {
		t.Errorf("quoted heredoc should not expand variables, got %q", out)
	}
}

func TestShell_MultilineChain(t *testing.T) {
	// A multi-command script body must survive as one coherent unit — not
	// be split/joined/truncated across lines by an intermediate shell.
	cmd := "a=1\nb=2\necho $((a+b))"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "3" {
		t.Errorf("multi-line script broken, want 3, got %q", out)
	}
}

// ── D. File I/O in the worktree ────────────────────────────────────────

func TestShell_WriteRead(t *testing.T) {
	out, isErr := runShell(t, "echo content-ok > t30.txt\ncat t30.txt")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "content-ok") {
		t.Errorf("want content-ok, got %q", out)
	}
}

func TestShell_Append(t *testing.T) {
	cmd := "printf 'a\\n' > t31.txt\nprintf 'b\\n' >> t31.txt\ncat t31.txt"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if strings.TrimSpace(out) != "a\nb" {
		t.Errorf("want a\\nb, got %q", out)
	}
}

// ── E. Path & cwd ──────────────────────────────────────────────────────

func TestShell_RelativePath(t *testing.T) {
	cmd := "mkdir -p sub/dir\necho x > sub/dir/f.txt\ncat sub/dir/f.txt"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "x") {
		t.Errorf("want x, got %q", out)
	}
}

func TestShell_NoWindowsPathLeak(t *testing.T) {
	out, isErr := runShell(t, "pwd")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	trimmed := strings.TrimSpace(out)
	if strings.Contains(trimmed, `\`) {
		t.Errorf("pwd output leaked a Windows-style path: %q", trimmed)
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		t.Errorf("pwd output looks like a Windows drive path: %q", trimmed)
	}
}

// ── F. Chains & scripting realism ──────────────────────────────────────

func TestShell_AndChain(t *testing.T) {
	out, isErr := runShell(t, "echo one && echo two && echo three")
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Errorf("want output containing %q, got %q", want, out)
		}
	}
}

// TestShell_GenerateAndRunScript is the exact repro from the bug report:
// heredoc-write a multiplication table script, then execute it. This must
// succeed in one shell call — if this test fails, the shell tool is not
// "ready" per the acceptance checklist.
func TestShell_GenerateAndRunScript(t *testing.T) {
	cmd := "cat > mul.sh <<'EOF'\n" +
		"#!/usr/bin/env bash\n" +
		"for i in 1 2 3; do\n" +
		"  echo \"2 x $i = $((2*i))\"\n" +
		"done\n" +
		"EOF\n" +
		"bash mul.sh"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	for _, want := range []string{"2 x 1 = 2", "2 x 2 = 4", "2 x 3 = 6"} {
		if !strings.Contains(out, want) {
			t.Errorf("want output containing %q, got %q", want, out)
		}
	}
}

// TestShell_MultiplicationTable11 is the full user-facing repro from the
// report: a 1-10 times-table script via heredoc + bash execution.
func TestShell_MultiplicationTable(t *testing.T) {
	cmd := "cat > perkalian_tabel.sh <<'EOF'\n" +
		"#!/usr/bin/env bash\n" +
		"for i in $(seq 1 10); do\n" +
		"  for j in $(seq 1 10); do\n" +
		"    printf \"%4d\" $((i*j))\n" +
		"  done\n" +
		"  echo\n" +
		"done\n" +
		"EOF\n" +
		"bash perkalian_tabel.sh"
	out, isErr := runShell(t, cmd)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 10 {
		t.Fatalf("want 10 rows, got %d:\n%s", len(lines), out)
	}
	for i := 1; i <= 10; i++ {
		fields := strings.Fields(lines[i-1])
		if len(fields) != 10 {
			t.Fatalf("row %d: want 10 columns, got %d: %q", i, len(fields), lines[i-1])
		}
		for j := 1; j <= 10; j++ {
			got, err := strconv.Atoi(fields[j-1])
			if err != nil || got != i*j {
				t.Errorf("row %d col %d: want %d, got %q", i, j, i*j, fields[j-1])
			}
		}
	}
}

// ── G. Safety / timeout ─────────────────────────────────────────────

// runShellArgs is runShell with extra handler args (e.g. timeout_ms).
func runShellArgs(t *testing.T, args map[string]any) (string, bool) {
	t.Helper()
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available on this host: %v", err)
	}
	td := shellTool(toolContext{Workspace: t.TempDir()})
	return td.handler(context.Background(), args)
}

// TestShell_TimeoutMsHonored proves a caller-supplied timeout_ms actually
// shifts the deadline: a 300ms cap on `sleep 5` must fail near-instantly
// with a deadline message, NOT run for the 10m default.
func TestShell_TimeoutMsHonored(t *testing.T) {
	start := time.Now()
	out, isErr := runShellArgs(t, map[string]any{
		"command":    "sleep 5",
		"timeout_ms": 300,
	})
	elapsed := time.Since(start)
	if !isErr {
		t.Fatalf("sleep 5 with timeout_ms=300 should time out, got ok: %q", out)
	}
	if !strings.Contains(out, "timed out") {
		t.Errorf("want a timeout message, got %q", out)
	}
	// Generous upper bound: the floor is 1s, so this fires ~1s, well under
	// the 5s sleep and the 10m default. Anything past a few seconds means
	// the deadline was ignored.
	if elapsed > 4*time.Second {
		t.Errorf("timeout_ms did not shift the deadline: took %v", elapsed)
	}
}

// TestShell_TimeoutMsSuccessWindow proves a command that finishes inside a
// generous timeout_ms still succeeds (the deadline isn't over-eager).
func TestShell_TimeoutMsSuccessWindow(t *testing.T) {
	out, isErr := runShellArgs(t, map[string]any{
		"command":    "echo done",
		"timeout_ms": 10000,
	})
	if isErr {
		t.Fatalf("fast command under a 10s timeout should succeed, got: %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("want done, got %q", out)
	}
}

func TestResolveShellTimeout(t *testing.T) {
	cases := []struct {
		name string
		arg  any
		want time.Duration
	}{
		{"absent", nil, shellDefaultTimeout},
		{"zero", float64(0), shellDefaultTimeout},
		{"negative", float64(-100), shellDefaultTimeout},
		{"float64", float64(5000), 5 * time.Second},
		{"int", 5000, 5 * time.Second},
		{"numeric string", "5000", 5 * time.Second},
		{"garbage string", "soon", shellDefaultTimeout},
		{"below floor clamps up", float64(10), shellMinTimeout},
		{"above ceiling clamps down", float64(99 * 60 * 1000), shellMaxTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveShellTimeout(map[string]any{"timeout_ms": tc.arg})
			if got != tc.want {
				t.Errorf("resolveShellTimeout(%v) = %v, want %v", tc.arg, got, tc.want)
			}
		})
	}
}
