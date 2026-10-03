package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/pkg/job"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/safeexec"
)

// buildJobEcho compiles cmd/plugins/jobecho into <root>/jobs/jobecho with its
// dumped manifest, the layout the host scans. Returns <root>/jobs.
func buildJobEcho(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "jobs", "jobecho")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "jobecho"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	build := safeexec.Command("go", "build", "-o", bin, "github.com/yogasw/wick/cmd/plugins/jobecho")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build jobecho: %v", err)
	}
	manifest, err := safeexec.Command(bin, "--dump-manifest").Output()
	if err != nil {
		t.Fatalf("dump-manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "jobs")
}

type cfgMap map[string]string

func (m cfgMap) GetOwned(_, key string) string { return m[key] }

func runWith(m job.Module, cfg map[string]string) (string, error) {
	ctx := job.WithCtx(context.Background(), job.NewCtx(m.Meta.Key, cfgMap(cfg)))
	return m.Run(ctx)
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func TestJobPluginEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	t.Setenv("WICK_PLUGIN_SOCKET_DIR", t.TempDir())
	dir := buildJobEcho(t)

	// Manifest: kind job, job meta + configs carried.
	found, err := connplugin.ScanKind(dir, wickplugin.KindJob)
	if err != nil || len(found) != 1 {
		t.Fatalf("scan = %+v, %v", found, err)
	}
	mf := found[0].Manifest
	if mf.Kind != wickplugin.KindJob || mf.Job == nil || mf.Job.Meta.DefaultCron != "*/5 * * * *" || len(mf.Job.Configs) != 2 {
		t.Fatalf("manifest not kind=job with meta/configs: %+v", mf)
	}

	// Registration into the job registry.
	var got []job.Module
	var recorded []string
	n := load(dir, nil, func(key, kind, version string) error {
		recorded = append(recorded, key+"/"+kind)
		return nil
	}, func(m job.Module) { got = append(got, m) }, BuildModule)
	if n != 1 || len(got) != 1 || got[0].Meta.Key != "jobecho" || got[0].Meta.Name != "Job Echo" {
		t.Fatalf("registered %d: %+v", n, got)
	}
	if len(recorded) != 1 || recorded[0] != "jobecho/job" {
		t.Fatalf("state record = %v", recorded)
	}
	if err := job.ValidateJobs(got); err != nil {
		t.Fatalf("plugin module must pass ValidateJobs: %v", err)
	}
	mod := got[0]

	// spawn → Run → result + streamed log; the process is gone afterwards.
	out, err := runWith(mod, map[string]string{"mode": "ok", "message": "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "done: hi") || !strings.Contains(out, "echo hi") || !strings.Contains(out, "Plugin log") {
		t.Fatalf("result missing output/log: %q", out)
	}
	i := strings.Index(out, "(pid ")
	pid, _ := strconv.Atoi(strings.TrimSuffix(strings.Fields(out[i+5:])[0], ")"))
	if pid == 0 {
		t.Fatalf("no pid in %q", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for pidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(pid) {
		t.Fatalf("plugin process %d still alive after Run", pid)
	}

	// Error propagation.
	if _, err := runWith(mod, map[string]string{"mode": "fail"}); !errors.Is(err, wickplugin.ErrJobRun) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want ErrJobRun boom, got %v", err)
	}

	// Timeout kills the run.
	short := buildModule(found[0], spawnProcess, 500*time.Millisecond)
	start := time.Now()
	_, err = runWith(short, map[string]string{"mode": "sleep"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timeout not enforced: %s", time.Since(start))
	}
}

type fakeConn struct{ err error }

func (f fakeConn) Run(_ context.Context, trigger string, cfg map[string]string, onLog func(string)) (string, error) {
	onLog("trigger=" + trigger)
	return "cfg=" + cfg["a"], f.err
}
func (fakeConn) Schema(context.Context) ([]byte, error) { return nil, nil }

func TestBuildModuleKillsProcessAndPassesConfig(t *testing.T) {
	f := connplugin.Found{Key: "k", BinaryPath: "/bin/k", Manifest: wickplugin.Manifest{
		Version: "1.2.3",
		Job:     &wickplugin.JobModule{Meta: job.Meta{Name: "K", DefaultCron: "0 * * * *"}},
	}}
	_ = json.Unmarshal([]byte(`[{"Key":"a"}]`), &f.Manifest.Job.Configs)
	killed := 0
	spawn := func(string) (func(), wickplugin.JobConn, error) { return func() { killed++ }, fakeConn{}, nil }
	m := buildModule(f, spawn, time.Minute)
	if m.Meta.Key != "k" || m.Meta.Icon == "" {
		t.Fatalf("meta = %+v", m.Meta)
	}
	out, err := runWith(m, map[string]string{"a": "1"})
	if err != nil || !strings.HasPrefix(out, "cfg=1") || !strings.Contains(out, "trigger=cron") || !strings.Contains(out, "v1.2.3") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if killed != 1 {
		t.Fatalf("process must be killed once per run, killed=%d", killed)
	}

	spawnErr := func(string) (func(), wickplugin.JobConn, error) { return nil, nil, errors.New("no exec") }
	if _, err := runWith(buildModule(f, spawnErr, time.Minute), nil); err == nil || !strings.Contains(err.Error(), "no exec") {
		t.Fatalf("spawn error must propagate, got %v", err)
	}
}

func TestLoadSkipsDisabledAndForeignKinds(t *testing.T) {
	dir := t.TempDir()
	for key, kind := range map[string]string{"a_job": wickplugin.KindJob, "a_conn": wickplugin.KindConnector} {
		if err := os.MkdirAll(filepath.Join(dir, key), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"kind": kind, "module": map[string]any{"meta": map[string]any{"key": key}}})
		_ = os.WriteFile(filepath.Join(dir, key, "plugin.json"), b, 0o644)
	}
	var got []job.Module
	build := func(f connplugin.Found) job.Module { return job.Module{Meta: job.Meta{Key: f.Key}} }
	n := load(dir, func(string) bool { return false }, nil, func(m job.Module) { got = append(got, m) }, build)
	if n != 0 || len(got) != 0 {
		t.Fatalf("disabled job must not register: %d %+v", n, got)
	}
}

// blockConn's Run blocks until its process is killed.
type blockConn struct{ killed chan struct{} }

func (c blockConn) Run(ctx context.Context, _ string, _ map[string]string, _ func(string)) (string, error) {
	select {
	case <-c.killed:
		return "", errors.New("killed")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (blockConn) Schema(context.Context) ([]byte, error) { return nil, nil }

func TestKillAllReapsRunningJobs(t *testing.T) {
	f := connplugin.Found{Key: "k", BinaryPath: "/bin/k", Manifest: wickplugin.Manifest{Job: &wickplugin.JobModule{}}}
	killed := make(chan struct{})
	kills := 0
	spawn := func(string) (func(), wickplugin.JobConn, error) {
		return func() { kills++; close(killed) }, blockConn{killed: killed}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := runWith(buildModule(f, spawn, time.Minute), nil); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live.Lock()
		n := len(live.kills)
		live.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job process never registered as live")
		}
		time.Sleep(5 * time.Millisecond)
	}
	KillAll()
	if err := <-done; err == nil {
		t.Fatal("interrupted run must fail")
	}
	if kills != 1 {
		t.Fatalf("kill must run exactly once (KillAll + deferred), got %d", kills)
	}
	live.Lock()
	defer live.Unlock()
	if len(live.kills) != 0 {
		t.Fatalf("live kills left: %d", len(live.kills))
	}
}
