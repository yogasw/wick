//go:build livetest

package opencode

import (
	"bufio"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestLiveTwoServes runs two opencode instances side by side on the real
// binary: each gets its own `opencode serve` (port, password, data dir),
// a turn without a login ends in an error instead of hanging, and both
// servers are reaped after a short idle window. No login, no hosted
// model. Opt-in, inside a memory-capped scope:
//
//	systemd-run --user --scope -p MemoryMax=1500M env \
//	  WICK_LIVE_OPENCODE_BIN=/path/opencode WICK_LIVE_DIR=/scratch \
//	  go test -tags livetest -run TestLiveTwoServes -v ./internal/agents/provider/opencode/
func TestLiveTwoServes(t *testing.T) {
	bin, dir := os.Getenv("WICK_LIVE_OPENCODE_BIN"), os.Getenv("WICK_LIVE_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_LIVE_OPENCODE_BIN and WICK_LIVE_DIR")
	}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("WICK_PORT", "")

	type started struct {
		spec serverSpec
		h    *serverHandle
	}
	var mu sync.Mutex
	var starts []started
	prev := servers
	servers = newManager(func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		h, err := startServe(ctx, spec, pw)
		if h != nil {
			mu.Lock()
			starts = append(starts, started{spec, h})
			mu.Unlock()
		}
		return h, err
	})
	servers.Every = time.Second
	t.Cleanup(func() { servers.shutdown(); servers = prev })

	const idle = 5 * time.Second
	ws := filepath.Join(dir, "ws")
	_ = os.MkdirAll(ws, 0o755)
	liveIns := func(name string) provider.Instance {
		return provider.Instance{Type: provider.TypeOpencode, Name: name, Binary: bin, OpencodeConfig: &provider.OpencodeConfig{
			DataDir: filepath.Join(dir, "data-"+name), Model: liveModel(),
		}}
	}
	turn := func(name string) (errMsg string, wall, errAt time.Duration, err error) {
		ins := liveIns(name)
		return liveTurn(bin, ws, &ins, idle)
	}

	var wg sync.WaitGroup
	for _, name := range []string{"livetest-a", "livetest-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg, wall, errAt, err := turn(name)
			t.Logf("%s: unauthenticated turn wall=%s error_at=%s wait_err=%v error_event=%q", name,
				wall.Round(time.Millisecond), errAt.Round(time.Millisecond), err, msg)
			if msg == "" && err == nil {
				t.Errorf("%s: a turn without a login ended with no error", name)
			}
			if wall > 90*time.Second {
				t.Errorf("%s: turn took %s — hang", name, wall)
			}
		}()
	}
	wg.Wait()
	idleFrom := time.Now()

	mu.Lock()
	got := append([]started(nil), starts...)
	mu.Unlock()
	if len(got) != 2 || servers.Len() != 2 {
		t.Fatalf("started %d servers, manager holds %d; want 2 and 2", len(got), servers.Len())
	}
	a, b := got[0], got[1]
	ua, _ := url.Parse(a.h.url)
	ub, _ := url.Parse(b.h.url)
	t.Logf("server %s pid=%d url=%s", a.spec.instance, a.h.pid, a.h.url)
	t.Logf("server %s pid=%d url=%s", b.spec.instance, b.h.pid, b.h.url)
	if ua == nil || ub == nil || ua.Hostname() != "127.0.0.1" || ub.Hostname() != "127.0.0.1" || ua.Port() == "" || ub.Port() == "" {
		t.Fatalf("listening lines did not parse to 127.0.0.1:<port>: %q %q", a.h.url, b.h.url)
	}
	if ua.Port() == ub.Port() || a.h.pid == b.h.pid {
		t.Fatalf("servers share a port or pid: %s/%d %s/%d", ua.Port(), a.h.pid, ub.Port(), b.h.pid)
	}
	if a.h.password == "" || a.h.password == b.h.password {
		t.Fatal("servers share a password")
	}
	for _, s := range got {
		want := filepath.Join(dir, "data-"+s.spec.instance)
		if s.spec.dir != want {
			t.Errorf("%s: server dir %s, want %s", s.spec.instance, s.spec.dir, want)
		}
		xdg := procXDG(t, s.h.pid)
		t.Logf("%s: process XDG %v rss=%dMB", s.spec.instance, xdg, procRSS(s.h.pid))
		if len(xdg) == 0 {
			t.Errorf("%s: no XDG_* in the server's env", s.spec.instance)
		}
		for k, v := range xdg {
			if k == "XDG_DATA_DIRS" || k == "XDG_RUNTIME_DIR" || k == "XDG_CONFIG_DIRS" {
				continue // system search paths / the login's runtime dir, not per-instance state
			}
			if !strings.HasPrefix(v, want+string(filepath.Separator)) && v != want {
				t.Errorf("%s: %s=%s is outside its data dir %s", s.spec.instance, k, v, want)
			}
		}
	}

	// While the turn servers live, the catalog (login choices, models)
	// comes from them: no throwaway serve.
	extra := 0
	prevServe := catalogServe
	catalogServe = func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		extra++
		return startServe(ctx, spec, pw)
	}
	t.Cleanup(func() { catalogServe = prevServe })
	for _, name := range []string{"livetest-a", "livetest-b"} {
		cat, err := fetchInstanceCatalog(context.Background(), liveIns(name))
		if err != nil {
			t.Fatalf("%s: catalog from the live server: %v", name, err)
		}
		t.Logf("%s: catalog from the live server: %d providers, %d with login methods, connected=%v", name,
			len(cat.Providers), len(cat.Auth), cat.Connected)
		if len(cat.Providers) == 0 {
			t.Errorf("%s: empty catalog", name)
		}
	}
	if extra != 0 {
		t.Errorf("catalog booted %d throwaway servers while the turn servers were live", extra)
	}

	// Reaped once idle: the reaper ticks every second, the window is 5s.
	deadline := time.Now().Add(idle + 20*time.Second)
	for servers.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	if n := servers.Len(); n != 0 {
		t.Fatalf("%d servers still held after the idle window", n)
	}
	t.Logf("manager empty %s after the last turn ended", time.Since(idleFrom).Round(100*time.Millisecond))
	for _, s := range got {
		select {
		case <-s.h.done:
			t.Logf("%s: pid %d exited %s after the last turn ended", s.spec.instance, s.h.pid, time.Since(idleFrom).Round(100*time.Millisecond))
		case <-time.After(10 * time.Second):
			t.Errorf("%s: pid %d still running after reap", s.spec.instance, s.h.pid)
		}
	}
	t.Log("both servers reaped after the idle window")

	// No live server: the catalog boots a throwaway one and kills it.
	cat, err := fetchInstanceCatalog(context.Background(), liveIns("livetest-a"))
	if err != nil {
		t.Fatalf("catalog via a throwaway serve: %v", err)
	}
	if extra != 1 || servers.Len() != 0 {
		t.Errorf("throwaway catalog serves=%d, manager holds %d; want 1 and 0", extra, servers.Len())
	}
	t.Logf("catalog via a throwaway serve: %d providers", len(cat.Providers))
}

