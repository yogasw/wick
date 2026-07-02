package view

import (
	"strings"
	"testing"
)

func TestQuoting(t *testing.T) {
	// A JSON --mcp-config arg is the hard case: it contains double-quotes.
	json := `{"mcpServers":{"wick":{"type":"http"}}}`

	if got := shellQuote(json); got != "'"+json+"'" {
		t.Errorf("bash: %q", got)
	}
	if got := pwshQuote(json); got != "'"+json+"'" {
		t.Errorf("pwsh: %q", got)
	}
	// cmd doubles internal double-quotes.
	wantCmd := `"` + strings.ReplaceAll(json, `"`, `""`) + `"`
	if got := cmdQuote(json); got != wantCmd {
		t.Errorf("cmd: got %q want %q", got, wantCmd)
	}

	// Single-quote handling.
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("bash squote: %q", got)
	}
	if got := pwshQuote("a'b"); got != "'a''b'" {
		t.Errorf("pwsh squote: %q", got)
	}
	// Safe value is bare in bash but always quoted in pwsh/cmd.
	if got := shellQuote("simple"); got != "simple" {
		t.Errorf("bash safe: %q", got)
	}
}

func TestShellReproduce(t *testing.T) {
	bin := "claude.exe"
	argv := []string{"-p", "--mcp-config", `{"a":"b"}`}
	env := []string{"CLAUDE_CONFIG_DIR=C:/x", "ANTHROPIC_AUTH_TOKEN=tok"}

	bash := ShellReproduceBash(bin, argv, env, false)
	if !strings.Contains(bash, "CLAUDE_CONFIG_DIR=C:/x \\\n") {
		t.Errorf("bash env prefix missing:\n%s", bash)
	}
	if !strings.Contains(bash, "ANTHROPIC_AUTH_TOKEN=tok \\\n") {
		t.Errorf("bash secret prefix missing:\n%s", bash)
	}
	if !strings.HasSuffix(bash, `'{"a":"b"}'`) {
		t.Errorf("bash json arg not single-quoted:\n%s", bash)
	}

	pwsh := ShellReproducePwsh(bin, argv, env, false)
	if !strings.Contains(pwsh, "$env:CLAUDE_CONFIG_DIR='C:/x'\n") {
		t.Errorf("pwsh env prefix missing:\n%s", pwsh)
	}
	if !strings.Contains(pwsh, "\n& 'claude.exe'") {
		t.Errorf("pwsh should invoke binary with call operator &:\n%s", pwsh)
	}
	if !strings.HasSuffix(pwsh, `'{"a":"b"}'`) {
		t.Errorf("pwsh json arg not single-quoted:\n%s", pwsh)
	}

	cmd := ShellReproduceCmd(bin, argv, env, false)
	if !strings.Contains(cmd, `set "CLAUDE_CONFIG_DIR=C:/x"`+"\n") {
		t.Errorf("cmd env prefix missing:\n%s", cmd)
	}
	if !strings.HasSuffix(cmd, `"{""a"":""b""}"`) {
		t.Errorf("cmd json arg quotes not doubled:\n%s", cmd)
	}
}

