// Package plugintest runs the real example_a2a_repeater service plugin under
// a service plugin Host for end-to-end tests outside internal/services/plugin.
package plugintest

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	serviceplugin "github.com/yogasw/wick/internal/services/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// RepeaterKey is the service key of example_a2a_repeater.
const RepeaterKey = "example_a2a_repeater"

// StartRepeater builds plugins/service/example_a2a_repeater, runs it under a
// fresh host (stopped on cleanup) and returns the host once it is running.
// Skipped with -short.
func StartRepeater(t *testing.T) *serviceplugin.Host {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	_, file, _, _ := runtime.Caller(0)
	bin := filepath.Join(t.TempDir(), RepeaterKey)
	cmd := exec.Command("go", "build", "-o", bin, "./service/"+RepeaterKey)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	cmd.Dir = filepath.Join(root, "plugins")
	// plugins/ requires a released wick; only the repo go.work points it at
	// this checkout, so force it even when the caller runs with GOWORK=off.
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", RepeaterKey, err, out)
	}
	h := serviceplugin.NewHost(nil, t.TempDir())
	s := h.Add(RepeaterKey, "0.0.1", wickplugin.ServiceModule{
		Meta:         wickplugin.ToolMeta{Key: RepeaterKey, Name: "A2A repeater"},
		Routes:       []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}},
		Capabilities: []string{wickplugin.CapRemoteSource},
	}, bin)
	t.Cleanup(h.Shutdown)
	h.Start()
	deadline := time.Now().Add(20 * time.Second)
	for s.Sup.Status().State != serviceplugin.StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("%s never started: %+v", RepeaterKey, s.Sup.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h
}

// Transport is the pluginremote transport of h's services.
func Transport(h *serviceplugin.Host) func(string) (http.RoundTripper, error) {
	return func(key string) (http.RoundTripper, error) {
		s, ok := h.Get(key)
		if !ok {
			return nil, serviceplugin.ErrNotRunning
		}
		return s.Sup.Transport()
	}
}
