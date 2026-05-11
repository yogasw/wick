package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stubWriter implements HookConfigWriter for tests. The Write method
// just records the call; nothing is written to disk.
type stubWriter struct {
	written bool
	err     error
}

func (w *stubWriter) Write(workspace, gateBin string) error {
	w.written = true
	return w.err
}
func (w *stubWriter) Remove(workspace string) error           { return nil }
func (w *stubWriter) DryRun(string, string) (string, []byte, error) { return "", nil, nil }

// stubProber implements Prober for tests. createSentinel=true makes it
// touch the sentinel file (simulating provider that IGNORED our deny).
// createSentinel=false leaves the sentinel absent (provider honored deny).
type stubProber struct {
	createSentinel bool
	err            error
}

func (p *stubProber) SendSentinel(ctx context.Context, workspace, sentinel string) error {
	if p.createSentinel {
		_ = os.WriteFile(sentinel, []byte("ran"), 0o644)
	}
	return p.err
}

func resetAll() {
	reset()
	resetWriters()
	resetProbers()
}

func TestHookCapabilityCheck_NotRegistered(t *testing.T) {
	resetAll()
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName: "nonexistent",
		GateBinary:   "/fake/gate",
	})
	if res.HookVerified {
		t.Error("HookVerified should be false for unregistered provider")
	}
	if res.HookError == "" {
		t.Error("expected HookError populated")
	}
}

func TestHookCapabilityCheck_HookNotSupported(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: false})
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName: "x",
		GateBinary:   "/fake/gate",
	})
	if res.HookVerified {
		t.Error("expected HookVerified=false")
	}
	if res.HookError == "" {
		t.Error("expected HookError")
	}
}

func TestHookCapabilityCheck_WriterMissing(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName: "x",
		GateBinary:   "/fake/gate",
	})
	if res.HookError == "" {
		t.Error("expected HookError when writer missing")
	}
}

func TestHookCapabilityCheck_ProberMissing(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{})
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName: "x",
		GateBinary:   "/fake/gate",
	})
	if res.HookError == "" {
		t.Error("expected HookError when prober missing")
	}
}

func TestHookCapabilityCheck_GateBinaryEmpty(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{})
	RegisterProber("x", &stubProber{})
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName: "x",
	})
	if res.HookError == "" {
		t.Error("expected HookError when gate binary unset")
	}
}

func TestHookCapabilityCheck_HappyPath(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true, InterceptScope: "shell"})
	RegisterHookConfigWriter("x", &stubWriter{})
	RegisterProber("x", &stubProber{createSentinel: false})

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "x",
		GateBinary:    "/fake/gate",
		WorkspaceRoot: t.TempDir(),
	})

	if !res.HookVerified {
		t.Errorf("expected HookVerified=true, got error: %s", res.HookError)
	}
	if res.InterceptScope != "shell" {
		t.Errorf("InterceptScope should be inherited from registry, got %q", res.InterceptScope)
	}
	if res.HookProbedAt.IsZero() {
		t.Error("HookProbedAt should be set")
	}
}

func TestHookCapabilityCheck_SentinelCreated(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{})
	RegisterProber("x", &stubProber{createSentinel: true})

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "x",
		GateBinary:    "/fake/gate",
		WorkspaceRoot: t.TempDir(),
	})
	if res.HookVerified {
		t.Error("expected HookVerified=false when sentinel created")
	}
	if res.HookError == "" {
		t.Error("expected HookError describing sentinel creation")
	}
}

func TestHookCapabilityCheck_WriterUnsupported(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{err: ErrUnsupported})
	RegisterProber("x", &stubProber{})

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "x",
		GateBinary:    "/fake/gate",
		WorkspaceRoot: t.TempDir(),
	})
	if res.HookVerified {
		t.Error("expected HookVerified=false when writer reports unsupported")
	}
	if res.HookError == "" {
		t.Error("expected HookError")
	}
}

func TestHookCapabilityCheck_ProberErrorButNoSentinel(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{})
	RegisterProber("x", &stubProber{err: errors.New("provider exited non-zero")})

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "x",
		GateBinary:    "/fake/gate",
		WorkspaceRoot: t.TempDir(),
	})
	// Ambiguous case treated as verified — provider erroring while
	// sentinel absent typically means the blocked tool propagated
	// non-zero up to the CLI, which is what we want.
	if !res.HookVerified {
		t.Errorf("expected verified=true (deny honored), got error %s", res.HookError)
	}
}

func TestHookCapabilityCheck_CleansUpWorkspace(t *testing.T) {
	resetAll()
	Register("x", Capability{HookSupported: true})
	RegisterHookConfigWriter("x", &stubWriter{})
	RegisterProber("x", &stubProber{})

	root := t.TempDir()
	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "x",
		GateBinary:    "/fake/gate",
		WorkspaceRoot: root,
	})
	_ = res

	// After the call, no wick-capability-* subdir should remain in root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Base(e.Name()) != "" && e.IsDir() {
			t.Errorf("workspace leaked: %s", e.Name())
		}
	}
}
