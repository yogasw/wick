package opencode

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestE2EServeRealBinary drives a real `opencode serve` through Spawn:
// one turn, a resumed second turn, and an abort in the middle of a long
// one. Opt-in (it calls a hosted model):
//
//	OPENCODE_E2E_BIN=/path/opencode OPENCODE_E2E_DIR=/scratch go test -run TestE2EServeRealBinary -v
//
// The server runs in its own memory-capped user scope.
func TestE2EServeRealBinary(t *testing.T) {
	bin, dir := os.Getenv("OPENCODE_E2E_BIN"), os.Getenv("OPENCODE_E2E_DIR")
	if bin == "" || dir == "" {
		t.Skip("set OPENCODE_E2E_BIN and OPENCODE_E2E_DIR")
	}
	prev := servers
	var pid int
	servers = newManager(func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		spec.wrap = func(b string, a []string) (string, []string, string) {
			return "systemd-run", append([]string{"--user", "--scope", "-q", "-p", "MemoryMax=900M", b}, a...), ""
		}
		h, err := startServe(ctx, spec, pw)
		if h != nil {
			pid = h.pid
		}
		return h, err
	})
	t.Cleanup(func() { servers.shutdown(); servers = prev })

	ws := filepath.Join(dir, "ws")
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "e2e", OpencodeConfig: &provider.OpencodeConfig{
		DataDir: filepath.Join(dir, "data-e2e"), Model: "opencode/big-pickle", AllowHosted: true,
	}}
	sp := Spawner{Binary: bin}
	turn := func(resume, msg string, killAfter time.Duration) (sid, text string, wall time.Duration, err error) {
		start := time.Now()
		p, err := sp.Spawn(context.Background(), provider.SpawnOptions{Workspace: ws, SessionID: "e2e-sess", Instance: &ins, ResumeID: resume, InitialMessage: msg})
		if err != nil {
			return "", "", 0, err
		}
		if killAfter > 0 {
			time.AfterFunc(killAfter, func() { _ = p.Kill() })
		}
		parser := event.NewOpencodeParser("e2e")
		sc := bufio.NewScanner(p.Stdout())
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			evs, _ := parser.ParseAll(sc.Text())
			for _, e := range evs {
				switch e.Type {
				case event.SessionStart:
					sid = e.SessionID
				case event.TextDelta:
					text += e.Text
				case event.Error:
					t.Logf("error event: %s", e.ErrorMsg)
				}
			}
		}
		err = p.Wait()
		return sid, text, time.Since(start), err
	}
	rss := func() int {
		b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "VmRSS:") {
				n, _ := strconv.Atoi(strings.Fields(l)[1])
				return n / 1024
			}
		}
		return -1
	}

	sid, text, wall, err := turn("", "Reply with exactly: pong", 0)
	t.Logf("turn1 (cold, incl. server boot) wall=%s sid=%s text=%q err=%v server_pid=%d rss=%dMB", wall, sid, text, err, pid, rss())
	if err != nil || sid == "" || text == "" {
		t.Fatalf("turn 1 failed")
	}
	sid2, text2, wall2, err := turn(sid, "What exact word did you reply with last time? One word.", 0)
	t.Logf("turn2 (warm, resumed) wall=%s sid=%s text=%q err=%v rss=%dMB", wall2, sid2, text2, err, rss())
	if err != nil || sid2 != sid {
		t.Fatalf("resume failed: %s vs %s", sid2, sid)
	}
	sid3, _, wall3, err := turn(sid, "Use the bash tool to run `sleep 40`, then reply done.", 6*time.Second)
	t.Logf("turn3 (abort after 6s) wall=%s sid=%s err=%v rss=%dMB", wall3, sid3, err, rss())
	if err == nil || wall3 > 15*time.Second {
		t.Fatalf("abort did not end the turn promptly (err=%v wall=%s)", err, wall3)
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
		t.Fatal("abort killed the server")
	}
	_, text4, wall4, err := turn(sid, "Reply with exactly: alive", 0)
	t.Logf("turn4 (after abort, same server) wall=%s text=%q err=%v rss=%dMB", wall4, text4, err, rss())
	if err != nil {
		t.Fatal("server unusable after abort")
	}
}
