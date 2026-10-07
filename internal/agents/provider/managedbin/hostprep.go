package managedbin

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// termuxGlibcHint is what a Termux user has to run before a glibc build
// can start natively (no proot): glibc-runner ships glibc under
// $PREFIX/glibc plus `grun`, whose -c rewrites a binary's interpreter and
// rpath to it (patchelf).
const termuxGlibcHint = "install glibc-runner in Termux: `pkg install glibc-repo && pkg install glibc-runner`, then download again"

// ErrMissingInterpreter: the binary is dynamically linked against a
// loader this host does not have, so exec fails with a bare ENOENT.
var ErrMissingInterpreter = errors.New("ELF interpreter missing on this host")

// hostPrep is what prepareBinary touches on the machine, swapped in tests.
type hostPrep struct {
	exists  func(string) bool
	resolve func(string) (string, error)
	run     func(ctx context.Context, bin string, args ...string) ([]byte, error)
}

var defaultHostPrep = hostPrep{
	exists:  pathExists,
	resolve: safeexec.ResolveBin,
	run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		c := safeexec.CommandContext(ctx, bin, args...)
		c.Env = envscrub.ScrubOSEnv()
		return c.CombinedOutput()
	},
}

// elfInterp returns the PT_INTERP path of an ELF file, "" when it has
// none (static) or is not ELF at all (a script, a Mach-O).
func elfInterp(path string) (string, error) {
	r, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != elf.ELFMAG {
		return "", nil
	}
	f, err := elf.NewFile(r)
	if err != nil {
		return "", err
	}
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(p.Open(), 4096))
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\x00"), nil
	}
	return "", nil
}

// prepareBinary makes a freshly unpacked binary runnable on h before the
// sandboxed --version. Off Termux it is a no-op. On Termux a glibc build
// is patched in place with `grun -c` so it execs natively — normal spawns
// then need no wrapper, and running through ld.so instead would break Bun
// executables (opencode, omp) that read their payload via /proc/self/exe.
// It runs before the sha256 is recorded, so the patched file is what
// Activate re-verifies later.
func (hp hostPrep) prepareBinary(ctx context.Context, h Host, bin string) error {
	if !h.Termux {
		return nil
	}
	interp, err := elfInterp(bin)
	if err != nil || interp == "" || hp.exists(interp) {
		return err
	}
	if strings.Contains(interp, "ld-musl") {
		return fmt.Errorf("%w: needs %s and Termux has no musl runtime; use the glibc build with glibc-runner", ErrMissingInterpreter, interp)
	}
	grun, err := hp.resolve("grun")
	if err != nil {
		return fmt.Errorf("%w: needs %s (glibc); %s", ErrMissingInterpreter, interp, termuxGlibcHint)
	}
	// patchelf rewrites a few hundred MB on a phone: give it room.
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if out, err := hp.run(cctx, grun, "-c", bin); err != nil {
		return fmt.Errorf("grun -c: %w: %s", err, strings.TrimSpace(string(out)))
	}
	patched, err := elfInterp(bin)
	if err != nil {
		return err
	}
	if patched == "" || !hp.exists(patched) {
		return fmt.Errorf("%w: interpreter is %q after grun -c; %s", ErrMissingInterpreter, patched, termuxGlibcHint)
	}
	return nil
}

// explainExecError turns the bare "no such file or directory" exec gives
// for a missing loader into one naming the loader, since the binary
// itself is plainly there.
func (hp hostPrep) explainExecError(h Host, bin string, err error) error {
	if !errors.Is(err, os.ErrNotExist) || !hp.exists(bin) {
		return err
	}
	interp, ierr := elfInterp(bin)
	if ierr != nil || interp == "" || hp.exists(interp) {
		return err
	}
	hint := ""
	if h.Termux {
		hint = "; " + termuxGlibcHint
	}
	return fmt.Errorf("%w (%s: needs %s)%s: %w", ErrMissingInterpreter, h.Label(), interp, hint, err)
}
