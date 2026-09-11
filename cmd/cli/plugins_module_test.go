package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// TestPluginsModuleSuite runs the nested plugins module's own tests.
//
// plugins/ is a separate module (see go.work), so `go test ./...` from the
// root never reaches it — and neither does any gate. pr-tests.yml is
// workflow_dispatch-only, .githooks/pre-push starts with `exit 0`, and
// release.yml tests ./internal/... ./cmd/... ./pkg/...: it BUILDS the plugins
// module but never tests it. Nothing ran those tests at all.
//
// That gap had a price. connector/git shipped an --end-of-options that git
// 2.43 refuses in two places, so `reset` (every mode) and `branch_create` from
// a start-point could not run on the distro's own git — three red tests that
// had been sitting there unnoticed until someone ran the module by hand.
//
// The tidy fix is one line per workflow. This is the version that needs no
// workflow change: the root suite CI already runs reaches over and runs the
// nested one. Delete this file the day the workflows do it themselves.
//
// CI-only by default. It costs ~70s, which is not what someone running
// `go test ./cmd/...` on a laptop asked for — set WICK_PLUGINS_MODULE_TEST=1
// to run it locally.
func TestPluginsModuleSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the nested plugins module suite")
	}
	// The child inherits the environment, so without this marker a plugins
	// module that ever grows a cmd/cli of its own would recurse.
	if os.Getenv("WICK_IN_PLUGINS_MODULE_TEST") == "1" {
		t.Skip("already inside the plugins module run")
	}
	if os.Getenv("WICK_PLUGINS_MODULE_TEST") != "1" && os.Getenv("CI") == "" {
		t.Skip("set WICK_PLUGINS_MODULE_TEST=1 (or run in CI) to test the nested plugins module")
	}

	dir := filepath.Join(findModuleRoot(t), "plugins")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Skipf("no plugins module at %s: %v", dir, err)
	}

	cmd := safeexec.Command("go", "test", "-count=1", "-timeout", "10m", "./...")
	cmd.Dir = dir
	// -mod=readonly explicitly: a host that sets GOFLAGS=-mod=mod globally
	// cannot run anything in workspace mode, and go.work covers this module.
	cmd.Env = append(os.Environ(), "WICK_IN_PLUGINS_MODULE_TEST=1", "GOFLAGS=-mod=readonly")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plugins module tests failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	t.Logf("plugins module suite:\n%s", strings.TrimSpace(string(out)))
}