// TestLiveAuthFrom: instance B takes its login from A (AuthFrom). A turn
// on B links B's auth.json to A's, B's server still runs on B's own data
// dir, and unlinking removes only the link. Same env as TestLiveTwoServes.
func TestLiveAuthFrom(t *testing.T) {
	bin, dir := os.Getenv("WICK_LIVE_OPENCODE_BIN"), os.Getenv("WICK_LIVE_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_LIVE_OPENCODE_BIN and WICK_LIVE_DIR")
	}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("WICK_PORT", "")
	var mu sync.Mutex
	var hs []*serverHandle
	prev := servers
	servers = newManager(func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		h, err := startServe(ctx, spec, pw)
		if h != nil {
			mu.Lock()
			hs = append(hs, h)
			mu.Unlock()
		}
		return h, err
	})
	servers.Every = time.Second
	t.Cleanup(func() { servers.shutdown(); servers = prev })

	a := provider.Instance{Type: provider.TypeOpencode, Name: "livetest-owner", OpencodeConfig: &provider.OpencodeConfig{
		DataDir: filepath.Join(dir, "data-owner"), Model: liveModel()}}
	b := provider.Instance{Type: provider.TypeOpencode, Name: "livetest-sharer", AuthFrom: a.Name, OpencodeConfig: &provider.OpencodeConfig{
		DataDir: filepath.Join(dir, "data-sharer"), Model: liveModel()}}
	restore := provider.SwapAuthInstanceLookup(func(typ provider.Type, name string) (provider.Instance, error) {
		if typ == a.Type && name == a.Name {
			return a, nil
		}
		return provider.Instance{}, os.ErrNotExist
	})
	t.Cleanup(restore)
	if err := provider.ValidateAuthFrom([]provider.Instance{a, b}, b); err != nil {
		t.Fatalf("auth_from rejected: %v", err)
	}
	src, _ := provider.OpencodeAuthFile(a)
	dst, _ := provider.OpencodeAuthFile(b)

	ws := filepath.Join(dir, "ws")
	_ = os.MkdirAll(ws, 0o755)
	msg, wall, errAt, err := liveTurn(bin, ws, &b, 5*time.Second)
	t.Logf("sharer turn wall=%s error_at=%s wait_err=%v error_event=%q", wall.Round(time.Millisecond), errAt.Round(time.Millisecond), err, msg)
	if msg == "" && err == nil {
		t.Error("sharer: a turn without a login ended with no error")
	}
	link, lerr := os.Readlink(dst)
	t.Logf("sharer auth.json %s -> %s (err=%v)", dst, link, lerr)
	if lerr != nil || link != src {
		t.Fatalf("sharer auth.json is not a link to the owner's: %q, %v", link, lerr)
	}
	mu.Lock()
	got := append([]*serverHandle(nil), hs...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("started %d servers, want 1 (the sharer's)", len(got))
	}
	if xdg := procXDG(t, got[0].pid); xdg["XDG_DATA_HOME"] != b.OpencodeConfig.DataDir {
		t.Errorf("sharer server XDG_DATA_HOME=%s, want its own %s", xdg["XDG_DATA_HOME"], b.OpencodeConfig.DataDir)
	}

	if err := provider.UnlinkOpencodeAuth(b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("sharer auth.json still there after unlink: %v", err)
	}
	if st, err := os.Stat(filepath.Dir(src)); err != nil || !st.IsDir() {
		t.Errorf("owner's opencode dir gone after unlink: %v", err)
	}
	t.Log("auth.json link created on spawn and removed by unlink; owner dir kept")
}

