package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// writeTool installs a verifiable kind=tool plugin key under dir whose binary
// holds content (a new content = an update).
func writeTool(t *testing.T, dir, key, content string) {
	t.Helper()
	d := filepath.Join(dir, key)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, key), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(content))
	m := wickplugin.Manifest{
		SchemaVersion: 1, Version: content, Kind: wickplugin.KindTool, ProtoVersion: wickplugin.ProtoVersion,
		Entry: key, OSArch: []string{runtime.GOOS + "/" + runtime.GOARCH}, SHA256: hex.EncodeToString(h[:]),
		Module: connector.Module{Meta: connector.Meta{Key: key, Name: key}},
		Tool:   &wickplugin.ToolModule{Meta: wickplugin.ToolMeta{Name: key, Icon: "x"}},
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(d, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeManifestSHA(t *testing.T, dir, key, sha string) {
	t.Helper()
	path := filepath.Join(dir, key, "plugin.json")
	b, _ := os.ReadFile(path)
	var m wickplugin.Manifest
	json.Unmarshal(b, &m)
	m.SHA256 = sha
	b, _ = json.Marshal(m)
	os.WriteFile(path, b, 0o644)
}

type fakeSink struct {
	mu     sync.Mutex
	mods   map[string]tool.Module
	adds   []string
	remove []string
	err    error
}

func newFakeSink() *fakeSink { return &fakeSink{mods: map[string]tool.Module{}} }

func (s *fakeSink) Add(m tool.Module) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.mods[m.Meta.Key] = m
	s.adds = append(s.adds, m.Meta.Key+"@"+m.Meta.Description)
	return nil
}

func (s *fakeSink) Remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mods, key)
	s.remove = append(s.remove, key)
}

func (s *fakeSink) get(key string) (tool.Module, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mods[key]
	return m, ok
}

// procs counts live fake plugin processes, so a test can prove none leaked.
type procs struct {
	h     http.Handler
	live  atomic.Int64
	spawn atomic.Int64
	gate  chan struct{} // when set, a spawn blocks until it is closed
}

func (pr *procs) fn(_, socket string) (func(), wickplugin.ToolConn, func() bool, error) {
	pr.spawn.Add(1)
	if pr.gate != nil {
		<-pr.gate
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, nil, err
	}
	h := pr.h
	if h == nil {
		h = echoHandler()
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	pr.live.Add(1)
	var dead atomic.Bool
	kill := func() {
		if dead.CompareAndSwap(false, true) {
			pr.live.Add(-1)
			srv.Close()
		}
	}
	return kill, &fakeConn{}, func() bool { return !dead.Load() }, nil
}

type enabledMap struct {
	mu  sync.Mutex
	m   map[string]bool
	err error
}

func (e *enabledMap) get(k string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return false, e.err
	}
	v, ok := e.m[k]
	return !ok || v, nil
}

func (e *enabledMap) set(k string, v bool) {
	e.mu.Lock()
	e.m[k] = v
	e.mu.Unlock()
}

// livePool loads dir at "boot" (registering into boot through validate) and
// attaches sink. Every runner spawns through pr.
func livePool(t *testing.T, dir string, en *enabledMap, boot *[]string, validate func(tool.Module) error, sink Sink, pr *procs) *Pool {
	t.Helper()
	p := NewPool()
	t.Cleanup(p.KillAll)
	sd := sockDir(t)
	build := func(f connplugin.Found) tool.Module {
		m := p.buildModule(f, pr.fn, sd)
		m.Meta.Description = f.Manifest.Version // lets a test see which version is mounted
		return m
	}
	p.load(dir, en.get, nil, bootSink{register: func(m tool.Module) { *boot = append(*boot, m.Meta.Key) }, validate: validate}, build)
	p.Attach(sink)
	return p
}

func newEnabled() *enabledMap { return &enabledMap{m: map[string]bool{}} }

// getTool runs GET /tools/<key>/api through the module the sink holds now.
func getTool(sink *fakeSink, key string) int {
	m, ok := sink.get(key)
	if !ok {
		return http.StatusNotFound
	}
	rr := &recRouter{h: map[string]tool.HandlerFunc{}, wh: map[string]tool.WebhookHandlerFunc{}}
	m.Register(rr)
	return call(rr, httptest.NewRequest("GET", "/tools/demo/api", nil)).Code
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBootLoadsEnabledAndSkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "on", "v1")
	writeTool(t, dir, "off", "v1")
	en := newEnabled()
	en.set("off", false)
	var boot []string
	p := livePool(t, dir, en, &boot, nil, newFakeSink(), &procs{})
	if strings.Join(boot, ",") != "on" {
		t.Fatalf("boot registered %v", boot)
	}
	if st, _ := p.State("on"); st != StateActive || !p.Owns("on") {
		t.Fatalf("enabled plugin not loaded at boot: %s", st)
	}
	if st, reason := p.State("off"); st != StateIdle || reason != "" {
		t.Fatalf("disabled plugin: %s %q", st, reason)
	}
	if st, _ := p.State("never-seen"); st != StatePending {
		t.Fatalf("unseen key: %s", st)
	}
	if mods := p.Modules(); len(mods) != 1 || mods[0].Meta.Key != "on" {
		t.Fatalf("modules %v", mods)
	}
}

