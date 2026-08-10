// git.go runs the git binary. Everything that touches the process — argument
// assembly, environment, timeouts, output limits — lives here, so the safety
// rules are in one auditable place.
//
// The central rule: arguments arriving from the agent (UserArgs) are filtered;
// arguments the plugin adds itself (InjectedArgs) are not. They are separate
// fields and only meet in Argv().
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

// bannedArgs are flags that turn git into an arbitrary-code-execution vector or
// make it block on an editor. Rejected only in agent-supplied arguments.
//
//	-c / --config-env  set arbitrary config: core.pager, alias.x=!cmd → shell
//	--exec-path        redirect git's helper binaries
//	--upload-pack / --receive-pack / --exec  run an arbitrary binary on either
//	                   end of the transport ("--exec" is push's alias for
//	                   --receive-pack)
//	ext::              the ext transport executes a shell command
//	--interactive      opens an editor and hangs until the timeout
//	--output           write to an arbitrary file path
//
// Deliberately NOT banned:
//
//	-u   It is not a short form of --upload-pack in any subcommand. Verified
//	     against git 2.52: in fetch it is --update-head-ok, in push it is
//	     --set-upstream, and ls-remote has no -u at all. Banning it only broke
//	     the idiomatic "push -u origin main" while blocking nothing, and the
//	     ban was self-inconsistent anyway (status -uall passed, status -u did
//	     not). The dangerous long forms above are banned on their own.
//	-i   Overlaps two unrelated meanings: "git add -i" opens an editor, but
//	     "git grep -i" is case-insensitive matching. Banning the short form
//	     broke grep for no gain — the editor-opening long form --interactive is
//	     still refused, and GIT_TERMINAL_PROMPT=0 plus the timeout stop a
//	     stray editor from hanging forever.
var bannedArgs = []string{
	"-c", "--config-env", "--exec-path",
	"--upload-pack", "--receive-pack", "--exec",
	"ext::", "--interactive", "--output",
}

// ValidateUserArgs rejects agent-supplied arguments that could escape the policy
// or hang the process. Matching covers both "--flag value" and "--flag=value".
func ValidateUserArgs(args []string) error {
	for _, a := range args {
		low := strings.ToLower(strings.TrimSpace(a))
		if strings.Contains(low, "ext::") {
			return fmt.Errorf("argument %q is not allowed (matched %q): it can execute arbitrary commands or block on an editor", a, "ext::")
		}
		for _, bad := range bannedArgs {
			if low == bad || strings.HasPrefix(low, bad+"=") {
				return fmt.Errorf("argument %q is not allowed (matched %q): it can execute arbitrary commands or block on an editor", a, bad)
			}
		}
	}
	return nil
}

// Cmd is one git invocation. UserArgs come from the agent and are validated;
// InjectedArgs are added by the plugin and are not. Network marks operations that
// contact a remote so the longer timeout applies.
type Cmd struct {
	RepoPath     string
	InjectedArgs []string
	UserArgs     []string
	Network      bool
}

// Argv assembles the final argument vector. Injected arguments come first because
// git requires its global options before the subcommand.
func (c Cmd) Argv() []string {
	argv := make([]string, 0, len(c.InjectedArgs)+len(c.UserArgs))
	argv = append(argv, c.InjectedArgs...)
	argv = append(argv, c.UserArgs...)
	return argv
}

// SplitRawArgs splits a raw argument string into argv, honouring single and
// double quotes so a commit message with spaces survives as one argument.
func SplitRawArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false

	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true // an empty quoted string is still an argument
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out
}

// globalValueFlags are the git *global* options that take their value as a
// separate following token, so that token must not be mistaken for the
// subcommand. Per `git --help`, only -C and -c work this way; --git-dir,
// --work-tree, --namespace and --config-env are all =-joined as globals.
var globalValueFlags = map[string]bool{"-C": true, "-c": true}

// globalBoolFlags are the git global options that take no value at all.
var globalBoolFlags = map[string]bool{
	"-v": true, "--version": true, "-h": true, "--help": true,
	"-p": true, "--paginate": true, "-P": true, "--no-pager": true,
	"--no-replace-objects": true, "--no-lazy-fetch": true,
	"--no-optional-locks": true, "--no-advice": true, "--bare": true,
	"--html-path": true, "--man-path": true, "--info-path": true,
}

