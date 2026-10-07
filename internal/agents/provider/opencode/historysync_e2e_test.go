//go:build historye2e

package opencode

import (
	"context"
	"github.com/yogasw/wick/pkg/safeexec"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// Real-binary A→B→A run through wick's own argv (buildArgs), store env
// (storeEnv) and history copy (carryHistory). HISTORY_E2E_DIR holds the
// throwaway data folders; OPENCODE_BIN the binary; OPENCODE_E2E_MODEL a
// free model. Transcript of every hop lands in the dir as hop-*.jsonl.
func TestHistoryCarryE2E(t *testing.T) {
	root, bin, model := os.Getenv("HISTORY_E2E_DIR"), os.Getenv("OPENCODE_BIN"), os.Getenv("OPENCODE_E2E_MODEL")
	if root == "" || bin == "" || model == "" {
		t.Skip("HISTORY_E2E_DIR / OPENCODE_BIN / OPENCODE_E2E_MODEL not set")
	}
	a, b := filepath.Join(root, "A"), filepath.Join(root, "B")
	work, state := filepath.Join(root, "work"), filepath.Join(root, "sess", ".opencode-wick")
	hist, memo := os.Getenv("HIST"), os.Getenv("MEMO")
	soul := writeSoul(provider.SpawnOptions{SessionDir: filepath.Dir(state), Preset: "You are a wick test agent."})
	if soul == "" {
		t.Fatal("no soul written")
	}
	sidRe := regexp.MustCompile(`"sessionID":"(ses_[A-Za-z0-9]+)"`)

	turn := func(name, dir, resume, prompt string) (string, string) {
		t.Helper()
		carryHistory(context.Background(), bin, work, state, resume, dir)
		args := buildArgs(provider.SpawnOptions{ResumeID: resume}, nil, model, false)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := safeexec.CommandContext(ctx, bin, append(args, prompt)...)
		cmd.Dir = work
		// PWD too: opencode takes its project dir from it, not the cwd.
		cmd.Env = append(os.Environ(), "PWD="+work)
		cmd.Env = append(cmd.Env, storeEnv(dir)...)
		// The soul exactly as a wick spawn builds it (preset + shipped
		// skill catalog) through the inline config.
		cmd.Env = append(cmd.Env, configEnvVar+"="+configContent(false, soul, nil, nil))
		cmd.Env = append(cmd.Env, "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1")
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		_ = os.WriteFile(filepath.Join(root, "hop-"+name+".jsonl"), out, 0o644)
		if err != nil {
			t.Logf("%s: %v", name, err)
		}
		t.Logf("%s done err=%v", name, err)
		sid := ""
		if m := sidRe.FindSubmatch(out); m != nil {
			sid = string(m[1])
		}
		return sid, string(out)
	}
	has := func(out, s string) bool { return strings.Contains(out, s) }

	sid, _ := turn("1-A-seed", a, "", "Remember the code word "+hist+" (just keep it in this chat, do not write it anywhere). Also create a file named AGENTS.md in the current directory containing exactly the line: Project memory marker: "+memo+". Reply only OK.")
	if sid == "" {
		t.Fatal("no session id from A")
	}
	t.Logf("session %s", sid)
	sid2, out := turn("2-B-ask", b, sid, "What code word did I ask you to remember in this chat, and what project memory marker is in AGENTS.md, and which wick skill names are listed in your instructions (list up to 5)? Answer briefly.")
	t.Logf("B same id=%v hist=%v memo=%v", sid2 == sid, has(out, hist), has(out, memo))
	sid3, out := turn("3-A-ask", a, sid, "Say the code word from earlier in this chat once more, nothing else.")
	t.Logf("A again same id=%v hist=%v", sid3 == sid, has(out, hist))
	if sid2 != sid || sid3 != sid || !has(out, hist) {
		t.Fail()
	}
}
