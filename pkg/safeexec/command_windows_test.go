//go:build windows

package safeexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeBatch writes a throwaway .cmd (or other-ext) file so Command's
// pre-resolution finds a real path to build the *exec.Cmd around.
func makeBatch(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFixBatchQuoting_SpaceInArg is the regression guard for the
// "'C:\Program' is not recognized" failure: an npm .cmd shim launched
// with a space-containing argument. The fix must redirect the launch
// through cmd.exe with the outer-quoted /s /c form.
func TestFixBatchQuoting_SpaceInArg(t *testing.T) {
	bat := makeBatch(t, "tool.cmd")

	cmd := Command(bat, "exec", "reply only the word: OK")

	// Launch must be redirected to cmd.exe, not the .cmd directly.
	if !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		t.Fatalf("cmd.Path = %q, want it redirected to cmd.exe", cmd.Path)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine == "" {
		t.Fatal("SysProcAttr.CmdLine not set for .cmd target")
	}
	line := cmd.SysProcAttr.CmdLine

	// Shape: "<comspec>" /s /c "<inner>" — the outer quote pair around
	// the inner command is what makes cmd.exe strip-and-run correctly.
	if !strings.Contains(line, " /s /c ") {
		t.Errorf("CmdLine missing `/s /c`: %q", line)
	}
	// The quoted batch path (with its own possible spaces) must appear
	// as a quoted token inside the inner command.
	if !strings.Contains(line, `"`+bat+`"`) {
		t.Errorf("CmdLine missing quoted batch path %q:\n%q", bat, line)
	}
	// The space-containing argument must be a single quoted token, not
	// split across whitespace.
	if !strings.Contains(line, `"reply only the word: OK"`) {
		t.Errorf("CmdLine did not quote the space-containing arg as one token:\n%q", line)
	}
	// The whole inner command must be wrapped by an outer quote pair
	// following /c.
	after := line[strings.Index(line, " /s /c ")+len(" /s /c "):]
	if !strings.HasPrefix(after, `"`) || !strings.HasSuffix(after, `"`) {
		t.Errorf("inner command after /c is not wrapped in an outer quote pair:\n%q", after)
	}
}

// TestFixBatchQuoting_EmbeddedQuote verifies embedded double quotes are
// doubled so the CRT argv parser keeps them inside the token.
func TestFixBatchQuoting_EmbeddedQuote(t *testing.T) {
	bat := makeBatch(t, "tool.cmd")

	cmd := Command(bat, `say "hi there"`)

	line := cmd.SysProcAttr.CmdLine
	if !strings.Contains(line, `"say ""hi there"""`) {
		t.Errorf("embedded quotes not doubled:\n%q", line)
	}
}

// TestFixBatchQuoting_ExeUntouched confirms .exe targets keep Go's
// native launch path — no cmd.exe redirect, no CmdLine override.
func TestFixBatchQuoting_ExeUntouched(t *testing.T) {
	exe := makeBatch(t, "tool.exe")

	cmd := Command(exe, "arg with space")

	if !strings.EqualFold(cmd.Path, exe) {
		t.Errorf("exe target redirected: cmd.Path = %q, want %q", cmd.Path, exe)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.CmdLine != "" {
		t.Errorf("exe target got a CmdLine override: %q", cmd.SysProcAttr.CmdLine)
	}
}

// TestFixBatchQuoting_PreservesArgv is a belt-and-braces check that the
// resolved argv the child would see round-trips through cmd.exe. It
// actually launches a batch file that echoes its args and asserts the
// space-containing arg arrives as one token.
func TestFixBatchQuoting_PreservesArgv(t *testing.T) {
	dir := t.TempDir()
	// A shim that prints each %N on its own line so we can count tokens.
	bat := filepath.Join(dir, "echoargs.cmd")
	script := "@echo off\r\n" +
		"echo ARG1=[%~1]\r\n" +
		"echo ARG2=[%~2]\r\n" +
		"echo ARG3=[%~3]\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Command(bat, "reply only the word: OK", "second")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\noutput:\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "ARG1=[reply only the word: OK]") {
		t.Errorf("space-containing arg not delivered as one token:\n%s", got)
	}
	if !strings.Contains(got, "ARG2=[second]") {
		t.Errorf("second arg mis-delivered:\n%s", got)
	}
	if !strings.Contains(got, "ARG3=[]") {
		t.Errorf("phantom third arg — split happened:\n%s", got)
	}
}
