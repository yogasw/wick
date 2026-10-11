package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Files that must name every Host API scope / method (KEEP IN SYNC in
// hostscopes.go), relative to the repo root.
const (
	hostAPIDoc        = "docs/plugins/host-api.md"
	authoringSkill    = "internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md"
	pluginsAdminSkill = "internal/agents/skillsync/builtin/wick-plugins/SKILL.md"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(here), "..", "..")
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestHostAPIDocsInSync is the KEEP IN SYNC guard: every scope in HostScopes,
// every RPC in HostMethods and every host_resources kind must appear, in
// backticks, in the Host API docs and the plugin-authoring skill; every scope
// also in the wick-plugins skill (its admin section). Adding one to
// hostscopes.go without documenting it fails here, naming the file to fix.
func TestHostAPIDocsInSync(t *testing.T) {
	var scopes, methods []string
	for _, s := range HostScopes {
		scopes = append(scopes, s.Name)
	}
	for _, m := range HostMethods {
		methods = append(methods, m.Name)
	}
	resources := []string{HostResourceRest, HostResourceDB, HostResourceConnectors, HostResourceConfig}

	want := map[string][]string{
		hostAPIDoc:        append(append(append([]string{}, scopes...), methods...), resources...),
		authoringSkill:    append(append(append([]string{}, scopes...), methods...), resources...),
		pluginsAdminSkill: scopes,
	}
	for _, file := range []string{hostAPIDoc, authoringSkill, pluginsAdminSkill} {
		body := readRepoFile(t, file)
		var missing []string
		for _, name := range want[file] {
			if !strings.Contains(body, "`"+name+"`") {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s does not mention %s (in backticks); document them there (see KEEP IN SYNC in pkg/plugin/hostscopes.go)",
				file, strings.Join(missing, ", "))
		}
	}
}