// B2: boot runs the live check; a rejected plugin is skipped with Error and
// the boot goes on.
func TestBootRejectsCollidingKeyAsError(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "taken", "v1")
	writeTool(t, dir, "fine", "v1")
	var boot []string
	validate := func(m tool.Module) error {
		if m.Meta.Key == "taken" {
			return errors.New(`key "taken" is already used by a built-in tool`)
		}
		return nil
	}
	p := livePool(t, dir, newEnabled(), &boot, validate, newFakeSink(), &procs{})
	if strings.Join(boot, ",") != "fine" {
		t.Fatalf("boot registered %v", boot)
	}
	if st, reason := p.State("taken"); st != StateError || !strings.Contains(reason, "built-in") {
		t.Fatalf("taken: %s %q", st, reason)
	}
	if p.Owns("taken") {
		t.Fatal("rejected plugin owned")
	}
	// Reject (boot mount failure) also ends as Error, unloaded.
	p.Reject("fine", errors.New("duplicate route"))
	if st, reason := p.State("fine"); st != StateError || reason != "duplicate route" || p.Owns("fine") {
		t.Fatalf("rejected: %s %q", st, reason)
	}
}

func TestHotInstallUpdateDisableEnableRemove(t *testing.T) {
	dir := t.TempDir()
	en := newEnabled()
	var boot []string
	sink := newFakeSink()
	pr := &procs{}
	p := livePool(t, dir, en, &boot, nil, sink, pr)
	p.DrainGrace = time.Second

	// Installed while running → loaded through the sink and routable.
	writeTool(t, dir, "demo", "v1")
	if st, _ := p.State("demo"); st != StatePending {
		t.Fatalf("before the pass: %s", st)
	}
	p.Reconcile()
	if code := getTool(sink, "demo"); code != http.StatusOK || !p.Owns("demo") {
		t.Fatalf("hot install: code %d", code)
	}
	// Unchanged → nothing happens.
	p.Reconcile()
	if len(sink.adds) != 1 {
		t.Fatalf("re-added an unchanged plugin: %v", sink.adds)
	}
	// Update → old unrouted and its process stopped, new version mounted.
	writeTool(t, dir, "demo", "v2")
	p.Reconcile()
	if m, _ := sink.get("demo"); m.Meta.Description != "v2" || len(sink.remove) != 1 {
		t.Fatalf("update: mounted %q, removes %v", m.Meta.Description, sink.remove)
	}
	waitFor(t, "old process stopped", func() bool { return pr.live.Load() == 0 })
	if code := getTool(sink, "demo"); code != http.StatusOK {
		t.Fatalf("after update: %d", code)
	}
	// Disable → unloaded, process gone, state idle.
	en.set("demo", false)
	p.Reconcile()
	if _, ok := sink.get("demo"); ok || p.Owns("demo") {
		t.Fatal("disabled plugin still loaded")
	}
	waitFor(t, "disabled process stopped", func() bool { return pr.live.Load() == 0 })
	if st, _ := p.State("demo"); st != StateIdle {
		t.Fatalf("disabled: %s", st)
	}
	// Enable → back.
	en.set("demo", true)
	p.Reconcile()
	if code := getTool(sink, "demo"); code != http.StatusOK {
		t.Fatalf("re-enabled: %d", code)
	}
	// Uninstall: Remove, then a tick before the files go must not reload it.
	p.Remove("demo")
	p.Reconcile()
	if _, ok := sink.get("demo"); ok {
		t.Fatal("watcher reloaded a plugin being uninstalled")
	}
	os.RemoveAll(filepath.Join(dir, "demo"))
	p.Reconcile()
	writeTool(t, dir, "demo", "v3") // reinstalled later: loads again
	p.Reconcile()
	if _, ok := sink.get("demo"); !ok {
		t.Fatal("reinstall after uninstall not loaded")
	}
	p.Remove("demo")
	waitFor(t, "no process left", func() bool { return pr.live.Load() == 0 })
}

// S2: an unreadable enable flag keeps the current state — a disabled plugin
// is not switched back on.
func TestEnableStateErrorKeepsCurrentState(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "demo", "v1")
	en := newEnabled()
	en.set("demo", false)
	var boot []string
	sink := newFakeSink()
	p := livePool(t, dir, en, &boot, nil, sink, &procs{})
	en.mu.Lock()
	en.err = errors.New("db timeout")
	en.mu.Unlock()
	p.Reconcile()
	if _, ok := sink.get("demo"); ok || p.Owns("demo") {
		t.Fatal("disabled plugin re-enabled on a state read error")
	}
	// And a loaded one stays loaded.
	en.mu.Lock()
	en.err = nil
	en.mu.Unlock()
	en.set("demo", true)
	p.Reconcile()
	en.mu.Lock()
	en.err = errors.New("db timeout")
	en.mu.Unlock()
	p.Reconcile()
	if !p.Owns("demo") {
		t.Fatal("loaded plugin dropped on a state read error")
	}
}

