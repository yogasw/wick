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
func fakeELF(interp string) []byte { return fakeELFSized(interp, len(interp)+1) }

// fakeELFSized is fakeELF with a PT_INTERP of filesz bytes, NUL-padded,
// last in the file.
func fakeELFSized(interp string, filesz int) []byte {
	const ehsize, phsize = 64, 56
	b := make([]byte, ehsize+phsize, ehsize+phsize+filesz)
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
	le.PutUint64(ph[32:], uint64(filesz))
	le.PutUint64(ph[40:], uint64(filesz))
	return append(append(b, interp...), make([]byte, filesz-len(interp))...)
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

// termuxPrep is a hostPrep over a fake Termux tree under t.TempDir():
// root/files/usr is $PREFIX, with glibc-runner's loader and a sh.
func termuxPrep(t *testing.T, withLoader bool) (hostPrep, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "com.termux")
	prefix := filepath.Join(root, "files", "usr")
	loader := filepath.Join(prefix, "glibc", "lib", "ld-linux-aarch64.so.1")
	_ = os.MkdirAll(filepath.Join(prefix, "bin"), 0o700)
	_ = os.WriteFile(filepath.Join(prefix, "bin", "sh"), nil, 0o755)
	if withLoader {
		_ = os.MkdirAll(filepath.Dir(loader), 0o700)
		_ = os.WriteFile(loader, nil, 0o755)
	}
	hp := defaultHostPrep
	hp.prefix = func() string { return prefix }
	return hp, root, loader
}

func TestPrepareBinaryRewritesInterpInPlace(t *testing.T) {
	termux := Host{OS: "linux", Arch: "arm64", Termux: true}
	// opencode and omp ship the same Bun layout: the interpreter path
	// must change without moving a single other byte.
	for _, name := range []string{"opencode", "omp"} {
		t.Run(name, func(t *testing.T) {
			hp, root, loader := termuxPrep(t, true)
			const real = "/lib/ld-linux-aarch64.so.1"
			link := filepath.Join(root, "ld")
			// on the phone 27 bytes hold "/data/data/com.termux/ld"; a
			// temp dir is longer, so the segment is sized to fit it
			filesz := max(len(real), len(link)) + 1
			bin := filepath.Join(t.TempDir(), "bin")
			if err := os.WriteFile(bin, fakeELFSized(real, filesz), 0o755); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(bin)
			sumBefore, _ := fileSHA256(bin)
			if err := hp.prepareBinary(context.Background(), termux, bin); err != nil {
				t.Fatal(err)
			}
			if got, err := os.Readlink(link); err != nil || got != loader {
				t.Fatalf("link %q %v", got, err)
			}
			if got, _ := elfInterp(bin); got != link {
				t.Fatalf("interp %q", got)
			}
			after, _ := os.ReadFile(bin)
			if len(after) != len(before) {
				t.Fatalf("size %d -> %d", len(before), len(after))
			}
			off := len(before) - filesz // INTERP is last
			for i := range before {
				if before[i] != after[i] && i < off {
					t.Fatalf("byte %d outside PT_INTERP changed", i)
				}
			}
			if want := link + strings.Repeat("\x00", filesz-len(link)); string(after[off:]) != want {
				t.Fatalf("INTERP bytes %q", after[off:])
			}
			// the sha256 recorded after prepare is the rewritten file's
			if sum, _ := fileSHA256(bin); sum == sumBefore {
				t.Fatal("sha256 unchanged after rewrite")
			}
			// second install: link reused, interp already resolves
			again := filepath.Join(t.TempDir(), "bin")
			_ = os.WriteFile(again, fakeELFSized(real, filesz), 0o755)
			if err := hp.prepareBinary(context.Background(), termux, again); err != nil {
				t.Fatal(err)
			}
			if got, _ := elfInterp(again); got != link {
				t.Fatalf("second interp %q", got)
			}
		})
	}
}

