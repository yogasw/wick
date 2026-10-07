//go:build historye2e

package omp

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// RPC mode A→B→A through the real Spawner.Spawn (resumeArg, the retire
// of the other instance's process, spawnRPC → cliserver → omp --mode
// rpc) with the real binary. HOME is the throwaway HISTORY_E2E_HOME.
func TestRPCResumeAcrossProfilesE2E(t *testing.T) {
	home, bin := os.Getenv("HISTORY_E2E_HOME"), os.Getenv("OMP_BIN")
	if home == "" || bin == "" {
		t.Skip("HISTORY_E2E_HOME / OMP_BIN not set")
	}
	t.Setenv("HOME", home)
	t.Cleanup(ShutdownServers)
	base := filepath.Dir(home)
	work, sess := filepath.Join(base, "work"), filepath.Join(base, "sess")
	root := ompRoot(home, "")
	hist, memo := os.Getenv("HIST"), os.Getenv("MEMO")
	a := provider.Instance{Type: provider.TypeOMP, Name: "e2e-a", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-a"}}
	b := provider.Instance{Type: provider.TypeOMP, Name: "e2e-b", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-b"}}
	models := map[string]string{"e2e-a": os.Getenv("OMP_MODEL_A"), "e2e-b": os.Getenv("OMP_MODEL_B")}
	idRe := regexp.MustCompile(`"id":"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"`)

	turn := func(name string, ins provider.Instance, resume, prompt string) (string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{Workspace: work, SessionDir: sess, SessionID: "e2e-rpc",
			ResumeID: resume, Instance: &ins, ModelID: models[ins.Name], Preset: "You are a wick test agent.", InitialMessage: prompt})
		if err != nil {
			t.Fatalf("%s spawn: %v", name, err)
		}
		var out strings.Builder
		sc := bufio.NewScanner(p.Stdout())
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			out.WriteString(sc.Text() + "\n")
		}
		werr := p.Wait()
		_ = os.WriteFile(filepath.Join(base, "rpc-hop-"+name+".jsonl"), []byte(out.String()), 0o644)
		first, _, _ := strings.Cut(out.String(), "\n")
		sid := ""
		if m := idRe.FindStringSubmatch(first); m != nil {
			sid = m[1]
		}
		t.Logf("%s: argv=%q sid=%s wait=%v", name, p.Argv(), sid, werr)
		return sid, out.String()
	}
	files := func() []string {
		m, _ := filepath.Glob(filepath.Join(root, "profiles", "*", "agent", "sessions", "*", "*.jsonl"))
		return m
	}

	sid, _ := turn("1-A-seed", a, "", "Remember the code word "+hist+" (only in this chat, do not write it to any file). Also create a file named AGENTS.md in the current directory containing exactly the line: Project memory marker: "+memo+". Reply only OK.")
	if sid == "" {
		t.Fatal("no session id from A")
	}
	sid2, out2 := turn("2-B-ask", b, sid, "What code word did I ask you to remember in this chat, what project memory marker is in AGENTS.md, and which wick skill names are listed in your instructions (up to 5)? Answer briefly.")
	t.Logf("B: same id=%v hist=%v memo=%v skills=%v files=%d", sid2 == sid, strings.Contains(out2, hist), strings.Contains(out2, memo), strings.Contains(out2, "wick-agents"), len(files()))
	sid3, out3 := turn("3-A-ask", a, sid, "What code word did I ask you to remember, and what did I ask on the previous turn? Answer briefly.")
	t.Logf("A again: same id=%v hist=%v files=%d", sid3 == sid, strings.Contains(out3, hist), len(files()))
	if sid2 != sid || sid3 != sid || !strings.Contains(out2, hist) || !strings.Contains(out3, hist) || len(files()) != 1 {
		t.Fail()
	}
}

