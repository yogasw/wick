package capability

import (
	"context"
	"errors"
	"testing"
)

type fakeWriter struct {
	writeCalls  int
	removeCalls int
	dryRunPath  string
	dryRunBytes []byte
	failWrite   error
}

func (f *fakeWriter) Write(workspace, gateBin string) error {
	f.writeCalls++
	return f.failWrite
}
func (f *fakeWriter) Remove(workspace string) error {
	f.removeCalls++
	return nil
}
func (f *fakeWriter) DryRun(workspace, gateBin string) (string, []byte, error) {
	return f.dryRunPath, f.dryRunBytes, nil
}

func TestHookConfigWriterRegisterLookup(t *testing.T) {
	resetWriters()
	w := &fakeWriter{}
	RegisterHookConfigWriter("claude", w)

	got, ok := LookupHookConfigWriter("claude")
	if !ok {
		t.Fatal("LookupHookConfigWriter(claude) returned ok=false")
	}
	if got != w {
		t.Errorf("got %p, want %p", got, w)
	}
}

func TestHookConfigWriterUnknownProvider(t *testing.T) {
	resetWriters()
	if _, ok := LookupHookConfigWriter("missing"); ok {
		t.Error("expected ok=false for unregistered provider")
	}
}

type fakeProber struct {
	called bool
	err    error
}

func (p *fakeProber) SendSentinel(ctx context.Context, workspace, sentinelPath string) error {
	p.called = true
	return p.err
}

func TestProberRegisterLookup(t *testing.T) {
	resetProbers()
	p := &fakeProber{}
	RegisterProber("codex", p)

	got, ok := LookupProber("codex")
	if !ok {
		t.Fatal("LookupProber(codex) returned ok=false")
	}
	if got != p {
		t.Errorf("got %p, want %p", got, p)
	}
}

func TestProberInvokeError(t *testing.T) {
	resetProbers()
	wantErr := errors.New("sentinel created")
	RegisterProber("codex", &fakeProber{err: wantErr})

	p, _ := LookupProber("codex")
	if err := p.SendSentinel(context.Background(), "/tmp/ws", "/tmp/ws/sentinel"); err != wantErr {
		t.Errorf("SendSentinel err = %v, want %v", err, wantErr)
	}
}

func TestErrUnsupportedSentinel(t *testing.T) {
	// Sanity check: provider implementations may return this as a
	// stable error their callers can branch on.
	if ErrUnsupported == nil {
		t.Fatal("ErrUnsupported should be non-nil")
	}
	if ErrUnsupported.Error() == "" {
		t.Error("ErrUnsupported should have a message")
	}
}
