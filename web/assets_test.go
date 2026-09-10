package web

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// buildOutputs are the files under public/ that are produced by a build step
// rather than written by hand — the Tailwind stylesheet and the shared
// markdown bundle from fe/common/md. embed.go names both explicitly, so a
// missing one is a compile error; these tests cover what a compile error
// cannot say.
var buildOutputs = []string{
	"public/css/app.css",
	"public/lib/wick-markdown.js",
}

// TestPublicFilesCarriesTheBuildOutputs: an empty or truncated asset embeds
// and compiles perfectly well, and the failure it produces is a UI that comes
// up unstyled with a service worker still serving the previous copy — so the
// content, not just the presence, is worth an assertion.
func TestPublicFilesCarriesTheBuildOutputs(t *testing.T) {
	for _, p := range buildOutputs {
		b, err := fs.ReadFile(PublicFiles, p)
		if err != nil {
			t.Errorf("%s is not in the embedded FS: %v", p, err)
			continue
		}
		if len(b) < 512 {
			t.Errorf("%s is embedded but only %d bytes — the build that writes it produced nothing usable", p, len(b))
		}
	}
}

// TestBuildOutputsAreTracked is the test that keeps the tree buildable.
//
// Both files were gitignored until the go:embed patterns in embed.go named
// them, at which point their absence stopped being a cosmetic problem and
// became "no package that imports web/ compiles". A fresh clone could not run
// `go build ./...`, and the release pipeline's unit-test job — which runs
// templ but no asset build — failed before a single test executed. Tracking
// them fixes that everywhere at once, and this test is what stops someone
// re-adding the ignore rule and rediscovering it through a red pipeline.
//
// Skipped where there is no checkout to ask: `go install <module>@<version>`
// builds from the module cache, which has no .git.
func TestBuildOutputsAreTracked(t *testing.T) {
	git, err := safeexec.LookPath("git")
	if err != nil {
		t.Skip("no git binary — nothing to ask about tracked files")
	}
	if _, err := os.Stat("../.git"); err != nil {
		t.Skip("not a git checkout (module cache build)")
	}
	for _, p := range buildOutputs {
		path := "web/" + p
		cmd := safeexec.Command(git, "ls-files", "--error-unmatch", "--", path)
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s is not tracked by git (%v: %s)\n"+
				"embed.go names it in a go:embed pattern, so an untracked copy means "+
				"a fresh clone and every CI job that does not build assets first fail to "+
				"compile. Commit the built file, or drop the pattern.",
				path, err, strings.TrimSpace(string(out)))
		}
	}
}