// RawSubcommandOf finds the git subcommand in an argument vector, skipping git's
// own global flags. The policy engine needs it to decide whether raw may run;
// returning "" means "unknown", which the engine treats as denied.
//
// It fails closed. Only the closed set of real git global options is skipped; any
// other leading flag means the vector is not a well-formed git invocation, and
// guessing a subcommand from it would be worse than refusing. In particular a
// subcommand-level flag such as "-n 5" must not cause "5" to be read as the
// subcommand.
func RawSubcommandOf(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return strings.ToLower(a)
		}
		switch {
		case globalValueFlags[a]:
			i++ // its value is the next token, never a subcommand
		case globalBoolFlags[a]:
			// no value to skip
		case strings.Contains(a, "="):
			// An =-joined global such as --git-dir=/x carries its own value.
		default:
			// Not a git global option, so this vector has no subcommand we can
			// trust. Deny rather than guess.
			return ""
		}
	}
	return ""
}

// Environment variable names used to hand the credential to the askpass helper.
// The helper is this same binary re-invoked with --askpass.
const (
	envAskpassUser  = "WICK_GIT_USERNAME"
	envAskpassToken = "WICK_GIT_TOKEN"
)

// envAllowlist are the only variables carried over from the parent process. Git
// needs PATH to find its own helper binaries (git-remote-https, the credential
// helpers), HOME/USERPROFILE to resolve ~, and on Windows SystemRoot for the
// socket and TLS stacks. Everything else is dropped, because a full inherit would
// forward GIT_CONFIG_*, GIT_SSH_COMMAND, GIT_PROXY_COMMAND and GIT_EXTERNAL_DIFF,
// each of which turns git into an arbitrary-command runner.
var envAllowlist = []string{
	"PATH", "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
	"SystemRoot", "SystemDrive", "windir", "COMSPEC", "PATHEXT",
	"ProgramFiles", "ProgramFiles(x86)", "ProgramData", "LOCALAPPDATA", "APPDATA",
	"TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL",
}

// AuthSpec is the resolved HTTPS credential for one execution.
//
// Method is one of:
//
//	askpass            GIT_ASKPASS points at this binary; token travels in env.
//	                   No file, token absent from argv. The default.
//	credential_helper  -c credential.helper reads the token from env. Needs -c,
//	                   so it goes through InjectedArgs.
//	extraheader        -c http.extraheader carries the token in argv, where it is
//	                   visible in the process list. Opt-in only.
type AuthSpec struct {
	Method   string
	Username string
	Token    string
}

// BuildEnv returns the child environment as an explicit allowlist. Inheriting the
// parent environment would forward GIT_CONFIG_*, GIT_SSH_COMMAND and friends,
// each of which can make git execute an arbitrary command.
func BuildEnv(a AuthSpec, selfPath string) []string {
	env := []string{
		// git must never block waiting for a human, and must never fall back to
		// the machine's credential manager.
		"GIT_TERMINAL_PROMPT=0",
	}

	// Carry over only what git genuinely needs to run.
	for _, k := range envAllowlist {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}

	if a.Token == "" {
		return env
	}

	env = append(env, envAskpassUser+"="+a.Username, envAskpassToken+"="+a.Token)
	if a.Method == "" || a.Method == "askpass" {
		env = append(env, "GIT_ASKPASS="+selfPath)
	}
	return env
}

// AuthInjectedArgs returns the plugin's own -c arguments for the credential
// methods that need them. These bypass ValidateUserArgs by construction: they
// are placed in Cmd.InjectedArgs, which is never filtered.
func AuthInjectedArgs(a AuthSpec) []string {
	if a.Token == "" {
		return nil
	}
	switch a.Method {
	case "credential_helper":
		// The helper echoes the token from the environment, so it never appears
		// in argv (and so never in the process list).
		return []string{"-c", `credential.helper=!f(){ echo username=$` + envAskpassUser +
			`; echo password=$` + envAskpassToken + `; }; f`}
	case "extraheader":
		basic := basicAuthValue(a.Username, a.Token)
		return []string{"-c", "http.extraheader=Authorization: Basic " + basic}
	default:
		return nil
	}
}

// basicAuthValue encodes username:token for an HTTP Basic header.
func basicAuthValue(user, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
}

// capBytes truncates output to max bytes. max <= 0 means no limit. Truncation is
// always reported so a caller never mistakes a cut result for a complete one.
func capBytes(b []byte, max int) (string, bool) {
	if max <= 0 || len(b) <= max {
		return string(b), false
	}
	return string(b[:max]), true
}

// ValidateRepoPath checks that a path is a plausible git repository. There is no
// path allowlist by design — the connector manages repos that already exist — so
// this guard exists to catch accidents, not to contain an attacker.
func ValidateRepoPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("repo_path is required")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("repo_path %q is not a valid path: %w", p, err)
	}
	if home, herr := os.UserHomeDir(); herr == nil && abs == filepath.Clean(home) {
		return errors.New("repo_path must not be the home directory itself")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("repo_path %q does not exist", p)
	}
	if !st.IsDir() {
		return fmt.Errorf("repo_path %q is not a directory", p)
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return fmt.Errorf("repo_path %q does not contain a .git directory", p)
	}
	return nil
}

