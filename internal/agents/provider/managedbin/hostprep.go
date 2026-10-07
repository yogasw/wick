package managedbin

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// termuxGlibcHint is what a Termux user has to run before a glibc build
// can start natively (no proot): glibc-runner ships glibc, loader
// included, under $PREFIX/glibc.
const termuxGlibcHint = "install glibc-runner in Termux: `pkg install glibc-repo && pkg install glibc-runner`, then download again"

// termuxPrefix is Termux's $PREFIX when the env does not carry it (a
// service started outside a Termux shell).
const termuxPrefix = "/data/data/com.termux/files/usr"

// ErrMissingInterpreter: the binary is dynamically linked against a
// loader this host does not have, so exec fails with a bare ENOENT.
var ErrMissingInterpreter = errors.New("ELF interpreter missing on this host")

// errInterpTooLong: the new interpreter path does not fit in the bytes
// PT_INTERP already has, so it cannot be rewritten in place.
var errInterpTooLong = errors.New("interpreter path longer than PT_INTERP")

// hostPrep is what prepareBinary touches on the machine, swapped in tests.
type hostPrep struct {
	exists func(string) bool
	// prefix is Termux's $PREFIX; glibc-runner lives in prefix/glibc.
	prefix func() string
	// loaderLink is the short path the rewritten PT_INTERP points at, a
	// symlink wick owns to glibc-runner's loader.
	loaderLink func(prefix string) string
}

var defaultHostPrep = hostPrep{
	exists: pathExists,
	prefix: func() string {
		if p := os.Getenv("PREFIX"); strings.Contains(p, "/com.termux/") {
			return p
		}
		return termuxPrefix
	},
	// $PREFIX/../.. is the app's data dir (/data/data/com.termux), the
	// shortest path the app can write: "/data/data/com.termux/ld" is 24
	// bytes, inside the 26 of "/lib/ld-linux-aarch64.so.1".
	loaderLink: func(prefix string) string {
		return filepath.Join(filepath.Dir(filepath.Dir(prefix)), "ld")
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
	p, err := interpProg(r)
	if err != nil || p == nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(p.Open(), 4096))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\x00"), nil
}

// interpProg returns r's PT_INTERP program header, nil when r is not ELF
// or has none.
func interpProg(r io.ReaderAt) (*elf.Prog, error) {
	var magic [4]byte
	if _, err := r.ReadAt(magic[:], 0); err != nil || string(magic[:]) != elf.ELFMAG {
		return nil, nil
	}
	f, err := elf.NewFile(r)
	if err != nil {
		return nil, err
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return p, nil
		}
	}
	return nil, nil
}

// rewriteInterp overwrites the PT_INTERP string of path in place with
// interp, NUL-padded to the segment's existing size. Only those bytes
// change: file size, offsets and every other segment stay as they were,
// which is what keeps Bun executables (opencode, omp) intact — they find
// their payload at fixed offsets in /proc/self/exe, and a relayout by
// patchelf crashes them in the loader.
func rewriteInterp(path, interp string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	p, err := interpProg(f)
	if err == nil && p == nil {
		err = errors.New("no PT_INTERP")
	}
	if err == nil && uint64(len(interp))+1 > p.Filesz {
		err = fmt.Errorf("%w: %q needs %d bytes, has %d", errInterpTooLong, interp, len(interp)+1, p.Filesz)
	}
	if err == nil {
		buf := make([]byte, p.Filesz)
		copy(buf, interp)
		_, err = f.WriteAt(buf, int64(p.Off))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ensureSymlink points link at target, replacing a stale symlink
// atomically. Anything at link that is not a symlink is left alone.
func ensureSymlink(target, link string) error {
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s exists and is not a symlink", link)
	}
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// writeLauncher is the fallback when PT_INTERP cannot be pointed at the
// loader: the binary moves to <bin>.real and bin becomes a script that
// runs it through the loader explicitly. /proc/self/exe is then ld.so,
// which opencode tolerates; the recorded sha256 covers the script only.
func writeLauncher(bin, loader, shell string) error {
	real := bin + ".real"
	if err := os.Rename(bin, real); err != nil {
		return err
	}
	script := "#!" + shell + "\nexec " + shQuote(loader) + ` "${0%/*}/` + filepath.Base(real) + `" "$@"` + "\n"
	return os.WriteFile(bin, []byte(script), 0o755)
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// prepareBinary makes a freshly unpacked binary runnable on h before the
// sandboxed --version. Off Termux it is a no-op. On Termux a glibc build
// gets its PT_INTERP rewritten in place to a short symlink wick keeps
// pointing at glibc-runner's loader, which already searches
// $PREFIX/glibc/lib, so the binary execs natively with no env and no
// wrapper. When the symlink cannot be made, or does not fit, a launcher
// script takes the binary's place instead. It runs before the sha256 is
// recorded, so the prepared file is what Activate re-verifies later.
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
	prefix := hp.prefix()
	loader := filepath.Join(prefix, "glibc", "lib", filepath.Base(interp))
	if !hp.exists(loader) {
		return fmt.Errorf("%w: needs %s (glibc), %s not found; %s", ErrMissingInterpreter, interp, loader, termuxGlibcHint)
	}
	link := hp.loaderLink(prefix)
	linkErr := ensureSymlink(loader, link)
	if linkErr == nil {
		linkErr = rewriteInterp(bin, link)
	}
	if linkErr == nil {
		if got, err := elfInterp(bin); err != nil || got != link {
			return fmt.Errorf("interpreter is %q after rewrite: %v", got, err)
		}
		return nil
	}
	if errors.Is(linkErr, errInterpTooLong) {
		// nothing was written; the file is still the original
	} else if got, _ := elfInterp(bin); got != interp {
		return fmt.Errorf("rewrite interpreter to %s: %w", link, linkErr)
	}
	shell := filepath.Join(prefix, "bin", "sh")
	if !hp.exists(shell) {
		return fmt.Errorf("%w: cannot link %s (%v) and no %s for a launcher", ErrMissingInterpreter, link, linkErr, shell)
	}
	if err := writeLauncher(bin, loader, shell); err != nil {
		return fmt.Errorf("launcher after %v: %w", linkErr, err)
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