func TestBinaryPath(t *testing.T) {
	full := `C:\msys64\home\Staffinc\.local\bin\claude.exe`

	// basename strips both separators.
	if got := binaryBasename(full); got != "claude.exe" {
		t.Errorf("basename: %q", got)
	}
	if got := binaryBasename("/usr/local/bin/codex"); got != "codex" {
		t.Errorf("basename posix: %q", got)
	}

	// bash full path rewrites Windows → MSYS /c/ form.
	if got := msysPath(full); got != "/c/msys64/home/Staffinc/.local/bin/claude.exe" {
		t.Errorf("msysPath: %q", got)
	}
	// posix path passes through.
	if got := msysPath("/usr/bin/claude"); got != "/usr/bin/claude" {
		t.Errorf("msysPath posix: %q", got)
	}

	// bash renderer: full → MSYS path; short → basename.
	bashFull := ShellReproduceBash(full, nil, nil, false)
	if !strings.Contains(bashFull, "/c/msys64/home/Staffinc/.local/bin/claude.exe") {
		t.Errorf("bash full should be MSYS path:\n%s", bashFull)
	}
	if strings.Contains(bashFull, `\`) {
		t.Errorf("bash full must not contain backslashes:\n%s", bashFull)
	}
	bashShort := ShellReproduceBash(full, nil, nil, true)
	if strings.TrimSpace(bashShort) != "claude.exe" {
		t.Errorf("bash short should be basename: %q", bashShort)
	}

	// pwsh/cmd keep the full path as-is (both accept backslashes).
	if got := ShellReproducePwsh(full, nil, nil, false); !strings.Contains(got, full) {
		t.Errorf("pwsh full should keep Windows path:\n%s", got)
	}
	if got := ShellReproduceCmd(full, nil, nil, true); strings.TrimSpace(got) != `"claude.exe"` {
		t.Errorf("cmd short should be quoted basename: %q", got)
	}
}

func TestBuildReproVariants(t *testing.T) {
	m := BuildReproVariants("claude", `C:\bin\claude.exe`, []string{"-p", "--add-dir", "x"}, []string{"K=v"})
	// 3 shells × 2 modes × 2 paths = 12 keys.
	if len(m) != 12 {
		t.Fatalf("want 12 variants, got %d: %v", len(m), keysOf(m))
	}
	// A known key exists and headless keeps -p; interactive drops it.
	if !strings.Contains(m["bash-h-full"], "-p") {
		t.Errorf("headless should keep -p:\n%s", m["bash-h-full"])
	}
	if strings.Contains(m["bash-i-full"], "-p") {
		t.Errorf("interactive should drop -p:\n%s", m["bash-i-full"])
	}
	// short path key uses basename.
	if !strings.Contains(m["cmd-h-short"], `"claude.exe"`) {
		t.Errorf("cmd short should use basename:\n%s", m["cmd-h-short"])
	}
	// key naming matches ReproKey.
	if ReproKey("powershell", true, true) != "powershell-i-short" {
		t.Errorf("ReproKey mismatch: %q", ReproKey("powershell", true, true))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInteractiveArgv(t *testing.T) {
	// claude: drop -p, --verbose, --include-partial-messages, and the
	// --input-format/--output-format value flags; keep everything else.
	claude := []string{
		"-p", "--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--mcp-config", `{"a":"b"}`,
		"--add-dir", "C:/x",
		"--resume", "abc",
	}
	got := InteractiveArgv("claude", claude)
	want := []string{"--mcp-config", `{"a":"b"}`, "--add-dir", "C:/x", "--resume", "abc"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("claude: got %v want %v", got, want)
	}

	// codex: drop the `exec` subcommand + all exec-only flags
	// (--json, --skip-git-repo-check, --sandbox<v>, --ask-for-approval<v>) and
	// the `resume <id>` subcommand; keep `-c` overrides + the trailing message.
	codex := []string{
		"exec", "--json", "--skip-git-repo-check",
		"--sandbox", "danger-full-access",
		"--ask-for-approval", "never",
		"-c", "x=1", "resume", "abc123", "coba wick list",
	}
	got = InteractiveArgv("codex", codex)
	want = []string{"-c", "x=1", "coba wick list"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("codex: got %v want %v", got, want)
	}

	// gemini: drop -p only.
	got = InteractiveArgv("gemini", []string{"-p", "--yolo", "--resume", "z"})
	want = []string{"--yolo", "--resume", "z"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("gemini: got %v want %v", got, want)
	}

	// --flag=value form of a value flag is also dropped.
	got = InteractiveArgv("claude", []string{"--output-format=stream-json", "--add-dir", "y"})
	want = []string{"--add-dir", "y"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("claude =value: got %v want %v", got, want)
	}

	// unknown provider → unchanged.
	in := []string{"-p", "whatever"}
	if got := InteractiveArgv("mystery", in); strings.Join(got, " ") != strings.Join(in, " ") {
		t.Errorf("unknown provider should pass through: %v", got)
	}
}

func TestShellReproduceCmdPercentEscape(t *testing.T) {
	got := ShellReproduceCmd("app.exe", nil, []string{"P=a%b"}, false)
	if !strings.Contains(got, `set "P=a%%b"`) {
		t.Errorf("cmd should double %%:\n%s", got)
	}
}