// ResolveGit finds the git binary. A clear failure here beats an obscure one at
// exec time.
//
// Uses safeexec rather than os/exec: Go's own LookPath calls faccessat2(2), which
// Android/Termux seccomp rejects with SIGSYS on kernels before 5.8.
func ResolveGit() (string, error) {
	p, err := safeexec.LookPath("git")
	if err != nil {
		return "", errors.New("git not found in PATH on the machine running wick")
	}
	return p, nil
}

// Result is the typed envelope every operation returns. A non-zero git exit is
// reported here, not as a Go error — the agent needs stderr to react.
type Result struct {
	OK         bool   `json:"ok"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
}

// RunOpts carries everything the runner needs that is not part of the command.
// Masks are values scrubbed from the recorded command and output.
type RunOpts struct {
	Auth      AuthSpec
	SelfPath  string
	Timeout   time.Duration
	MaxOutput int
	Masks     []string
}

// Run executes git with a bounded lifetime, an allowlisted environment, and
// capped output. It returns an error only when the command could not be started
// or was killed; a normal non-zero exit lands in Result.
func Run(ctx context.Context, c Cmd, o RunOpts) (Result, error) {
	// Only agent-supplied arguments are filtered. Validating c.Argv() instead
	// would reject the plugin's own "-c credential.helper=…" injection and break
	// every authenticated operation — the split exists precisely for this.
	if err := ValidateUserArgs(c.UserArgs); err != nil {
		return Result{}, err
	}
	gitPath, err := ResolveGit()
	if err != nil {
		return Result{}, err
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}

	argv := c.Argv()
	shown := mask("git "+strings.Join(argv, " "), o.Masks)

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	// safeexec.Command, not exec.Command: it pre-resolves the binary through a
	// LookPath that avoids the faccessat2(2) syscall Android/Termux seccomp kills.
	// gitPath is already absolute here, so resolution is a no-op — the wrapper is
	// used consistently so no spawn in this package can regress to os/exec.
	//
	// setProcAttr runs after, and only ever adds fields to SysProcAttr, so it
	// composes with any SysProcAttr safeexec may have set.
	cmd := safeexec.Command(gitPath, argv...)
	cmd.Dir = c.RepoPath
	cmd.Env = BuildEnv(o.Auth, o.SelfPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	setProcAttr(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Command: shown}, fmt.Errorf("start git: %w", err)
	}

	// Kill the whole process group on timeout. exec.CommandContext would only
	// signal the parent, orphaning git's transport helpers.
	//
	// The goroutine cannot leak: cmd.Wait always returns once the process is
	// reaped, killGroup guarantees it is, and the channel is buffered so the send
	// completes even after this function has returned.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		killGroup(cmd)
		<-done
		// Cap first, then mask. Masking a stream that is later cut could leave a
		// half-redacted secret; cutting first and masking the survivor cannot.
		errStr, _ := capBytes(stderr.Bytes(), o.MaxOutput)
		return Result{
				Command:    shown,
				DurationMS: time.Since(start).Milliseconds(),
				Stderr:     mask(errStr, o.Masks),
			},
			fmt.Errorf("git timed out after %s and was killed", o.Timeout)
	}

	res := Result{
		Command:    shown,
		DurationMS: time.Since(start).Milliseconds(),
	}
	// Cap before masking, never after: a placeholder is shorter than the secret it
	// replaces, so masking first would let the cap land inside what is left of a
	// partially matched secret and leak its tail.
	outStr, outTrunc := capBytes(stdout.Bytes(), o.MaxOutput)
	errStr, errTrunc := capBytes(stderr.Bytes(), o.MaxOutput)
	res.Stdout = mask(outStr, o.Masks)
	res.Stderr = mask(errStr, o.Masks)
	res.Truncated = outTrunc || errTrunc

	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		res.OK = true
	case errors.As(waitErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("git failed: %w", waitErr)
	}
	return res, nil
}

// mask replaces every secret value with a fixed placeholder. Applied to the
// recorded command and to both output streams, since git echoes URLs back.
//
// The caller supplies the list, and must include every encoding a credential can
// reach argv in — with auth_method=extraheader the token appears as
// base64(user:token), and base64 is encoding, not protection. mask itself is
// encoding-agnostic: it redacts every string it is given.
func mask(s string, values []string) string {
	for _, v := range values {
		// Values shorter than 4 characters are skipped: they collide with ordinary
		// words and would mangle unrelated output. No real credential is that
		// short, so nothing that matters is exempted here.
		if len(v) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, v, "••••••••")
	}
	return s
}