// The transcript's last model is one the target account does not have:
// A runs pinned to OMP_FOREIGN_MODEL (B's account lacks it), B and then A
// again run unpinned. Control first: with the fix inert B fails the way
// the live bug did; with it B runs on its own model, and A on its own.
func TestRPCResumeForeignModelE2E(t *testing.T) {
	home, bin, foreign := os.Getenv("HISTORY_E2E_HOME"), os.Getenv("OMP_BIN"), os.Getenv("OMP_FOREIGN_MODEL")
	if home == "" || bin == "" || foreign == "" {
		t.Skip("HISTORY_E2E_HOME / OMP_BIN / OMP_FOREIGN_MODEL not set")
	}
	t.Setenv("HOME", home)
	t.Cleanup(provider.SetModelStateDirForTest(filepath.Join(filepath.Dir(home), "model-state")))
	t.Cleanup(ShutdownServers)
	base := filepath.Dir(home)
	work, sess := filepath.Join(base, "work"), filepath.Join(base, "sess")
	hist := os.Getenv("HIST")
	a := provider.Instance{Type: provider.TypeOMP, Name: "e2e-a", Binary: bin, OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-a"}}
	b := provider.Instance{Type: provider.TypeOMP, Name: "e2e-b", Binary: bin, OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-b"}}
	idRe := regexp.MustCompile(`"id":"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"`)
	modelRe := regexp.MustCompile(`"model":"([^"]+)"`)

	turn := func(name string, ins provider.Instance, pin, resume, prompt string) (string, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{Workspace: work, SessionDir: sess, SessionID: "e2e-fm",
			ResumeID: resume, Instance: &ins, ModelID: pin, Preset: "You are a wick test agent.", InitialMessage: prompt})
		if err != nil {
			t.Fatalf("%s spawn: %v", name, err)
		}
		var out strings.Builder
		sc := bufio.NewScanner(p.Stdout())
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			out.WriteString(sc.Text() + "\n")
		}
		_ = p.Wait()
		_ = os.WriteFile(filepath.Join(base, "fm-hop-"+name+".jsonl"), []byte(out.String()), 0o644)
		first, _, _ := strings.Cut(out.String(), "\n")
		sid, model := "", ""
		if m := idRe.FindStringSubmatch(first); m != nil {
			sid = m[1]
		}
		if all := modelRe.FindAllStringSubmatch(out.String(), -1); len(all) > 0 {
			model = all[len(all)-1][1]
		}
		t.Logf("%s: argv=%q sid=%s model=%s", name, p.Argv(), sid, model)
		return sid, model, out.String()
	}
	notFound := func(s string) bool {
		return strings.Contains(s, "model_not_found") || strings.Contains(s, "does not exist")
	}

	sid, m1, _ := turn("1-A-seed-pinned", a, foreign, "", "Remember the code word "+hist+" (only in this chat). Reply only OK.")
	if sid == "" {
		t.Fatal("no session id from A")
	}
	// Control: the fix inert (no model known for B) reproduces the bug.
	pe, po := explicitDefaultFn, ownModelFn
	explicitDefaultFn = func(context.Context, provider.Instance) string { return "" }
	ownModelFn = func(context.Context, provider.Instance) string { return "" }
	_, _, outC := turn("2c-B-control-nofix", b, "", sid, "What code word did I ask you to remember? Answer briefly.")
	explicitDefaultFn, ownModelFn = pe, po
	_ = os.Remove(filepath.Join(sess, ".omp", writersFile)) // forget the control's writer record
	t.Logf("control: model_not_found=%v", notFound(outC))

	sid2, m2, out2 := turn("2-B-unpinned", b, "", sid, "What code word did I ask you to remember? Answer briefly.")
	t.Logf("B: same id=%v model=%s (A ran %s) hist=%v notfound=%v", sid2 == sid, m2, m1, strings.Contains(out2, hist), notFound(out2))
	sid3, m3, out3 := turn("3-A-unpinned", a, "", sid, "Say the code word once more, nothing else.")
	t.Logf("A again: same id=%v model=%s hist=%v notfound=%v", sid3 == sid, m3, strings.Contains(out3, hist), notFound(out3))
	if sid2 != sid || sid3 != sid || notFound(out2) || notFound(out3) || !strings.Contains(out2, hist) || !strings.Contains(out3, hist) {
		t.Fail()
	}
}

