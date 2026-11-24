package builder

import (
	"github.com/yogasw/wick/pkg/safeexec"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/buildsource"
	"github.com/yogasw/wick/internal/pkg/envscrub"
)

func sourceGit(dir string, args ...string) string {
	cmd := safeexec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = envscrub.ScrubOSEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sourceMetadata discovers a local Wick module replacement. This also works
// when an older deploy script has just pulled the new builder, and for
// downstream apps that build against a local Wick checkout.
func sourceMetadata() (buildsource.Metadata, bool) {
	dir := os.Getenv("WICK_BUILD_SOURCE_DIR")
	if dir == "" {
		cmd := safeexec.Command("go", "list", "-m", "-f", "{{if .Replace}}{{.Replace.Dir}}{{end}}", "github.com/yogasw/wick")
		cmd.Env = envscrub.ScrubOSEnv()
		out, err := cmd.Output()
		if err != nil {
			return buildsource.Metadata{}, false
		}
		dir = strings.TrimSpace(string(out))
	}
	if dir == "" {
		return buildsource.Metadata{}, false
	}
	rev := sourceGit(dir, "rev-parse", "HEAD")
	if len(rev) != 40 {
		return buildsource.Metadata{}, false
	}

	version, _ := os.ReadFile(filepath.Join(dir, "VERSION"))
	m := buildsource.Metadata{
		Kind:          "source",
		Revision:      rev,
		CommitDate:    sourceGit(dir, "log", "-1", "--format=%cI", rev),
		SourceVersion: buildsource.StableVersion(string(version)),
		Repository:    buildsource.GitHubRepository(sourceGit(dir, "remote", "get-url", "origin")),
		Branch:        sourceGit(dir, "symbolic-ref", "--quiet", "--short", "HEAD"),
	}
	// The deploy script records clean/dirty before generating build artifacts.
	// If no stamp was supplied, inspect the local tracked working tree.
	switch stamped := os.Getenv("WICK_BUILD_COMMIT"); {
	case stamped == rev:
		m.Dirty = false
	case stamped != "":
		m.Dirty = true
	default:
		cmd := safeexec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no")
		cmd.Env = envscrub.ScrubOSEnv()
		out, err := cmd.Output()
		m.Dirty = err != nil || strings.TrimSpace(string(out)) != ""
	}
	if m.Repository != "" && m.Branch != "" {
		up := sourceGit(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if strings.HasPrefix(up, "origin/") {
			m.Upstream = up
			m.CompareRef = strings.TrimPrefix(up, "origin/")
		} else if up == "" && (sourceGit(dir, "rev-parse", "--verify", "refs/remotes/origin/"+m.Branch) != "" || os.Getenv("WICK_BUILD_REF") == m.Branch) {
			m.Upstream = "origin/" + m.Branch
			m.CompareRef = m.Branch
		}
	}
	if m.Branch == "" {
		// A detached checkout may be a deliberately fetched branch or a tag.
		// The deploy script supplies a ref only when it actually fetched it.
		ref := os.Getenv("WICK_BUILD_REF")
		if ref != "" && sourceGit(dir, "rev-parse", "--verify", "refs/tags/"+ref+"^{commit}") == rev {
			m.Tag = ref
		} else if ref != "" && m.Repository != "" {
			m.Branch = ref
			m.Upstream = "origin/" + ref
			m.CompareRef = ref
		} else {
			m.Tag = sourceGit(dir, "describe", "--tags", "--exact-match", rev)
		}
	}
	return m, true
}