func TestPrepareBinary(t *testing.T) {
	ctx := context.Background()
	termux := Host{OS: "linux", Arch: "arm64", Termux: true}
	hp, root, loader := termuxPrep(t, true)
	link := filepath.Join(root, "ld")

	// off Termux: never touched, even with a missing loader
	bin := writeELF(t, "/nope/ld.so")
	if err := hp.prepareBinary(ctx, Host{OS: "linux", Arch: "arm64"}, bin); err != nil {
		t.Fatal(err)
	}
	if got, _ := elfInterp(bin); got != "/nope/ld.so" {
		t.Fatalf("non-termux rewritten to %q", got)
	}
	// loader present (static build, or already prepared): untouched
	if err := hp.prepareBinary(ctx, termux, writeELF(t, loader)); err != nil {
		t.Fatal(err)
	}
	// a stale link wick made earlier is repointed
	_ = os.Symlink("/old/ld.so", link)
	bin = filepath.Join(t.TempDir(), "bin")
	_ = os.WriteFile(bin, fakeELFSized("/lib/ld-linux-aarch64.so.1", len(link)+1), 0o755)
	if err := hp.prepareBinary(ctx, termux, bin); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != loader {
		t.Fatalf("stale link -> %q", got)
	}
	if got, _ := elfInterp(bin); got != link {
		t.Fatalf("interp %q", got)
	}
	// musl build: no runtime on Termux
	if err := hp.prepareBinary(ctx, termux, writeELF(t, "/lib/ld-musl-aarch64.so.1")); !errors.Is(err, ErrMissingInterpreter) || !strings.Contains(err.Error(), "musl") {
		t.Fatalf("musl: %v", err)
	}
	// no glibc-runner: a clear hint instead of a bare fork/exec ENOENT
	noRunner, _, _ := termuxPrep(t, false)
	if err := noRunner.prepareBinary(ctx, termux, writeELF(t, "/lib/ld-linux-aarch64.so.1")); !errors.Is(err, ErrMissingInterpreter) || !strings.Contains(err.Error(), "pkg install glibc-runner") {
		t.Fatalf("no glibc-runner: %v", err)
	}
}

func TestPrepareBinaryLauncherFallback(t *testing.T) {
	ctx := context.Background()
	termux := Host{OS: "linux", Arch: "arm64", Termux: true}
	check := func(t *testing.T, hp hostPrep, loader, bin string, orig []byte) {
		t.Helper()
		if err := hp.prepareBinary(ctx, termux, bin); err != nil {
			t.Fatal(err)
		}
		if real, _ := os.ReadFile(bin + ".real"); string(real) != string(orig) {
			t.Fatal(".real is not the untouched download")
		}
		script, _ := os.ReadFile(bin)
		if !strings.Contains(string(script), "exec '"+loader+"' \"${0%/*}/bin.real\" \"$@\"") {
			t.Fatalf("launcher %q", script)
		}
	}
	t.Run("link path does not fit", func(t *testing.T) {
		hp, _, loader := termuxPrep(t, true)
		long := filepath.Join(t.TempDir(), "a-path-longer-than-26-bytes")
		_ = os.MkdirAll(long, 0o700)
		hp.loaderLink = func(string) string { return filepath.Join(long, "ld") }
		bin := writeELF(t, "/lib/ld-linux-aarch64.so.1")
		orig, _ := os.ReadFile(bin)
		check(t, hp, loader, bin, orig)
	})
	t.Run("link path taken by a file", func(t *testing.T) {
		hp, root, loader := termuxPrep(t, true)
		_ = os.WriteFile(filepath.Join(root, "ld"), []byte("not ours"), 0o600)
		bin := writeELF(t, "/lib/ld-linux-aarch64.so.1")
		orig, _ := os.ReadFile(bin)
		check(t, hp, loader, bin, orig)
		if b, _ := os.ReadFile(filepath.Join(root, "ld")); string(b) != "not ours" {
			t.Fatal("foreign file at link path replaced")
		}
	})
}

func TestRewriteInterpTooLong(t *testing.T) {
	bin := writeELF(t, "/lib/ld.so")
	before, _ := os.ReadFile(bin)
	if err := rewriteInterp(bin, "/data/data/com.termux/ld"); !errors.Is(err, errInterpTooLong) {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(bin); string(after) != string(before) {
		t.Fatal("file changed on a failed rewrite")
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
