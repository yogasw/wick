//go:build historye2e

package omp

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

// Real-binary A→B→A across two omp profiles through wick's own argv
// (buildArgs + soul + overlay) and resume-by-path (resumeArg). HOME is a
// throwaway dir (HISTORY_E2E_HOME) holding .omp/profiles/{A,B}; OMP_BIN
// the binary; OMP_MODEL_A / OMP_MODEL_B each profile's model.
func TestResumeAcrossProfilesE2E(t *testing.T) {
	home, bin := os.Getenv("HISTORY_E2E_HOME"), os.Getenv("OMP_BIN")
	if home == "" || bin == "" {
		t.Skip("HISTORY_E2E_HOME / OMP_BIN not set")
	}
	work := filepath.Join(filepath.Dir(home), "work")
	sessDir := filepath.Join(filepath.Dir(home), "sess")
	root := ompRoot(home, "")
	hist, memo := os.Getenv("HIST"), os.Getenv("MEMO")
	idRe := regexp.MustCompile(`"id":"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"`)
	a := provider.Instance{Type: provider.TypeOMP, Name: "e2e-a", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-a"}}
	b := provider.Instance{Type: provider.TypeOMP, Name: "e2e-b", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-b"}}
	models := map[string]string{"e2e-a": os.Getenv("OMP_MODEL_A"), "e2e-b": os.Getenv("OMP_MODEL_B")}

	turn := func(name string, ins provider.Instance, resumeID, prompt string) (string, string, string) {
		t.Helper()
		opt := provider.SpawnOptions{Workspace: work, SessionDir: sessDir, SessionID: "e2e", ResumeID: resumeID,
			Instance: &ins, ModelID: models[ins.Name], Preset: "You are a wick test agent."}
		resume := resumeArg(root, provider.OMPProfile(ins), resumeID, sessionOMPDir(opt))
		args := buildArgs(ins, withResume(opt, resume), writeSoul(opt), writeOverlay(opt), nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := safeexec.CommandContext(ctx, bin, args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "HOME="+home, "PWD="+work, "CLAUDE_CONFIG_DIR=", "PI_CONFIG_FILES=")
		cmd.Stdin = strings.NewReader(prompt)
		out, err := cmd.CombinedOutput()
		_ = os.WriteFile(filepath.Join(filepath.Dir(home), "hop-"+name+".jsonl"), out, 0o644)
		t.Logf("%s: resume arg %q err=%v", name, resume, err)
		id := ""
		if m := idRe.FindSubmatch(out); m != nil {
			id = string(m[1])
		}
		return id, resume, string(out)
	}
	files := func() []string {
		m, _ := filepath.Glob(filepath.Join(root, "profiles", "*", "agent", "sessions", "*", "*.jsonl"))
		return m
	}

	id, _, _ := turn("1-A-seed", a, "", "Remember the code word "+hist+" (only in this chat, do not write it to any file). Also create a file named AGENTS.md in the current directory containing exactly the line: Project memory marker: "+memo+". Reply only OK.")
	if id == "" {
		t.Fatal("no session id from A")
	}
	t.Logf("session %s files=%v", id, files())
	id2, r2, out := turn("2-B-ask", b, id, "What code word did I ask you to remember in this chat, what project memory marker is in AGENTS.md, and which wick skill names are listed in your instructions (up to 5)? Answer briefly.")
	t.Logf("B: same id=%v by path=%v hist=%v memo=%v files=%d", id2 == id, strings.HasSuffix(r2, ".jsonl"), strings.Contains(out, hist), strings.Contains(out, memo), len(files()))
	id3, r3, out := turn("3-A-ask", a, id, "What code word did I ask you to remember, and what did I ask you on the previous turn? Answer briefly.")
	t.Logf("A again: same id=%v resume=%q hist=%v files=%d", id3 == id, r3, strings.Contains(out, hist), len(files()))
	if id2 != id || id3 != id || !strings.Contains(out, hist) || len(files()) != 1 {
		t.Fail()
	}
}
