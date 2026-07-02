package view

import "strings"

// Shell-specific renderers for the Spawn Log "Reproduce" block. Each turns a
// binary + argv + injected env into a single copy-pasteable command for one
// shell dialect. The env is emitted as a prefix (exports) so the reproduced
// command runs with the same environment wick injected. These are called both
// by the templ page (masked env) and the reveal endpoint (unmasked env).

// splitEnvKV splits a "KEY=VALUE" entry. ok is false when there is no '='.
func splitEnvKV(e string) (k, v string, ok bool) {
	return strings.Cut(e, "=")
}

// binaryBasename returns the last path segment of a binary path (handles both
// / and \ separators), e.g. `C:\...\claude.exe` → `claude.exe`. Used for the
// "short" path mode so the command relies on PATH lookup.
func binaryBasename(bin string) string {
	i := strings.LastIndexAny(bin, `/\`)
	if i < 0 {
		return bin
	}
	return bin[i+1:]
}

// msysPath rewrites a Windows path to the MSYS/git-bash form so a bash
// reproduce line actually resolves: `C:\msys64\...\claude.exe` →
// `/c/msys64/.../claude.exe`. A drive letter `X:` becomes `/x`, and every
// backslash becomes a forward slash. Non-Windows paths pass through unchanged
// (no drive prefix, no backslashes → returned as-is).
func msysPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' && isDriveLetter(p[0]) {
		drive := strings.ToLower(string(p[0]))
		rest := strings.TrimPrefix(p[2:], "/")
		return "/" + drive + "/" + rest
	}
	return p
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// StripResumeArgv drops the resume-session tokens so the command starts a fresh
// session instead of continuing the logged one. Per provider: claude uses
// `--resume <id>` (value flag), codex uses `resume <id>` (subcommand + id).
// Other tokens are preserved.
func StripResumeArgv(providerType string, argv []string) []string {
	dropValueFlag := map[string]bool{}
	dropSubcmdValue := map[string]bool{}
	switch providerType {
	case "claude", "gemini":
		dropValueFlag["--resume"] = true
	case "codex":
		dropSubcmdValue["resume"] = true
	}
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if dropValueFlag[a] || dropSubcmdValue[a] {
			i++ // skip the following id
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok && dropValueFlag[k] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// HasResumeArgv reports whether argv carries a resume-session token for the
// provider — i.e. whether the Keep/Fresh toggle would make any difference. The
// first spawn of a session has no resume id, so the toggle should be hidden.
func HasResumeArgv(providerType string, argv []string) bool {
	return len(StripResumeArgv(providerType, argv)) != len(argv)
}

// ReproKey names a reproduce variant by its four axes. Used as the map key in
// BuildReproVariants and mirrored by the front-end / reveal keys.
//   shell: "bash" | "powershell" | "cmd"
//   interactive: headless "h" vs interactive "i"
//   short: full path "full" vs basename "short"
//   resume: keep resume "res" vs fresh session "new"
func ReproKey(shell string, interactive, short, resume bool) string {
	mode := "h"
	if interactive {
		mode = "i"
	}
	path := "full"
	if short {
		path = "short"
	}
	res := "res"
	if !resume {
		res = "new"
	}
	return shell + "-" + mode + "-" + path + "-" + res
}

// BuildReproVariants renders all 24 reproduce commands (3 shells × headless/
// interactive × full/short path × keep/strip resume) for the given binary,
// argv, and env. The same function serves the masked page render and the
// unmasked reveal endpoint — callers pass masked vs unmasked env so the keys
// line up exactly.
func BuildReproVariants(providerType, binary string, argv, env []string) map[string]string {
	out := make(map[string]string, 24)
	for _, resume := range []bool{true, false} {
		base := argv
		if !resume {
			base = StripResumeArgv(providerType, argv)
		}
		iArgv := InteractiveArgv(providerType, base)
		for _, m := range []struct {
			interactive bool
			av          []string
		}{{false, base}, {true, iArgv}} {
			for _, short := range []bool{false, true} {
				out[ReproKey("bash", m.interactive, short, resume)] = ShellReproduceBash(binary, m.av, env, short)
				out[ReproKey("powershell", m.interactive, short, resume)] = ShellReproducePwsh(binary, m.av, env, short)
				out[ReproKey("cmd", m.interactive, short, resume)] = ShellReproduceCmd(binary, m.av, env, short)
			}
		}
	}
	return out
}

// InteractiveArgv strips the headless/programmatic flags wick adds so the
// command runs in the CLI's normal interactive chat mode instead of emitting
// a JSON stream. Everything else (--mcp-config, --add-dir, --resume, …) is
// kept so the session context is identical. providerType is the spawn log's
// ProviderType ("claude" | "codex" | "gemini").
//
// Per provider, the flags dropped:
//   - claude: -p, --verbose, --include-partial-messages, and the value flags
//     --input-format / --output-format (each drops its following value token).
//   - codex:  the `exec` subcommand and --json.
//   - gemini: -p.
func InteractiveArgv(providerType string, argv []string) []string {
	// dropFlag: bare flags removed outright.
	// dropValueFlag: flags that also consume the next token (the value).
	var dropFlag, dropValueFlag, dropSubcmd map[string]bool
	// dropSubcmdValue: subcommand tokens that also consume the following token
	// (a positional, e.g. codex's `resume <id>`).
	var dropSubcmdValue map[string]bool
	switch providerType {
	case "claude":
		dropFlag = map[string]bool{"-p": true, "--print": true, "--verbose": true, "--include-partial-messages": true}
		dropValueFlag = map[string]bool{"--input-format": true, "--output-format": true}
	case "codex":
		// `codex exec` is the headless entry point; interactive is plain `codex`.
		// Drop the exec subcommand and all exec-only flags — several
		// (--skip-git-repo-check, --sandbox, --ask-for-approval) are unknown to
		// the root command and would error. Keep `-c` overrides + the message.
		dropFlag = map[string]bool{"--json": true, "--skip-git-repo-check": true}
		dropValueFlag = map[string]bool{"--sandbox": true, "--ask-for-approval": true}
		dropSubcmd = map[string]bool{"exec": true}
		dropSubcmdValue = map[string]bool{"resume": true}
	case "gemini":
		dropFlag = map[string]bool{"-p": true, "--prompt": true}
	default:
		return argv
	}

	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if dropSubcmd[a] {
			continue
		}
		if dropSubcmdValue[a] {
			i++ // also skip the positional that follows (e.g. the resume id)
			continue
		}
		if dropValueFlag[a] {
			i++ // also skip the value token
			continue
		}
		// `--flag=value` form of a value flag.
		if k, _, ok := strings.Cut(a, "="); ok && dropValueFlag[k] {
			continue
		}
		if dropFlag[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// ShellReproduceBash renders a POSIX/bash command: inline VAR='v' assignments
// on continuation lines, then the quoted command. short=true uses the binary
// basename (rely on PATH); otherwise the full path is rewritten to MSYS form
// (C:\… → /c/…) so it resolves in git-bash/msys2.
func ShellReproduceBash(binary string, argv, env []string, short bool) string {
	var b strings.Builder
	b.WriteString("# run in bash / git-bash / msys2\n")
	for _, e := range env {
		k, v, ok := splitEnvKV(e)
		if !ok {
			continue
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(shellQuote(v))
		b.WriteString(" \\\n")
	}
	b.WriteString(shellCommand(bashBinary(binary, short), argv))
	return b.String()
}

// bashBinary resolves the binary token for a bash line: basename when short,
// else the MSYS-rewritten full path.
func bashBinary(binary string, short bool) string {
	if short {
		return binaryBasename(binary)
	}
	return msysPath(binary)
}

// winBinary resolves the binary token for a Windows shell (pwsh/cmd): basename
// when short, else the full path unchanged (both shells accept backslashes).
func winBinary(binary string, short bool) string {
	if short {
		return binaryBasename(binary)
	}
	return binary
}

// ShellReproducePwsh renders a PowerShell command: one $env:KEY='v' statement
// per line, then the command with PowerShell single-quote quoting. The binary
// is invoked with the call operator `&` — a quoted string on its own is just a
// literal in PowerShell (it echoes, doesn't execute); `& 'path'` runs it.
func ShellReproducePwsh(binary string, argv, env []string, short bool) string {
	var b strings.Builder
	b.WriteString("# run in PowerShell\n")
	for _, e := range env {
		k, v, ok := splitEnvKV(e)
		if !ok {
			continue
		}
		b.WriteString("$env:")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(pwshQuote(v))
		b.WriteString("\n")
	}
	b.WriteString("& ")
	b.WriteString(pwshQuote(winBinary(binary, short)))
	for _, a := range argv {
		b.WriteString(" ")
		b.WriteString(pwshQuote(a))
	}
	return b.String()
}

// ShellReproduceCmd renders a cmd.exe command: one `set "KEY=v"` per line,
// then the command with double-quote quoting. NOTE: cmd.exe reproduction is
// best-effort — a JSON arg like --mcp-config contains double-quotes which must
// be doubled ("") inside a quoted arg, and cmd's quoting/escaping is finicky.
// PowerShell or bash reproduce such args more reliably.
func ShellReproduceCmd(binary string, argv, env []string, short bool) string {
	var b strings.Builder
	b.WriteString("REM run in cmd.exe\n")
	for _, e := range env {
		k, v, ok := splitEnvKV(e)
		if !ok {
			continue
		}
		// `set "KEY=VALUE"` quotes the whole assignment so spaces survive;
		// a literal % must be doubled to avoid variable expansion.
		b.WriteString(`set "`)
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(cmdEscapePercent(v))
		b.WriteString("\"\n")
	}
	b.WriteString(cmdQuote(winBinary(binary, short)))
	for _, a := range argv {
		b.WriteString(" ")
		b.WriteString(cmdQuote(a))
	}
	return b.String()
}

