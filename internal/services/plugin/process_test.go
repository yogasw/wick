package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// buildRepeater builds plugins/service/example_a2a_repeater (its own module)
// into a temp dir and returns the binary path.
func buildRepeater(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	if runtime.GOOS != "linux" {
		t.Skip("process checks read /proc")
	}
	bin := filepath.Join(t.TempDir(), "example_a2a_repeater")
	cmd := exec.Command("go", "build", "-o", bin, "./service/example_a2a_repeater")
	cmd.Dir = filepath.Join("..", "..", "..", "plugins")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example_a2a_repeater: %v\n%s", err, out)
	}
	return bin
}

// repeaterModule is the manifest section of example_a2a_repeater.
func repeaterModule() wickplugin.ServiceModule {
	return wickplugin.ServiceModule{
		Meta:         wickplugin.ToolMeta{Key: "example_a2a_repeater", Name: "A2A repeater"},
		Routes:       []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}},
		Capabilities: []string{wickplugin.CapRemoteSource},
	}
}

// pidsOf lists live processes running binary.
func pidsOf(binary string) []int {
	var out []int
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if err == nil && exe == binary {
			out = append(out, pid)
		}
	}
	return out
}

func socketsIn(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.sock"))
	return m
}

// startRepeater runs a real example_a2a_repeater under a fresh host.
func startRepeater(t *testing.T) (*Host, string, string) {
	t.Helper()
	bin := buildRepeater(t)
	sockDir := t.TempDir()
	h := NewHost(nil, sockDir)
	s := h.Add("example_a2a_repeater", "0.0.1", repeaterModule(), bin)
	t.Cleanup(h.Shutdown)
	h.Start()
	waitFor(t, "repeater running", func() bool { return s.Sup.Status().State == StateRunning })
	return h, bin, sockDir
}

func TestShutdownStopsServiceProcesses(t *testing.T) {
	h, bin, sockDir := startRepeater(t)
	if n := len(pidsOf(bin)); n != 1 {
		t.Fatalf("want 1 live plugin process, got %d", n)
	}
	h.Shutdown()
	if p := pidsOf(bin); len(p) != 0 {
		t.Fatalf("plugin processes alive after Shutdown: %v", p)
	}
	if s := socketsIn(sockDir); len(s) != 0 {
		t.Fatalf("sockets left after Shutdown: %v", s)
	}
	for _, s := range h.List() {
		if st := s.Sup.Status().State; st != StateStopped {
			t.Fatalf("%s state = %s after Shutdown", s.Key, st)
		}
	}
}
