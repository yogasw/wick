package safeexec_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestNoDirectOSExec walks the repo and fails if any .go file outside
// internal/safeexec/ calls os/exec.Command, exec.CommandContext, or
// exec.LookPath directly. Those callsites trigger Go's internal
// LookPath which uses faccessat2(2), rejected by Android/Termux seccomp
// on kernel < 5.8 → SIGSYS crash. Use the safeexec wrappers instead:
//
//	exec.Command(...)        → safeexec.Command(...)
//	exec.CommandContext(...) → safeexec.CommandContext(...)
//	exec.LookPath(...)       → safeexec.LookPath(...)
//
// safeexec/command.go has a small set of //nolint:forbidigo exemptions
// — the wrapper has to call the real exec functions internally. Those
// callsites live inside the safeexec package, which this scanner skips
// entirely (so the wrapper's own usage doesn't trip the test).
func TestNoDirectOSExec(t *testing.T) {
	root := repoRoot(t)
	banned := map[string]struct{}{
		"Command":        {},
		"CommandContext": {},
		"LookPath":       {},
	}

	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "graphify-out", "bin", "dist":
				return filepath.SkipDir
			case "safeexec":
				// Exempt the wrapper package itself — Command/LookPath
				// implementations have to call the real os/exec internals.
				if strings.HasSuffix(filepath.ToSlash(path), "internal/safeexec") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		// template/ ships as a scaffold for downstream apps and is not
		// part of the wick binary build, but we still want to keep it
		// consistent — downstream forks inherit the same Termux risk.
		// Include it in the scan.

		offenses, perr := scanFile(path)
		if perr != nil {
			return perr
		}
		for _, off := range offenses {
			if _, bad := banned[off.fn]; !bad {
				continue
			}
			offenders = append(offenders,
				rel+":"+strconv.Itoa(off.line)+
					": exec."+off.fn+"(...) — replace with safeexec."+off.fn)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("found %d direct os/exec callsites — replace with safeexec wrappers:\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

type offense struct {
	fn   string
	line int
}

// scanFile parses path and returns every selector call of the form
// `<alias>.<name>(...)` where <alias> is the import alias for os/exec
// (defaults to "exec"). Dot-imports and underscore-imports are skipped
// — both are rare enough that flagging them as scanner gaps gives
// false-positive noise.
func scanFile(path string) ([]offense, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	alias := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != `"os/exec"` {
			continue
		}
		switch {
		case imp.Name == nil:
			alias = "exec"
		case imp.Name.Name == "_" || imp.Name.Name == ".":
			return nil, nil
		default:
			alias = imp.Name.Name
		}
		break
	}
	if alias == "" {
		return nil, nil
	}

	var out []offense
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != alias {
			return true
		}
		out = append(out, offense{
			fn:   sel.Sel.Name,
			line: fset.Position(call.Pos()).Line,
		})
		return true
	})
	return out, nil
}

// repoRoot walks up from this test file's directory until it finds
// go.mod, so the scanner works regardless of where `go test` was
// invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}