// shellQuote renders a value safe for POSIX/bash. Bare when it contains only
// safe characters, otherwise single-quoted with embedded quotes escaped.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellCommand renders binary + argv as a single bash line.
func shellCommand(binary string, argv []string) string {
	parts := make([]string, 0, len(argv)+1)
	parts = append(parts, shellQuote(binary))
	for _, a := range argv {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// pwshQuote quotes a token for a PowerShell call to a NATIVE exe.
//
// A PowerShell single-quoted string keeps the value literal *within
// PowerShell*, but when the arg contains double quotes (e.g. a JSON
// --mcp-config) Windows PowerShell 5.1 strips them while rebuilding the native
// command line, so the child sees an unquoted blob. For those args, wrap in
// double quotes and escape inner quotes as \" — which survives into the child's
// argv. Args with no double quote stay single-quoted (handles spaces, $, etc.).
func pwshQuote(s string) string {
	if !strings.Contains(s, `"`) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// cmdQuote wraps a token in double quotes for a Windows command line, escaping
// inner double quotes as \" — the convention the C runtime / Node argv parser
// (which claude.exe / codex.cmd use) expects. cmd.exe's own `""` doubling does
// NOT survive into a non-MSVCRT child's argv, which mangled JSON args like
// --mcp-config into an unquoted blob. Backslashes immediately before a closing
// quote must also be doubled so they don't escape it.
func cmdQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			backslashes++
		case '"':
			// Escape the run of backslashes preceding this quote, then the quote.
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
			continue
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
		}
		if c != '\\' {
			b.WriteByte(c)
		}
	}
	// Double any trailing backslashes so they don't escape the closing quote.
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

// cmdEscapePercent doubles % so `set "K=V"` doesn't expand %VAR%.
func cmdEscapePercent(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

// isShellSafe reports whether s needs no quoting in bash.
func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == ',' || r == '=' || r == '@' || r == '+' || r == '%':
		default:
			return false
		}
	}
	return true
}
