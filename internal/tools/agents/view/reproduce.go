package view

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

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

// dropTokens returns argv with the given tokens removed. `flags` and `subcmds`
// are dropped outright; `valueFlags` and `subcmdValues` also drop the following
// token (their value/id). `--flag=value` forms of a valueFlag are dropped too.
func dropTokens(argv, flags, valueFlags, subcmds, subcmdValues []string) []string {
	set := func(ss []string) map[string]bool {
		m := make(map[string]bool, len(ss))
		for _, s := range ss {
			m[s] = true
		}
		return m
	}
	flagSet, valueSet, subcmdSet, subcmdValSet := set(flags), set(valueFlags), set(subcmds), set(subcmdValues)
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if valueSet[a] || subcmdValSet[a] {
			i++ // also skip the following value / id token
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok && valueSet[k] {
			continue
		}
		if flagSet[a] || subcmdSet[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// StripResumeArgv drops the resume-session tokens (per provider.ReproSpec) so
// the command starts a fresh session instead of continuing the logged one.
func StripResumeArgv(providerType string, argv []string) []string {
	s := provider.ReproSpecFor(provider.Type(providerType))
	return dropTokens(argv, nil, s.ResumeValueFlags, nil, s.ResumeSubcmds)
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
//
// prompt is the first user message the command should carry. Empty renders the
// bare command (what the spawn log used to show), which for a stdin provider
// starts a CLI with nothing to say — claude then reports "No deferred tool
// marker found in the resumed session". Non-empty is delivered per the
// provider's ReproSpec: piped into stdin as a stream-json line for the
// long-lived-stdin CLIs, appended as a positional arg in interactive mode.
func BuildReproVariants(providerType, binary string, argv, env []string, prompt string) map[string]string {
	spec := provider.ReproSpecFor(provider.Type(providerType))
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
			delivery    provider.PromptDelivery
		}{
			{false, base, spec.HeadlessPrompt},
			{true, iArgv, spec.InteractivePrompt},
		} {
			av, stdin := applyPrompt(m.av, prompt, m.delivery)
			for _, short := range []bool{false, true} {
				out[ReproKey("bash", m.interactive, short, resume)] = ShellReproduceBash(binary, av, env, short, stdin)
				out[ReproKey("powershell", m.interactive, short, resume)] = ShellReproducePwsh(binary, av, env, short, stdin)
				out[ReproKey("cmd", m.interactive, short, resume)] = ShellReproduceCmd(binary, av, env, short, stdin)
			}
		}
	}
	return out
}

// applyPrompt folds the prompt into one variant: returns the argv to render
// (prompt appended for positional delivery) and the single stdin line to feed
// (non-empty only for stream-json delivery). An empty prompt, or a provider
// whose argv already carries the message (codex), changes nothing.
func applyPrompt(argv []string, prompt string, d provider.PromptDelivery) (av []string, stdin string) {
	if prompt == "" {
		return argv, ""
	}
	switch d {
	case provider.PromptPositional:
		av = make([]string, 0, len(argv)+1)
		av = append(av, argv...)
		return append(av, prompt), ""
	case provider.PromptStdinStreamJSON:
		return argv, StreamJSONUserLine(prompt)
	}
	return argv, ""
}

// StreamJSONUserLine renders one stream-json user message — the exact line
// provider.Agent.Send writes to the subprocess stdin. Kept byte-identical to
// that envelope: reproducing a spawn means feeding the CLI what wick feeds it,
// not an approximation. Always a single line (json escapes any newline), which
// is what lets the bash/PowerShell heredocs below use a fixed delimiter.
func StreamJSONUserLine(text string) string {
	b, err := json.Marshal(text)
	if err != nil { // impossible for a string; fall back to an empty prompt
		b = []byte(`""`)
	}
	return `{"type":"user","message":{"role":"user","content":` + string(b) + `}}`
}

// PromptSupported reports whether the Prompt control is meaningful for this
// provider — false when the spawn's argv already carries the message, so the
// UI can hide a toggle that would only duplicate it.
func PromptSupported(providerType string) bool {
	s := provider.ReproSpecFor(provider.Type(providerType))
	return s.HeadlessPrompt != provider.PromptInArgv || s.InteractivePrompt != provider.PromptInArgv
}

// InteractiveArgv strips the headless/programmatic tokens wick adds (per
// provider.ReproSpec) so the command runs in the CLI's normal interactive chat
// mode instead of emitting a JSON stream. Everything else (--mcp-config,
// --add-dir, --resume, …) is kept so the session context is identical.
func InteractiveArgv(providerType string, argv []string) []string {
	s := provider.ReproSpecFor(provider.Type(providerType))
	return dropTokens(argv, s.HeadlessFlags, s.HeadlessValueFlags, s.HeadlessSubcmds, nil)
}

// ShellReproduceBash renders a POSIX/bash command: inline VAR='v' assignments
// on continuation lines, then the quoted command. short=true uses the binary
// basename (rely on PATH); otherwise the full path is rewritten to MSYS form
// (C:\… → /c/…) so it resolves in git-bash/msys2.
func ShellReproduceBash(binary string, argv, env []string, short bool, stdin string) string {
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
	if stdin != "" {
		// Quoted heredoc: the body is passed through byte for byte (no
		// expansion, no escaping to get wrong), and the EOF closes stdin
		// so the CLI finishes the turn and exits instead of hanging on
		// an open pipe.
		b.WriteString(" <<'" + promptHeredoc + "'\n")
		b.WriteString(stdin)
		b.WriteString("\n" + promptHeredoc)
	}
	return b.String()
}

// promptHeredoc is the delimiter for the bash heredoc / PowerShell here-string
// that carries the stdin prompt. Safe as a fixed word: the payload is always a
// single json line, so it can never contain the delimiter on a line of its own.
const promptHeredoc = "WICK_PROMPT"

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
func ShellReproducePwsh(binary string, argv, env []string, short bool, stdin string) string {
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
	if stdin != "" {
		// Single-quoted here-string: literal, and both delimiters must sit
		// at the start of their own line. Piped into the exe so it lands on
		// stdin the way wick's pipe does.
		b.WriteString("@'\n")
		b.WriteString(stdin)
		b.WriteString("\n'@ | ")
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
func ShellReproduceCmd(binary string, argv, env []string, short bool, stdin string) string {
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
	if stdin != "" {
		// No heredoc in cmd.exe — echo the line into the pipe. Best-effort
		// like the rest of the cmd variant: shell metacharacters outside
		// the json's quoted spans are caret-escaped.
		b.WriteString("echo ")
		b.WriteString(cmdEchoEscape(stdin))
		b.WriteString("| ")
	}
	b.WriteString(cmdQuote(winBinary(binary, short)))
	for _, a := range argv {
		b.WriteString(" ")
		b.WriteString(cmdQuote(a))
	}
	return b.String()
}

// cmdEchoEscape prepares a line for `echo <line>|` in cmd.exe. Inside a
// double-quoted span cmd treats metacharacters literally, so escaping there
// would echo the caret itself; outside one they must be caret-escaped or the
// shell eats them. % is doubled everywhere so %VAR% is not expanded.
func cmdEchoEscape(s string) string {
	var b strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == '%':
			b.WriteString("%")
		case !inQuotes && strings.ContainsRune(`^&|<>()`, r):
			b.WriteByte('^')
		}
		b.WriteRune(r)
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