// Live mode with a chosen Default model on both instances and NO session
// pin anywhere: A's default is OMP_FOREIGN_MODEL (B's account lacks it),
// B's is OMP_MODEL_B. Every hop must send its own instance's default.
// Control: with the rules inert B restores A's model from the transcript
// and fails (the 0.1.398 bug).
func TestRPCLiveDefaultE2E(t *testing.T) {
	home, bin, foreign := os.Getenv("HISTORY_E2E_HOME"), os.Getenv("OMP_BIN"), os.Getenv("OMP_FOREIGN_MODEL")
	if home == "" || bin == "" || foreign == "" {
		t.Skip("HISTORY_E2E_HOME / OMP_BIN / OMP_FOREIGN_MODEL not set")
	}
	t.Setenv("HOME", home)
	t.Cleanup(provider.SetModelStateDirForTest(filepath.Join(filepath.Dir(home), "model-state")))
	t.Cleanup(ShutdownServers)
	base := filepath.Dir(home)
	work, sess := filepath.Join(base, "work"), filepath.Join(base, "sess")
	hist := os.Getenv("HIST")
	a := provider.Instance{Type: provider.TypeOMP, Name: "e2e-a", Binary: bin, LiveModels: true, LiveModelDefault: foreign,
		OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-a"}}
	b := provider.Instance{Type: provider.TypeOMP, Name: "e2e-b", Binary: bin, LiveModels: true, LiveModelDefault: os.Getenv("OMP_MODEL_B"),
		OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-b"}}
	idRe := regexp.MustCompile(`"id":"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"`)
	modelRe := regexp.MustCompile(`"model":"([^"]+)"`)

	turn := func(name string, ins provider.Instance, pin, resume, prompt string) (string, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{Workspace: work, SessionDir: sess, SessionID: "e2e-live",
			ResumeID: resume, Instance: &ins, ModelID: pin, Preset: "You are a wick test agent.", InitialMessage: prompt})
		if err != nil {
			t.Fatalf("%s spawn: %v", name, err)
		}
		var out strings.Builder
		sc := bufio.NewScanner(p.Stdout())
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			out.WriteString(sc.Text() + "\n")
		}
		_ = p.Wait()
		_ = os.WriteFile(filepath.Join(base, "live-hop-"+name+".jsonl"), []byte(out.String()), 0o644)
		first, _, _ := strings.Cut(out.String(), "\n")
		sid, model := "", ""
		if m := idRe.FindStringSubmatch(first); m != nil {
			sid = m[1]
		}
		if all := modelRe.FindAllStringSubmatch(out.String(), -1); len(all) > 0 {
			model = all[len(all)-1][1]
		}
		t.Logf("%s: argv=%q sid=%s model=%s", name, p.Argv(), sid, model)
		return sid, model, out.String()
	}
	notFound := func(s string) bool {
		return strings.Contains(s, "model_not_found") || strings.Contains(s, "does not exist")
	}

	sid, m1, _ := turn("1-A-seed", a, "", "", "Remember the code word "+hist+" (only in this chat). Reply only OK.")
	if sid == "" {
		t.Fatal("no session id from A")
	}
	// Control: the fix inert (no model known for B) reproduces the bug.
	pe, po := explicitDefaultFn, ownModelFn
	explicitDefaultFn = func(context.Context, provider.Instance) string { return "" }
	ownModelFn = func(context.Context, provider.Instance) string { return "" }
	_, _, outC := turn("2c-B-control-nofix", b, "", sid, "What code word did I ask you to remember? Answer briefly.")
	explicitDefaultFn, ownModelFn = pe, po
	_ = os.Remove(filepath.Join(sess, ".omp", writersFile)) // forget the control's writer record
	t.Logf("control: model_not_found=%v", notFound(outC))

	sid2, m2, out2 := turn("2-B", b, "", sid, "What code word did I ask you to remember? Answer briefly.")
	t.Logf("B: same id=%v model=%s (A ran %s) hist=%v notfound=%v", sid2 == sid, m2, m1, strings.Contains(out2, hist), notFound(out2))
	sid3, m3, out3 := turn("3-A", a, "", sid, "Say the code word once more, nothing else.")
	t.Logf("A again: same id=%v model=%s hist=%v notfound=%v", sid3 == sid, m3, strings.Contains(out3, hist), notFound(out3))
	if m1 != modelName(foreign) || m2 != modelName(os.Getenv("OMP_MODEL_B")) || m3 != modelName(foreign) ||
		sid2 != sid || sid3 != sid || notFound(out2) || notFound(out3) || !strings.Contains(out2, hist) || !strings.Contains(out3, hist) {
		t.Fail()
	}
}

// modelName is the model part of "provider/model" (the stream names it bare).
func modelName(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return id
}