func TestFailedLoadReportsError(t *testing.T) {
	dir := t.TempDir()
	var boot []string
	sink := newFakeSink()
	p := livePool(t, dir, newEnabled(), &boot, nil, sink, &procs{})

	writeTool(t, dir, "demo", "v1")
	p.Reconcile()
	// Corrupt the binary in place (a bad update): unloaded, Error with reason.
	os.WriteFile(filepath.Join(dir, "demo", "demo"), []byte("tampered"), 0o755)
	writeManifestSHA(t, dir, "demo", "0000")
	p.Reconcile()
	if p.Owns("demo") {
		t.Fatal("unverified update left mounted")
	}
	if st, reason := p.State("demo"); st != StateError || !strings.Contains(reason, "verification failed") {
		t.Fatalf("status %s %q", st, reason)
	}

	// The sink refusing (key taken by a built-in) is an error too.
	writeTool(t, dir, "taken", "v1")
	sink.mu.Lock()
	sink.err = errors.New(`key "taken" is already used by a built-in tool`)
	sink.mu.Unlock()
	p.Reconcile()
	if st, reason := p.State("taken"); st != StateError || !strings.Contains(reason, "built-in") {
		t.Fatalf("taken: %s %q", st, reason)
	}
	// A refused key was never loaded, so it must not be "removed" (that would
	// drop the built-in it collides with).
	for _, k := range sink.remove {
		if k == "taken" {
			t.Fatal("sink.Remove called for a key the pool never loaded")
		}
	}
}

// B1: a spawn in flight when the runner is stopped is killed once it
// completes, and a stopped runner never spawns again.
func TestStoppedRunnerKillsInFlightSpawnAndNeverRespawns(t *testing.T) {
	pr := &procs{gate: make(chan struct{})}
	run := newRunner("demo", "x", sockDir(t), false, pr.fn)
	errc := make(chan error, 1)
	go func() {
		_, rel, err := run.Acquire(context.Background(), nil)
		if rel != nil {
			rel()
		}
		errc <- err
	}()
	waitFor(t, "spawn started", func() bool { return pr.spawn.Load() == 1 })
	run.Stop()
	close(pr.gate)
	if err := <-errc; err == nil {
		t.Fatal("request served by a stopped runner")
	}
	waitFor(t, "orphan killed", func() bool { return pr.live.Load() == 0 })
	if run.Running() {
		t.Fatal("stopped runner still has a process")
	}
	if _, _, err := run.Acquire(context.Background(), nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("acquire after stop: %v", err)
	}
	if n := pr.spawn.Load(); n != 1 {
		t.Fatalf("spawned %d times, want 1", n)
	}
}

// S3: Close lets a request in flight finish, then kills; past the grace it
// kills anyway.
func TestCloseDrainsInFlightThenKills(t *testing.T) {
	pr := &procs{}
	run := newRunner("demo", "x", sockDir(t), false, pr.fn)
	_, rel, err := run.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := run.Close(5 * time.Second)
	time.Sleep(100 * time.Millisecond)
	if pr.live.Load() != 1 {
		t.Fatal("killed while a request was in flight")
	}
	if _, _, err := run.Acquire(context.Background(), nil); err == nil {
		t.Fatal("closed runner accepted a new request")
	}
	rel()
	<-done
	if pr.live.Load() != 0 {
		t.Fatal("not killed after the request finished")
	}

	run2 := newRunner("demo", "x", sockDir(t), false, pr.fn)
	if _, _, err := run2.Acquire(context.Background(), nil); err != nil { // never released
		t.Fatal(err)
	}
	start := time.Now()
	<-run2.Close(150 * time.Millisecond)
	if pr.live.Load() != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("grace not enforced: live=%d", pr.live.Load())
	}
}

// Requests, reconciles (enable flips, updates) and removes running together:
// run with -race. Afterwards no process may be left behind.
func TestConcurrentReconcileAndRequests(t *testing.T) {
	dir := t.TempDir()
	en := newEnabled()
	var boot []string
	sink := newFakeSink()
	pr := &procs{}
	p := livePool(t, dir, en, &boot, nil, sink, pr)
	p.DrainGrace = 200 * time.Millisecond
	writeTool(t, dir, "demo", "v1")
	p.Reconcile()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					getTool(sink, "demo") // 200, 404 or 503 are all fine mid-swap
					p.State("demo")
					p.Inflight()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			switch i % 3 {
			case 0:
				en.set("demo", false)
			case 1:
				en.set("demo", true)
			case 2:
				writeTool(t, dir, "demo", "v"+string(rune('a'+i)))
			}
			p.Reconcile()
		}
	}()
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
	p.Remove("demo")
	waitFor(t, "every process stopped", func() bool { return pr.live.Load() == 0 && p.Inflight() == 0 })
}

func TestReconcileNoopBeforeAttach(t *testing.T) {
	p := NewPool()
	t.Cleanup(p.KillAll)
	p.Reconcile() // no dir, no sink: must not panic
	if p.Owns("x") {
		t.Fatal("owns unknown key")
	}
}
