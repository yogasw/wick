package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	_ "github.com/yogasw/wick/internal/agents/provider/logintty" // registers omp/opencode ModelSets
	"github.com/yogasw/wick/internal/agents/state"
)

// TestE2EAccountFolderRealBinary: an instance with two account folders
// (main + a2, no login — the free hosted model needs none). A picker pin
// naming account a2 must run opencode in a2's data folder, and the same
// pin through the registry must reach the CLI as the bare model id.
// Opt-in, same harness as TestE2EQueueAndKillRealBinary:
//
//	systemd-run --user --scope -p MemoryMax=900M env \
//	  WICK_E2E_PROVIDER_BIN=/path/opencode WICK_E2E_DIR=/scratch \
//	  go test -run TestE2EAccountFolderRealBinary -v ./internal/agents/provider/opencode/
func TestE2EAccountFolderRealBinary(t *testing.T) {
	bin, dir := os.Getenv("WICK_E2E_PROVIDER_BIN"), os.Getenv("WICK_E2E_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_E2E_PROVIDER_BIN and WICK_E2E_DIR")
	}
	useFreshServers(t, startServe)
	watchdog(t, 3*time.Minute)
	data := filepath.Join(dir, "data-accounts")
	_ = os.RemoveAll(data) // fresh folders every run: the ids are asserted
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "e2e-acc", RunPerTurn: true,
		OpencodeConfig: &provider.OpencodeConfig{DataDir: data, AllowHosted: true}}
	id, _, err := provider.NewOpencodeAccount(ins)
	if err != nil || id != "a2" {
		t.Fatalf("new account folder: %q %v", id, err)
	}
	pin := provider.EncodePin([]string{"opencode", "a2"}, "opencode/big-pickle")
	if args := provider.ModelArgs(provider.SpawnOptions{Instance: &ins, ModelID: pin}, nil); len(args) != 2 || args[1] != "opencode/big-pickle" {
		t.Fatalf("pin must reach the CLI as the bare model: %q", args)
	}

	if err := os.MkdirAll(filepath.Join(dir, "ws"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &e2eRun{last: time.Now()}
	var spawnEnv []string
	r.a = provider.New(provider.Options{
		Workspace:     filepath.Join(dir, "ws"),
		SessionID:     "e2e-acc",
		Instance:      &ins,
		ModelID:       pin,
		IdleTimeout:   2 * time.Minute,
		ParserFactory: func() event.Parser { return event.NewOpencodeParser("e2e") },
		Spawner:       Spawner{Binary: bin},
		State:         state.New(nil),
		SendMode:      provider.SendRespawnQueue,
		OnEvent: func(e event.AgentEvent) {
			r.mu.Lock()
			r.events, r.last = append(r.events, e), time.Now()
			r.mu.Unlock()
		},
		OnExit: func(reason provider.ExitReason, _ string) {
			r.mu.Lock()
			r.exits = append(r.exits, reason)
			r.mu.Unlock()
		},
		OnSpawn: func(_ string, _ []string, env []string, _ int, msg string) {
			r.mu.Lock()
			r.spawns, r.last, spawnEnv = append(r.spawns, msg), time.Now(), env
			r.mu.Unlock()
		},
	})
	if err := r.a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.a.Stop() })
	_ = r.a.Send("Reply with exactly the word: alpha")
	// A fresh data folder makes opencode's first start slow (its own
	// storage/migration), so wait longer than waitSpawns' 30 s.
	for deadline := time.Now().Add(90 * time.Second); len(r.spawnMsgs()) < 1; {
		if time.Now().After(deadline) {
			r.log(t)
			t.Fatal("no spawn within 90s")
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.settle(t)
	r.log(t)
	r.noCrash(t)

	want := "XDG_DATA_HOME=" + filepath.Join(data, "accounts", "a2")
	r.mu.Lock()
	env := append([]string(nil), spawnEnv...)
	r.mu.Unlock()
	found := false
	for _, kv := range env {
		if kv == want {
			found = true
		}
		if strings.HasPrefix(kv, "XDG_DATA_HOME=") && kv != want {
			t.Fatalf("spawn ran in the wrong folder: %s", kv)
		}
	}
	if !found {
		t.Fatalf("spawn env lacks %s", want)
	}
	if !strings.Contains(strings.ToLower(r.text()), "alpha") {
		t.Fatalf("no answer from the a2 folder: %q", r.text())
	}
	if _, err := os.Stat(filepath.Join(data, "accounts", "a2", "opencode")); err != nil {
		t.Fatalf("opencode wrote nothing under a2: %v", err)
	}
}
