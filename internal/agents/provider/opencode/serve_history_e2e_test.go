//go:build historye2e

package opencode

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

// Server mode A→B→A through the real Spawner.Spawn (carryHistory, then
// spawnServe → server manager → ensureSession) with the real binary.
// B's server is already running (a warm-up turn) when the session is
// imported into B's DB, and A's is running when it is copied back.
func TestServeHistoryCarryE2E(t *testing.T) {
	root, bin, model := os.Getenv("HISTORY_E2E_DIR"), os.Getenv("OPENCODE_BIN"), os.Getenv("OPENCODE_E2E_MODEL")
	if root == "" || bin == "" || model == "" {
		t.Skip("HISTORY_E2E_DIR / OPENCODE_BIN / OPENCODE_E2E_MODEL not set")
	}
	t.Cleanup(ShutdownServers)
	work, sess := filepath.Join(root, "work"), filepath.Join(root, "sess")
	hist, memo := os.Getenv("HIST"), os.Getenv("MEMO")
	mk := func(name string) provider.Instance {
		return provider.Instance{Type: provider.TypeOpencode, Name: "e2e-" + name,
			OpencodeConfig: &provider.OpencodeConfig{DataDir: filepath.Join(root, name), AllowHosted: true}}
	}
	a, b := mk("A"), mk("B")
	// B may run another model than the one A's turns recorded in the
	// session: every turn must use its own instance's model.
	models := map[string]string{a.Name: model, b.Name: model}
	if mb := os.Getenv("OPENCODE_E2E_MODEL_B"); mb != "" {
		models[b.Name] = mb
	}
	sidRe := regexp.MustCompile(`"sessionID":"(ses_[A-Za-z0-9]+)"`)
	textRe := regexp.MustCompile(`"text":"((?:[^"\\]|\\.)*)"`)

	turn := func(name string, ins provider.Instance, resume, prompt string) (string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{Workspace: work, SessionDir: sess, SessionID: "e2e-serve",
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
		_ = os.WriteFile(filepath.Join(root, "hop-"+name+".jsonl"), []byte(out.String()), 0o644)
		sid, texts := "", []string{}
		if m := sidRe.FindStringSubmatch(out.String()); m != nil {
			sid = m[1]
		}
		for _, m := range textRe.FindAllStringSubmatch(out.String(), -1) {
			texts = append(texts, m[1])
		}
		t.Logf("%s: argv=%q sid=%s wait=%v text=%q", name, p.Argv(), sid, werr, texts)
		return sid, strings.Join(texts, " | ")
	}

	_, _ = turn("0-B-warmup", b, "", "Reply only: warm")
	sid, _ := turn("1-A-seed", a, "", "Remember the code word "+hist+" (only in this chat, do not write it to any file). Also create a file named AGENTS.md in the current directory containing exactly the line: Project memory marker: "+memo+". Reply only OK.")
	if sid == "" {
		t.Fatal("no session id from A")
	}
	sid2, out2 := turn("2-B-ask", b, sid, "What code word did I ask you to remember in this chat, what project memory marker is in AGENTS.md, and which wick skill names are listed in your instructions (up to 5)? Answer briefly.")
	okB := sid2 == sid && strings.Contains(out2, hist) && !strings.Contains(out2, "could not find")
	t.Logf("B (server warm before import): same id=%v hist=%v memo=%v", sid2 == sid, strings.Contains(out2, hist), strings.Contains(out2, memo))
	sid3, out3 := turn("3-A-ask", a, sid, "What code word did I ask you to remember, and what did I ask on the previous turn? Answer briefly.")
	okA := sid3 == sid && strings.Contains(out3, hist)
	t.Logf("A again (server warm): same id=%v hist=%v", sid3 == sid, strings.Contains(out3, hist))
	sid4, out4 := turn("4-B-bogus", b, "ses_doesnotexist0000000000", "Reply only: fresh")
	okN := sid4 != "" && sid4 != "ses_doesnotexist0000000000" && strings.Contains(out4, "could not find")
	t.Logf("B bogus id: new sid=%v notice=%v", sid4 != "ses_doesnotexist0000000000", strings.Contains(out4, "could not find"))
	if !okB || !okA || !okN {
		t.Fail()
	}
}