// liveModel is the model a live turn asks for; no login holds it.
func liveModel() string {
	if m := os.Getenv("WICK_LIVE_OPENCODE_MODEL"); m != "" {
		return m
	}
	return "anthropic/claude-sonnet-4-5"
}

// liveTurn runs one turn on ins and reports the last error event, the
// turn's wall time and when that error arrived.
func liveTurn(bin, ws string, ins *provider.Instance, idle time.Duration) (errMsg string, wall, errAt time.Duration, err error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Spawner{Binary: bin}.Spawn(ctx, provider.SpawnOptions{Workspace: ws, SessionID: "live-" + ins.Name,
		Instance: ins, InitialMessage: "hello", IdleTimeout: idle})
	if err != nil {
		return "", 0, 0, err
	}
	parser := event.NewOpencodeParser(ins.Name)
	sc := bufio.NewScanner(p.Stdout())
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		evs, _ := parser.ParseAll(sc.Text())
		for _, e := range evs {
			if e.Type == event.Error {
				errMsg, errAt = e.ErrorMsg, time.Since(start)
			}
		}
	}
	err = p.Wait()
	return errMsg, time.Since(start), errAt, err
}

// procXDG returns only the XDG_* entries of pid's environment — the rest
// is never read out.
func procXDG(t *testing.T, pid int) map[string]string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Fatalf("read environ of %d: %v", pid, err)
	}
	out := map[string]string{}
	for _, kv := range strings.Split(string(b), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "XDG_") {
			out[k] = v
		}
	}
	return out
}

func procRSS(pid int) int {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if _, v, ok := strings.Cut(string(b), "VmRSS:"); ok {
		n, _ := strconv.Atoi(strings.Fields(v)[0])
		return n / 1024
	}
	return -1
}
