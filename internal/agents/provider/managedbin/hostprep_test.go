package managedbin

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeELF is the smallest ELF64 aarch64 file debug/elf accepts: a header
// and one PT_INTERP program header pointing at interp ("" = static).
func fakeELF(interp string) []byte {
	const ehsize, phsize = 64, 56
	b := make([]byte, ehsize+phsize, ehsize+phsize+len(interp)+1)
	copy(b, "\x7fELF")
	b[4], b[5], b[6] = 2, 1, 1 // ELFCLASS64, little endian, EV_CURRENT
	le := binary.LittleEndian
	le.PutUint16(b[16:], 2)   // ET_EXEC
	le.PutUint16(b[18:], 183) // EM_AARCH64
	le.PutUint32(b[20:], 1)
	le.PutUint16(b[52:], ehsize)
	if interp == "" {
		return b[:ehsize]
	}
	le.PutUint64(b[32:], ehsize) // e_phoff
	le.PutUint16(b[54:], phsize)
	le.PutUint16(b[56:], 1) // e_phnum
	ph := b[ehsize:]
	le.PutUint32(ph[0:], 3) // PT_INTERP
	le.PutUint32(ph[4:], 4)
	le.PutUint64(ph[8:], ehsize+phsize)
	le.PutUint64(ph[32:], uint64(len(interp)+1))
	le.PutUint64(ph[40:], uint64(len(interp)+1))
	return append(append(b, interp...), 0)
}

func writeELF(t *testing.T, interp string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, fakeELF(interp), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsTermux(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	none := func(string) bool { return false }
	cases := []struct {
		name   string
		env    map[string]string
		exists func(string) bool
		want   bool
	}{
		{"TERMUX_VERSION", map[string]string{"TERMUX_VERSION": "0.118.1"}, none, true},
		{"PREFIX", map[string]string{"PREFIX": "/data/data/com.termux/files/usr"}, none, true},
		{"service without env", nil, func(p string) bool { return p == "/data/data/com.termux/files/usr" }, true},
		{"plain linux", map[string]string{"PREFIX": "/usr/local"}, none, false},
	}
	for _, c := range cases {
		if got := isTermux(env(c.env), c.exists); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	if got := (Host{OS: "linux", Arch: "arm64", Termux: true}).Label(); got != "linux-arm64 · termux" {
		t.Fatal(got)
	}
}

func TestElfInterp(t *testing.T) {
	if got, err := elfInterp(writeELF(t, "/lib/ld-linux-aarch64.so.1")); err != nil || got != "/lib/ld-linux-aarch64.so.1" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := elfInterp(writeELF(t, "")); err != nil || got != "" {
		t.Fatalf("static: %q %v", got, err)
	}
	sh := filepath.Join(t.TempDir(), "sh")
	_ = os.WriteFile(sh, []byte("#!/bin/sh\n"), 0o755)
	if got, err := elfInterp(sh); err != nil || got != "" {
		t.Fatalf("script: %q %v", got, err)
	}
}

func TestPrepareBinary(t *testing.T) {
	termux := Host{OS: "linux", Arch: "arm64", Termux: true}
	glibcLD := filepath.Join(t.TempDir(), "glibc", "lib", "ld-linux-aarch64.so.1")
	_ = os.MkdirAll(filepath.Dir(glibcLD), 0o700)
	_ = os.WriteFile(glibcLD, nil, 0o755)
	ran := 0
	hp := hostPrep{
		exists:  pathExists,
		resolve: func(string) (string, error) { return "/termux/bin/grun", nil },
		run: func(_ context.Context, bin string, args ...string) ([]byte, error) {
			ran++
			if bin != "/termux/bin/grun" || len(args) != 2 || args[0] != "-c" {
				t.Fatalf("ran %s %v", bin, args)
			}
			return nil, os.WriteFile(args[1], fakeELF(glibcLD), 0o755) // what patchelf does
		},
	}

	// off Termux: never touched, even with a missing loader
	if err := hp.prepareBinary(context.Background(), Host{OS: "linux", Arch: "arm64"}, writeELF(t, "/nope/ld.so")); err != nil || ran != 0 {
		t.Fatalf("non-termux: %v ran=%d", err, ran)
	}
	// loader present (a static build, or already patched): no grun
	if err := hp.prepareBinary(context.Background(), termux, writeELF(t, glibcLD)); err != nil || ran != 0 {
		t.Fatalf("present: %v ran=%d", err, ran)
	}
	// glibc build: patched with grun -c
	bin := writeELF(t, "/lib/ld-linux-aarch64.so.1")
	if err := hp.prepareBinary(context.Background(), termux, bin); err != nil || ran != 1 {
		t.Fatalf("patch: %v ran=%d", err, ran)
	}
	if got, _ := elfInterp(bin); got != glibcLD {
		t.Fatalf("interp after patch %q", got)
	}
	// musl build: no runtime on Termux
	if err := hp.prepareBinary(context.Background(), termux, writeELF(t, "/lib/ld-musl-aarch64.so.1")); !errors.Is(err, ErrMissingInterpreter) || !strings.Contains(err.Error(), "musl") {
		t.Fatalf("musl: %v", err)
	}
	// no glibc-runner: a clear hint instead of a bare fork/exec ENOENT
	noGrun := hp
	noGrun.resolve = func(string) (string, error) { return "", os.ErrNotExist }
	if err := noGrun.prepareBinary(context.Background(), termux, writeELF(t, "/lib/ld-linux-aarch64.so.1")); !errors.Is(err, ErrMissingInterpreter) || !strings.Contains(err.Error(), "pkg install glibc-runner") {
		t.Fatalf("no grun: %v", err)
	}
	// grun ran but left the interpreter missing
	noop := hp
	noop.run = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err := noop.prepareBinary(context.Background(), termux, writeELF(t, "/lib/ld-linux-aarch64.so.1")); !errors.Is(err, ErrMissingInterpreter) {
		t.Fatalf("noop grun: %v", err)
	}
	// grun failing surfaces its output
	failing := hp
	failing.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Error from glibc-runner: program 'patchelf' not found"), errors.New("exit status 1")
	}
	if err := failing.prepareBinary(context.Background(), termux, writeELF(t, "/lib/ld-linux-aarch64.so.1")); err == nil || !strings.Contains(err.Error(), "patchelf") {
		t.Fatalf("failing grun: %v", err)
	}
}

func TestExplainExecError(t *testing.T) {
	bin := writeELF(t, "/lib/ld-linux-aarch64.so.1")
	enoent := &os.PathError{Op: "fork/exec", Path: bin, Err: os.ErrNotExist}
	got := defaultHostPrep.explainExecError(Host{OS: "linux", Arch: "arm64", Termux: true}, bin, enoent)
	if !errors.Is(got, ErrMissingInterpreter) || !errors.Is(got, os.ErrNotExist) ||
		!strings.Contains(got.Error(), "/lib/ld-linux-aarch64.so.1") || !strings.Contains(got.Error(), "glibc-runner") {
		t.Fatalf("got %v", got)
	}
	other := errors.New("exit status 1")
	if got := defaultHostPrep.explainExecError(Host{OS: "linux"}, bin, other); got != other {
		t.Fatalf("non-ENOENT rewritten: %v", got)
	}
}
