package envscrub

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// inheritAllowed lists the files whose exec sites may hand their child the
// daemon's full environment. Everything else must set cmd.Env — normally to
// ScrubOSEnv() — in the same function that builds the command.
//
// This list is the point of the test. A new spawner does not get to inherit
// DATABASE_URL by forgetting to think about it: it fails here, and whoever
// adds it has to decide which side it belongs on. Add a file only when its
// child is wick itself (and needs the DSN), or is a fixed system tool that
// never runs caller-supplied commands.
var inheritAllowed = map[string]string{
	// The wrapper every other site goes through; it builds, it does not spawn.
	"pkg/safeexec/command.go": "wrapper entrypoint",

	// wick re-executing or supervising itself: the child IS the daemon.
	"internal/pkg/daemon/service_linux.go":    "systemctl/journalctl, and the daemon's own unit",
	"internal/pkg/daemon/supervised_linux.go": "starts the wick daemon child, which needs DATABASE_URL",
	"internal/pkg/daemon/binary.go":           "probes the wick binary itself",
	"internal/updater/updater.go":             "installs and restarts wick itself",
	"internal/updater/asset_darwin.go":        "hdiutil/ditto on an update asset",
	"internal/pkg/daemon/daemon.go":           "starts the wick daemon, which needs DATABASE_URL",

	// Fixed build/packaging tools with fixed arguments.
	"internal/builder/gate.go":              "go list on a fixed module path",
	"internal/builder/windows/msi.go":       "wix packaging",
	"internal/builder/darwin/dmg.go":        "hdiutil packaging",
	"internal/builder/darwin/sign.go":       "codesign",
	"cmd/cli/plugin.go":                     "developer CLI, runs in the operator's own shell",
	"cmd/cli/run.go":                        "developer CLI, runs in the operator's own shell",
	"cmd/cli/mcp.go":                        "developer CLI, runs in the operator's own shell",
	"cmd/cli/doctor.go":                     "developer CLI, runs in the operator's own shell",
	"cmd/cli/init.go":                       "developer CLI, runs in the operator's own shell",
	"internal/systemtray/notify_windows.go": "desktop notification helper",
	"internal/systemtray/notify_other.go":   "desktop notification helper",
	"internal/systemtray/editor_windows.go": "opens a file in the desktop editor",
	"internal/systemtray/editor_other.go":   "opens a file in the desktop editor",

	// Process bookkeeping: fixed tools, no caller-supplied command.
	"internal/agents/provider/terminate_windows.go":          "taskkill",
	"internal/agents/gate/claude_hook.go":                    "reads claude's own version/config",
	"internal/agents/airouter/manager.go":                    "version probe; the router spawn itself is scrubbed",
	"plugins/connector/playwright_browser/orphan_windows.go": "taskkill of an orphaned browser",
	"plugins/connector/playwright_browser/ids.go":            "ps lookup of browser pids",
	"plugins/connector/git/proc_windows.go":                  "taskkill of a git child",
	"internal/agents/provider/memscope/backend_linux.go":     "systemd-run probe of /bin/true",
	"internal/agents/provider/memscope/install_linux.go":     "systemctl --user daemon-reload",
	"internal/pkg/netboot/netboot.go":                        "getprop on Android",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// TestEverySpawnSiteScrubsOrIsAllowed walks the repository and fails on any
// exec.Command / safeexec.Command whose enclosing function never assigns a
// .Env and whose file is not in inheritAllowed.
func TestEverySpawnSiteScrubsOrIsAllowed(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	seenAllowed := map[string]bool{}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata", "template", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil // generated or build-tagged oddities are not this test's concern
		}
		for _, site := range unscrubbedSites(fset, f) {
			if _, ok := inheritAllowed[rel]; ok {
				seenAllowed[rel] = true
				continue
			}
			offenders = append(offenders, rel+":"+site)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("spawn site inherits the daemon environment (DATABASE_URL included): %s\n"+
			"\tset cmd.Env = envscrub.ScrubOSEnv() in the same function, or add the file to inheritAllowed with a reason", o)
	}

	// A stale allowlist entry is a hole waiting to be reused: once a file
	// stops spawning, it should stop being trusted to.
	for f := range inheritAllowed {
		if !seenAllowed[f] {
			t.Errorf("inheritAllowed lists %s, which no longer has an unscrubbed spawn site — remove it", f)
		}
	}
}

// unscrubbedSites returns "line" for every exec call in f whose enclosing
// function body contains no assignment to a field named Env.
func unscrubbedSites(fset *token.FileSet, f *ast.File) []string {
	var out []string
	var visit func(body *ast.BlockStmt)
	visit = func(body *ast.BlockStmt) {
		if body == nil {
			return
		}
		setsEnv := false
		var calls []token.Pos
		ast.Inspect(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				visit(x.Body) // a closure is judged on its own body
				return false
			case *ast.AssignStmt:
				for _, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Env" {
						setsEnv = true
					}
				}
			case *ast.CallExpr:
				if isExecCall(x) {
					calls = append(calls, x.Pos())
				}
			}
			return true
		})
		if setsEnv {
			return
		}
		for _, pos := range calls {
			out = append(out, itoa(fset.Position(pos).Line))
		}
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			visit(fn.Body)
		}
	}
	return out
}

func isExecCall(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if pkg.Name != "exec" && pkg.Name != "safeexec" {
		return false
	}
	return sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
