package plugin

import (
	"os"
	"path/filepath"
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

// readRepoFile reads rel from the repo root. go test runs with the package
// directory as cwd, so the root is two levels up. A missing file fails the
// test: the guard must never pass by not looking.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
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

// scopeTableSpec says where each file's scope table keeps the values the
// code decides: the Default column, the destructive / sensitive marks and
// (when present) the host_resources kind a scope needs.
type scopeTableSpec struct {
	file        string
	flagCol     string // column carrying the destructive / sensitive mark
	destructive string // mark for HostScope.Destructive
	sensitive   string // mark for HostScope.Sensitive
	exactFlag   bool   // the flag cell is exactly the mark (else: contains it)
	resourceCol string // column naming the host_resources kind; "" = none
}

var scopeTables = []scopeTableSpec{
	{file: hostAPIDoc, flagCol: "Marked", destructive: "destructive", sensitive: "sensitive", exactFlag: true, resourceCol: "Needs declaration"},
	{file: authoringSkill, flagCol: "RPCs", destructive: "(destructive)", sensitive: "(sensitive)", resourceCol: "Needs `host_resources`"},
	{file: pluginsAdminSkill, flagCol: "Notes", destructive: "— destructive", sensitive: "— sensitive"},
}

// parseScopeTable returns the first markdown table whose header starts with
// "Scope", as scope name -> column header -> trimmed cell.
func parseScopeTable(t *testing.T, file, body string) map[string]map[string]string {
	t.Helper()
	split := func(line string) []string {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		return cells
	}
	var header []string
	rows := map[string]map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if header == nil {
			if strings.HasPrefix(line, "| Scope |") {
				header = split(line)
			}
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := split(line)
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		if len(cells) != len(header) {
			t.Fatalf("%s: scope table row has %d cells, header has %d: %s", file, len(cells), len(header), line)
		}
		row := map[string]string{}
		for i, h := range header {
			row[h] = cells[i]
		}
		rows[strings.Trim(cells[0], "`")] = row
	}
	if header == nil {
		t.Fatalf("%s: no scope table (a line starting with \"| Scope |\")", file)
	}
	return rows
}

// TestHostAPIDocsScopeTables locks the documented Default, destructive /
// sensitive marks and host_resources kind of every scope to HostScopes, so
// flipping e.g. db.read to allow-by-default without updating the docs and
// skills fails here.
func TestHostAPIDocsScopeTables(t *testing.T) {
	for _, spec := range scopeTables {
		rows := parseScopeTable(t, spec.file, readRepoFile(t, spec.file))
		for _, s := range HostScopes {
			row, ok := rows[s.Name]
			if !ok {
				t.Errorf("%s: scope table has no row for `%s`", spec.file, s.Name)
				continue
			}

			def := strings.ToLower(strings.Trim(row["Default"], "* "))
			wantDef := "off"
			if s.DefaultAllow {
				wantDef = "allow"
			}
			if def != wantDef {
				t.Errorf("%s: `%s` Default = %q, code says %q (DefaultAllow=%v)", spec.file, s.Name, row["Default"], wantDef, s.DefaultAllow)
			}

			flag := row[spec.flagCol]
			has := func(mark string) bool {
				if spec.exactFlag {
					return flag == mark
				}
				return strings.Contains(flag, mark)
			}
			if has(spec.destructive) != s.Destructive {
				t.Errorf("%s: `%s` %s = %q, code says Destructive=%v", spec.file, s.Name, spec.flagCol, flag, s.Destructive)
			}
			if has(spec.sensitive) != s.Sensitive {
				t.Errorf("%s: `%s` %s = %q, code says Sensitive=%v", spec.file, s.Name, spec.flagCol, flag, s.Sensitive)
			}

			if spec.resourceCol != "" {
				wantRes := "–"
				if s.Resource != "" {
					wantRes = "`" + s.Resource + "`"
				}
				if got := row[spec.resourceCol]; got != wantRes {
					t.Errorf("%s: `%s` %s = %q, code says %q", spec.file, s.Name, spec.resourceCol, got, wantRes)
				}
			}
		}
	}
}
